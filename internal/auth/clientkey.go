package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

// ClientKeyHMACSecret is the fixed public salt for client-generated API keys.
// Not a secret — it's a domain separator. Security comes from the 256-bit random key.
const ClientKeyHMACSecret = "shellroute-api-key-v1"

// hashToken produces a deterministic HMAC-SHA256 hash of a token.
func hashToken(token, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}

// HashClientKey produces an HMAC hash of a client-generated API key.
// Used by the CLI to hash locally-generated keys before sending to the server.
func HashClientKey(rawKey string) string {
	return hashToken(rawKey, ClientKeyHMACSecret)
}

// KeyPrefix returns the first 11 characters of a key (e.g. "pk_a1b2c3d4").
func KeyPrefix(rawKey string) string {
	if len(rawKey) < 11 {
		return rawKey
	}
	return rawKey[:11]
}

// GenerateRawKey creates a random API key string: "pk_" + 64 hex chars.
func GenerateRawKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "pk_" + hex.EncodeToString(b), nil
}
