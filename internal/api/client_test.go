package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNew(t *testing.T) {
	c := New("https://api.test.com", "pk_test")
	if c.baseURL != "https://api.test.com" {
		t.Errorf("baseURL = %q", c.baseURL)
	}
	if c.apiKey != "pk_test" {
		t.Errorf("apiKey = %q", c.apiKey)
	}
}

func TestGetAccount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/account/info" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer pk_test" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode(AccountResponse{Email: "user@test.com", BalanceUSD: 9.97})
	}))
	defer srv.Close()

	c := New(srv.URL, "pk_test")
	resp, err := c.GetAccount()
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if resp.Email != "user@test.com" {
		t.Errorf("email = %q", resp.Email)
	}
	if resp.BalanceUSD != 9.97 {
		t.Errorf("balance = %f", resp.BalanceUSD)
	}
}

func TestGetBalance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"balance_usd": 5.50,
			"low_balance": true,
			"byte_budget": 1833333333,
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "pk_test")
	resp, err := c.GetBalance()
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if resp.BalanceUSD != 5.50 {
		t.Errorf("balance = %f", resp.BalanceUSD)
	}
	if !resp.LowBalance {
		t.Error("should be low balance")
	}
	if resp.ByteBudget != 1833333333 {
		t.Errorf("byte_budget = %d, want 1833333333", resp.ByteBudget)
	}
}

func TestGetBalance_WithOpts(t *testing.T) {
	var gotType, gotSession string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotType = r.URL.Query().Get("type")
		gotSession = r.URL.Query().Get("session_id")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"balance_usd": 5.00,
			"byte_budget": 5000000000,
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "pk_test")

	// With type + session_id
	resp, err := c.GetBalance(BalanceOpts{IPType: "datacenter", SessionID: "sess-123"})
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if gotType != "datacenter" {
		t.Errorf("expected type=datacenter, got %q", gotType)
	}
	if gotSession != "sess-123" {
		t.Errorf("expected session_id=sess-123, got %q", gotSession)
	}
	if resp.ByteBudget != 5000000000 {
		t.Errorf("byte_budget = %d", resp.ByteBudget)
	}

	// Without opts — no query params
	gotType = ""
	gotSession = ""
	_, err = c.GetBalance()
	if err != nil {
		t.Fatalf("GetBalance no opts: %v", err)
	}
	if gotType != "" {
		t.Errorf("expected no type param, got %q", gotType)
	}
}

func TestCreateSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s", r.Method)
		}
		var req SessionCreateRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Country != "DE" {
			t.Errorf("country = %q", req.Country)
		}
		if req.City != "Berlin" {
			t.Errorf("city = %q", req.City)
		}
		if !req.Sticky {
			t.Error("sticky should be true")
		}
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(SessionCreateResponse{
			SessionID:  "sess-123",
			Country:    "DE",
			ByteBudget: 1000000,
			Gateway:    GatewayInfo{Endpoint: "gw:8080", Token: "tok"},
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "pk_test")
	resp, err := c.CreateSession(&SessionCreateRequest{Country: "DE", City: "Berlin", Sticky: true})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if resp.SessionID != "sess-123" {
		t.Errorf("session = %q", resp.SessionID)
	}
	if resp.Gateway.Token != "tok" {
		t.Errorf("token = %q", resp.Gateway.Token)
	}
}

func TestEndSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/v1/sessions/sess-456" {
			t.Errorf("path = %q", r.URL.Path)
		}
		json.NewEncoder(w).Encode(SessionEndResponse{
			SessionID:   "sess-456",
			DurationSec: 120,
			BytesTotal:  5000,
			CostUSD:     0.001,
			BalanceUSD:  9.99,
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "pk_test")
	resp, err := c.EndSession("sess-456")
	if err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	if resp.DurationSec != 120 {
		t.Errorf("duration = %d", resp.DurationSec)
	}
}

func TestAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(402)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Insufficient balance",
			"code":    "insufficient_balance",
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "pk_test")
	_, err := c.GetAccount()
	if err == nil {
		t.Fatal("should error on 402")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("wrong error type: %T", err)
	}
	if apiErr.StatusCode != 402 {
		t.Errorf("status = %d", apiErr.StatusCode)
	}
	if !apiErr.IsInsufficientBalance() {
		t.Error("should be insufficient balance")
	}
	if apiErr.Error() != "Insufficient balance" {
		t.Errorf("message = %q", apiErr.Error())
	}
}

func TestAPIErrorAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()

	c := New(srv.URL, "bad_key")
	_, err := c.GetAccount()
	apiErr := err.(*APIError)
	if !apiErr.IsAuthError() {
		t.Error("should be auth error")
	}
}

func TestPublicEndpointNoAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("public endpoint should not send auth header")
		}
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer srv.Close()

	c := New(srv.URL, "pk_test")
	if err := c.SendLoginCode("user@test.com"); err != nil {
		t.Fatalf("SendLoginCode: %v", err)
	}
}

func TestSendLoginCodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		json.NewEncoder(w).Encode(map[string]string{"message": "Too many requests", "code": "rate_limited"})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	err := c.SendLoginCode("user@test.com")
	if err == nil {
		t.Fatal("should error on 429")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("wrong type: %T", err)
	}
	if apiErr.StatusCode != 429 {
		t.Errorf("status = %d", apiErr.StatusCode)
	}
}

func TestVerifyLoginCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s", r.Method)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("verify should not send auth")
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["email"] != "user@test.com" {
			t.Errorf("email = %q", body["email"])
		}
		if body["otp"] != "123456" {
			t.Errorf("otp = %q", body["otp"])
		}
		if body["source"] != "cli" {
			t.Errorf("source = %q", body["source"])
		}
		if body["key_hash"] != "test_hash" {
			t.Errorf("key_hash = %q", body["key_hash"])
		}
		if body["key_prefix"] != "pk_test1234" {
			t.Errorf("key_prefix = %q", body["key_prefix"])
		}
		// No api_key in response — key was generated client-side
		json.NewEncoder(w).Encode(LoginResponse{
			Email:      "user@test.com",
			BalanceUSD: 5.00,
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	resp, err := c.VerifyLoginCode("user@test.com", "123456", "test_hash", "pk_test1234")
	if err != nil {
		t.Fatalf("VerifyLoginCode: %v", err)
	}
	if resp.APIKey != "" {
		t.Errorf("api_key should be empty for client-generated keys, got %q", resp.APIKey)
	}
	if resp.BalanceUSD != 5.00 {
		t.Errorf("balance = %f", resp.BalanceUSD)
	}
}

func TestVerifyLoginCodeWrongOTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]string{"message": "Invalid or expired code", "code": "auth_invalid"})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	_, err := c.VerifyLoginCode("user@test.com", "000000", "h", "p")
	if err == nil {
		t.Fatal("should error on wrong OTP")
	}
	apiErr := err.(*APIError)
	if !apiErr.IsAuthError() {
		t.Error("should be auth error")
	}
}

func TestGetVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/version" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("version endpoint should not send auth")
		}
		json.NewEncoder(w).Encode(map[string]string{
			"latest":       "0.2.0",
			"min_required": "0.1.0",
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	resp, err := c.GetVersion()
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if resp.Latest != "0.2.0" {
		t.Errorf("latest = %q", resp.Latest)
	}
	if resp.MinRequired != "0.1.0" {
		t.Errorf("min = %q", resp.MinRequired)
	}
}

func TestGetSessionRouteHealth_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sessions/sess-1/route-health" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("probe") != "0" {
			t.Errorf("probe = %q, want 0", r.URL.Query().Get("probe"))
		}
		json.NewEncoder(w).Encode(RouteHealthResponse{Healthy: true})
	}))
	defer srv.Close()

	c := New(srv.URL, "pk_test")
	resp, err := c.GetSessionRouteHealth("sess-1")
	if err != nil {
		t.Fatalf("GetSessionRouteHealth: %v", err)
	}
	if !resp.Healthy {
		t.Error("should be healthy")
	}
}

func TestGetSessionRouteHealth_NonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"message":"internal error"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "pk_test")
	_, err := c.GetSessionRouteHealth("sess-1")
	if err == nil {
		t.Fatal("should error on 500")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("wrong error type: %T", err)
	}
	if apiErr.StatusCode != 500 {
		t.Errorf("status = %d, want 500", apiErr.StatusCode)
	}
}

func TestGetSessionRouteHealth_Depleted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(RouteHealthResponse{Healthy: false, Reason: "depleted"})
	}))
	defer srv.Close()

	c := New(srv.URL, "pk_test")
	resp, err := c.GetSessionRouteHealth("sess-1")
	if err != nil {
		t.Fatalf("GetSessionRouteHealth: %v", err)
	}
	if resp.Healthy {
		t.Error("should not be healthy")
	}
	if resp.Reason != "depleted" {
		t.Errorf("reason = %q, want depleted", resp.Reason)
	}
}

func TestUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		json.NewEncoder(w).Encode(AccountResponse{})
	}))
	defer srv.Close()

	c := New(srv.URL, "pk_test")
	c.GetAccount()
	if gotUA == "" {
		t.Error("should send User-Agent")
	}
}
