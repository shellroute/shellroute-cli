package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"time"
)

// Version is set by the cli package so the User-Agent reflects the actual build.
var Version = "dev"

// --- Client ---

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout:   15 * time.Second,
			Transport: &http.Transport{Proxy: nil}, // bypass HTTP_PROXY — API calls go direct
		},
	}
}

// --- Request types ---

type SessionCreateRequest struct {
	Country string `json:"country"`
	City    string `json:"city,omitempty"`
	Type    string `json:"type"`
	Sticky  bool   `json:"sticky,omitempty"`
}

// --- Response types ---

type AccountResponse struct {
	Email      string  `json:"email"`
	BalanceUSD float64 `json:"balance_usd"`
}

type BalanceResponse struct {
	BalanceUSD float64 `json:"balance_usd"`
	LowBalance bool    `json:"low_balance"`
	Email      string  `json:"email"`
	ByteBudget int64   `json:"byte_budget"`
	Rates      struct {
		ResidentialPerGB float64 `json:"residential_per_gb"`
		DatacenterPerGB  float64 `json:"datacenter_per_gb"`
	} `json:"rates"`
}

type GatewayInfo struct {
	Endpoint string `json:"endpoint"`
	Token    string `json:"token"`
}

type SessionCreateResponse struct {
	SessionID   string            `json:"session_id"`
	Gateway     GatewayInfo       `json:"gateway"`
	ExitIP      string            `json:"exit_ip"`
	Country     string            `json:"country"`
	City        string            `json:"city"`
	Type        string            `json:"type"`
	ByteBudget  int64             `json:"byte_budget"`
	BalanceUSD  float64           `json:"balance_usd"`
	LowBalance  bool              `json:"low_balance"`
	PrevSession *PrevSessionStats `json:"prev_session,omitempty"`
}

type PrevSessionStats struct {
	DurationSec int     `json:"duration_sec"`
	BytesTotal  int64   `json:"bytes_total"`
	CostUSD     float64 `json:"cost_usd"`
}

type SessionEndResponse struct {
	SessionID   string  `json:"session_id"`
	DurationSec int     `json:"duration_seconds"`
	BytesTotal  int64   `json:"bytes_total"`
	CostUSD     float64 `json:"cost_usd"`
	BalanceUSD  float64 `json:"balance_usd"`
}

type SessionInfo struct {
	ID        string  `json:"id"`
	Country   string  `json:"country"`
	Type      string  `json:"type"`
	Status    string  `json:"status"`
	BytesUp   int64   `json:"bytes_up"`
	BytesDown int64   `json:"bytes_down"`
	CostUSD   float64 `json:"cost_usd"`
	StartedAt string  `json:"started_at"`
}

type City struct {
	Name             string `json:"name"`
	ResidentialCount int    `json:"residential_count"`
	DatacenterCount  int    `json:"datacenter_count"`
}

type Location struct {
	Country          string `json:"country"`
	CountryName      string `json:"country_name"`
	ResidentialCount int    `json:"residential_count"`
	DatacenterCount  int    `json:"datacenter_count"`
	Cities           []City `json:"cities"`
}

type LocationsResponse struct {
	Locations []Location `json:"locations"`
}

type LoginResponse struct {
	APIKey     string  `json:"api_key,omitempty"` // empty — key is generated locally
	Email      string  `json:"email"`
	BalanceUSD float64 `json:"balance_usd"`
}

type VersionResponse struct {
	Latest       string `json:"latest"`
	MinRequired  string `json:"min_required"`
	ChangelogURL string `json:"changelog_url"`
}

// --- API Error ---

type APIError struct {
	StatusCode int
	Message    string `json:"message"`
	Code       string `json:"code"`
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("API error: HTTP %d", e.StatusCode)
}

// UserMessage returns a message safe for user-facing display.
// Never empty — falls back to a generic message.
func (e *APIError) UserMessage() string {
	if e.Message != "" {
		return e.Message
	}
	return "Something went wrong. Try again."
}

func (e *APIError) IsAuthError() bool {
	return e.StatusCode == 401 || e.StatusCode == 403
}

func (e *APIError) IsInsufficientBalance() bool {
	return e.Code == "insufficient_balance"
}

func (e *APIError) IsNoNodes() bool {
	return e.Code == "no_nodes_available"
}

// --- Methods ---

func (c *Client) GetAccount() (*AccountResponse, error) {
	var resp AccountResponse
	return &resp, c.request("GET", "/v1/account/info", true, nil, &resp)
}

// BalanceOpts holds optional parameters for GetBalance.
type BalanceOpts struct {
	IPType    string // for byte_budget calculation
	SessionID string // CLI heartbeat — updates last_cli_ping
}

func (c *Client) GetBalance(opts ...BalanceOpts) (*BalanceResponse, error) {
	var resp BalanceResponse
	path := "/v1/account/balance"
	sep := "?"
	if len(opts) > 0 {
		if opts[0].IPType != "" {
			path += sep + "type=" + opts[0].IPType
			sep = "&"
		}
		if opts[0].SessionID != "" {
			path += sep + "session_id=" + opts[0].SessionID
		}
	}
	return &resp, c.request("GET", path, true, nil, &resp)
}

func (c *Client) CreateSession(req *SessionCreateRequest) (*SessionCreateResponse, error) {
	var resp SessionCreateResponse
	return &resp, c.request("POST", "/v1/sessions/create", true, req, &resp)
}

