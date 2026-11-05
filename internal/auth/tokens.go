package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"strings"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/crypto"
)

// Token kinds (the first segment of a bearer token).
const (
	TokenKindAPI     = "hpa" // control-plane API token for service principals
	TokenKindGateway = "hpk" // gateway credential for client bindings
)

// ErrTokenFormat is returned for malformed bearer tokens.
var ErrTokenFormat = errors.New("malformed token")

var b32 = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// GeneratedToken is a freshly minted bearer token. Plaintext is shown to the
// caller exactly once and never stored.
type GeneratedToken struct {
	Plaintext string
	Prefix    string
	Secret    string
}

// NewToken mints "<kind>_<8-char prefix>_<43-char secret>" (256-bit secret).
func NewToken(kind string) GeneratedToken {
	prefix := b32.EncodeToString(crypto.RandomBytes(5)) // 8 chars
	secret := base64.RawURLEncoding.EncodeToString(crypto.RandomBytes(32))
	return GeneratedToken{Plaintext: kind + "_" + prefix + "_" + secret, Prefix: prefix, Secret: secret}
}

// ParseToken splits a token into kind, prefix and secret.
func ParseToken(tok string) (kind, prefix, secret string, err error) {
	parts := strings.SplitN(tok, "_", 3)
	if len(parts) != 3 || len(parts[1]) != 8 || len(parts[2]) != 43 {
		return "", "", "", ErrTokenFormat
	}
	if parts[0] != TokenKindAPI && parts[0] != TokenKindGateway {
		return "", "", "", ErrTokenFormat
	}
	if _, err := b32.DecodeString(parts[1]); err != nil {
		return "", "", "", ErrTokenFormat
	}
	if _, err := base64.RawURLEncoding.DecodeString(parts[2]); err != nil {
		return "", "", "", ErrTokenFormat
	}
	return parts[0], parts[1], parts[2], nil
}

// Verifier computes the stored verifier for a token secret.
func Verifier(pepper []byte, kind, prefix, secret string) []byte {
	m := hmac.New(sha256.New, pepper)
	m.Write([]byte("harbor-pilot/token/v1|" + kind + "|" + prefix + "|" + secret))
	return m.Sum(nil)
}

// VerifierEqual compares verifiers in constant time.
func VerifierEqual(a, b []byte) bool { return hmac.Equal(a, b) }

// HashOpaque hashes an opaque secret (session token, one-time token, OIDC
// state) for storage and lookup.
func HashOpaque(pepper []byte, purpose, v string) string {
	m := hmac.New(sha256.New, pepper)
	m.Write([]byte("harbor-pilot/" + purpose + "/v1|" + v))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// RandomString returns n random bytes base64url-encoded.
func RandomString(n int) string {
	return base64.RawURLEncoding.EncodeToString(crypto.RandomBytes(n))
}
