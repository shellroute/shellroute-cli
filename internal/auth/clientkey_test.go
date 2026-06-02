package auth

import (
	"strings"
	"testing"
)

func TestGenerateRawKey(t *testing.T) {
	key, err := GenerateRawKey()
	if err != nil {
		t.Fatalf("GenerateRawKey: %v", err)
	}
	if !strings.HasPrefix(key, "pk_") {
		t.Errorf("key should start with pk_, got %q", key[:6])
	}
	// pk_ (3) + 64 hex chars = 67
	if len(key) != 67 {
		t.Errorf("key length = %d, want 67", len(key))
	}

	// Two keys must differ
	key2, _ := GenerateRawKey()
	if key == key2 {
		t.Error("two generated keys should not be identical")
	}
}

func TestHashClientKey(t *testing.T) {
	key := "pk_0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	hash := HashClientKey(key)

	// Deterministic: same input → same output
	if HashClientKey(key) != hash {
		t.Error("HashClientKey should be deterministic")
	}

	// Different input → different output
	other := HashClientKey("pk_ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	if hash == other {
		t.Error("different keys should produce different hashes")
	}

	// Hash is 64 hex chars (SHA-256)
	if len(hash) != 64 {
		t.Errorf("hash length = %d, want 64", len(hash))
	}
}

func TestHashClientKeyUsesClientKeyHMACSecret(t *testing.T) {
	key := "pk_test"
	// hashToken with the constant secret should match HashClientKey
	expected := hashToken(key, ClientKeyHMACSecret)
	got := HashClientKey(key)
	if got != expected {
		t.Errorf("HashClientKey should use ClientKeyHMACSecret")
	}
}

func TestKeyPrefix(t *testing.T) {
	key := "pk_a1b2c3d4e5f6"
	prefix := KeyPrefix(key)
	if prefix != "pk_a1b2c3d4" {
		t.Errorf("prefix = %q, want pk_a1b2c3d4", prefix)
	}

	// Short key returns as-is
	short := KeyPrefix("pk_ab")
	if short != "pk_ab" {
		t.Errorf("short prefix = %q, want pk_ab", short)
	}
}