// RotateOpts are optional overrides for rotate. Zero values = keep old session's values.
type RotateOpts struct {
	IPType string
	Sticky string // "on", "off", or "" (keep)
}

func (c *Client) RotateSession(sessionID string, opts *RotateOpts) (*SessionCreateResponse, error) {
	path := fmt.Sprintf("/v1/sessions/%s/rotate", sessionID)
	if opts != nil {
		q := neturl.Values{}
		if opts.IPType != "" {
			q.Set("iptype", opts.IPType)
		}
		if opts.Sticky != "" {
			q.Set("sticky", opts.Sticky)
		}
		if encoded := q.Encode(); encoded != "" {
			path += "?" + encoded
		}
	}
	var resp SessionCreateResponse
	return &resp, c.request("POST", path, true, nil, &resp)
}

type RouteHealthResponse struct {
	Healthy bool   `json:"healthy"`
	Reason  string `json:"reason"`
}

func (c *Client) GetSessionRouteHealth(sessionID string) (*RouteHealthResponse, error) {
	url := fmt.Sprintf("%s/v1/sessions/%s/route-health?probe=0", c.baseURL, sessionID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("User-Agent", "shellroute-cli/"+Version)

	client := &http.Client{
		Timeout:   3 * time.Second,
		Transport: &http.Transport{Proxy: nil},
	}
	httpResp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode >= 400 {
		apiErr := &APIError{StatusCode: httpResp.StatusCode}
		json.NewDecoder(httpResp.Body).Decode(apiErr)
		return nil, apiErr
	}

	var resp RouteHealthResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) EndSession(sessionID string) (*SessionEndResponse, error) {
	var resp SessionEndResponse
	return &resp, c.request("DELETE", fmt.Sprintf("/v1/sessions/%s", sessionID), true, nil, &resp)
}

func (c *Client) GetSessions() ([]SessionInfo, error) {
	var resp struct {
		Sessions []SessionInfo `json:"sessions"`
	}
	return resp.Sessions, c.request("GET", "/v1/sessions", true, nil, &resp)
}

// ReportSessionConnect reports session connect outcome for diagnostics.
// Sends synchronously but with a short timeout — the process may exit soon after.
func (c *Client) ReportSessionConnect(sessionID, country, ipType string, success bool, connectMs int, errorMsg string) {
	payload := map[string]any{
		"session_id": sessionID,
		"country":    country,
		"ip_type":    ipType,
		"success":    success,
		"connect_ms": connectMs,
	}
	if errorMsg != "" {
		payload["error"] = errorMsg
	}
	// Synchronous with short timeout — process may exit right after this call.
	// Use a dedicated client so we don't block the main client's timeout.
	reportClient := &http.Client{Timeout: 2 * time.Second}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", c.baseURL+"/v1/sessions/connect-report", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "shellroute-cli/"+Version)
	resp, err := reportClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

// RouteHealthResult is the response from the route-health endpoint.
type RouteHealthResult struct {
	Healthy         bool   `json:"healthy"`
	ExitIP          string `json:"exit_ip,omitempty"`
	TunnelID        int    `json:"tunnel_id,omitempty"`
	RouteGeneration uint64 `json:"route_generation,omitempty"`
	LastCheckAgeMs  int64  `json:"last_check_age_ms,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

// RouteHealth checks the session's tunnel health via the gateway.
// Uses a separate transport with Proxy=nil to avoid routing through the dead proxy.
func (c *Client) RouteHealth(sessionID string) (*RouteHealthResult, error) {
	url := fmt.Sprintf("%s/v1/sessions/%s/route-health", c.baseURL, sessionID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("User-Agent", "shellroute-cli/"+Version)

	// Direct transport — must not route through the proxy (it may be dead)
	directClient := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{Proxy: nil},
	}
	resp, err := directClient.Do(req)
	if err != nil {
		return &RouteHealthResult{Healthy: false, Reason: "unreachable"}, err
	}
	defer resp.Body.Close()

	var result RouteHealthResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return &RouteHealthResult{Healthy: false, Reason: "decode_error"}, err
	}
	return &result, nil
}

func (c *Client) GetLocations() (*LocationsResponse, error) {
	var resp LocationsResponse
	return &resp, c.request("GET", "/v1/locations", true, nil, &resp)
}

func (c *Client) ValidateKey() (*AccountResponse, error) {
	return c.GetAccount()
}

func (c *Client) SendLoginCode(email string) error {
	return c.request("POST", "/v1/auth/email", false, map[string]string{"email": email, "source": "cli"}, nil)
}

func (c *Client) VerifyLoginCode(email, otp, keyHash, keyPrefix string) (*LoginResponse, error) {
	var resp LoginResponse
	return &resp, c.request("POST", "/v1/auth/email/verify", false, map[string]interface{}{
		"email": email, "otp": otp, "source": "cli",
		"key_hash": keyHash, "key_prefix": keyPrefix,
	}, &resp)
}

func (c *Client) GetVersion() (*VersionResponse, error) {
	var resp VersionResponse
	return &resp, c.request("GET", "/v1/version", false, nil, &resp)
}

// --- HTTP helper (single method for all requests) ---

func (c *Client) request(method, path string, authenticated bool, body, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "shellroute-cli/"+Version)
	if authenticated {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		apiErr := &APIError{StatusCode: resp.StatusCode}
		_ = json.Unmarshal(respBody, apiErr)
		return apiErr
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
	}
	return nil
}
