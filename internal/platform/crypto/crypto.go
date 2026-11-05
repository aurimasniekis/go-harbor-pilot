// Package crypto implements envelope encryption for data at rest.
//
// A key-encryption key (KEK) is supplied by the operator from a file or the
// environment and never stored in the database. Each protected value gets a
// fresh random data-encryption key (DEK). The value is sealed with the DEK
// (AES-256-GCM) and the DEK is wrapped with the KEK (AES-256-GCM). Both
// operations bind caller-supplied associated data so a ciphertext cannot be
// moved to a different row or purpose.
//
// The keyring may hold several KEKs: one current key used for new seals, and
// older keys that are only used to unwrap until `keys rotate` re-wraps all
// DEKs under the current key.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

// KeySize is the size in bytes of KEKs and DEKs.
const KeySize = 32

// Errors returned by the package. Messages never include key material.
var (
	ErrNoKey       = errors.New("crypto: master key not configured")
	ErrUnknownKEK  = errors.New("crypto: envelope sealed with an unknown master key")
	ErrMalformed   = errors.New("crypto: malformed envelope")
	ErrDecrypt     = errors.New("crypto: authentication failed")
	ErrKeyMaterial = errors.New("crypto: master key must be 32 bytes, base64 or hex encoded")
)

// KEK is a key-encryption key with a stable fingerprint id.
type KEK struct {
	ID  string
	key []byte
}

// String never reveals key material.
func (k KEK) String() string { return "KEK(" + k.ID + ")" }

// ParseKEK decodes a 32-byte key from base64 (std or url, padded or not) or hex.
func ParseKEK(s string) (KEK, error) {
	s = strings.TrimSpace(s)
	var raw []byte
	for _, dec := range []func(string) ([]byte, error){
		base64.StdEncoding.DecodeString, base64.RawStdEncoding.DecodeString,
		base64.URLEncoding.DecodeString, base64.RawURLEncoding.DecodeString,
		hex.DecodeString,
	} {
		if b, err := dec(s); err == nil && len(b) == KeySize {
			raw = b
			break
		}
	}
	if raw == nil {
		return KEK{}, ErrKeyMaterial
	}
	sum := sha256.Sum256(append([]byte("harbor-pilot/kek-id/v1|"), raw...))
	return KEK{ID: "k" + hex.EncodeToString(sum[:6]), key: raw}, nil
}

// LoadKEKFile reads a KEK from a file. The file must not be group/world
// readable on Unix systems.
func LoadKEKFile(path string) (KEK, error) {
	st, err := os.Stat(path)
	if err != nil {
		return KEK{}, fmt.Errorf("master key file: %w", err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return KEK{}, fmt.Errorf("master key file %s must not be accessible by group or others (mode %04o)", path, st.Mode().Perm())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return KEK{}, fmt.Errorf("master key file: %w", err)
	}
	return ParseKEK(string(b))
}

// GenerateKEKString returns a new random KEK encoded as base64.
func GenerateKEKString() string {
	return base64.StdEncoding.EncodeToString(RandomBytes(KeySize))
}

// RandomBytes returns n cryptographically random bytes.
func RandomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failure: " + err.Error())
	}
	return b
}

// Keyring holds the current KEK and any previous KEKs needed to unwrap.
type Keyring struct {
	current KEK
	byID    map[string]KEK
}

// NewKeyring builds a keyring. previous may be empty.
func NewKeyring(current KEK, previous ...KEK) (*Keyring, error) {
	if len(current.key) != KeySize {
		return nil, ErrNoKey
	}
	kr := &Keyring{current: current, byID: map[string]KEK{current.ID: current}}
	for _, p := range previous {
		if len(p.key) == KeySize {
			kr.byID[p.ID] = p
		}
	}
	return kr, nil
}

// CurrentID returns the id of the KEK used for new seals.
func (kr *Keyring) CurrentID() string { return kr.current.ID }

// Has reports whether the keyring can unwrap envelopes sealed with id.
func (kr *Keyring) Has(id string) bool { _, ok := kr.byID[id]; return ok }

// WrapDEK wraps a DEK under the current KEK. aad binds the wrapped key to
// its purpose (for example "trace-bucket|pln_x|2026-11-05").
func (kr *Keyring) WrapDEK(dek []byte, aad string) (kekID string, wrapped []byte, err error) {
	if len(dek) != KeySize {
		return "", nil, ErrMalformed
	}
	ct, err := seal(kr.current.key, dek, []byte("dek|"+aad))
	if err != nil {
		return "", nil, err
	}
	return kr.current.ID, ct, nil
}

// UnwrapDEK reverses WrapDEK.
func (kr *Keyring) UnwrapDEK(kekID string, wrapped []byte, aad string) ([]byte, error) {
	k, ok := kr.byID[kekID]
	if !ok {
		return nil, ErrUnknownKEK
	}
	return open(k.key, wrapped, []byte("dek|"+aad))
}

// Seal encrypts plaintext under a fresh DEK and returns a self-describing
// envelope string: "v1.<kekid>.<b64 wrapped dek>.<b64 ciphertext>".
func (kr *Keyring) Seal(plaintext []byte, aad string) (string, error) {
	dek := RandomBytes(KeySize)
	kekID, wrapped, err := kr.WrapDEK(dek, aad)
	if err != nil {
		return "", err
	}
	ct, err := seal(dek, plaintext, []byte("val|"+aad))
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	return strings.Join([]string{"v1", kekID, enc.EncodeToString(wrapped), enc.EncodeToString(ct)}, "."), nil
}

// Open decrypts an envelope produced by Seal with the same aad.
func (kr *Keyring) Open(envelope, aad string) ([]byte, error) {
	kekID, wrapped, ct, err := parseEnvelope(envelope)
	if err != nil {
		return nil, err
	}
	dek, err := kr.UnwrapDEK(kekID, wrapped, aad)
	if err != nil {
		return nil, err
	}
	return open(dek, ct, []byte("val|"+aad))
}

// Rewrap re-wraps an envelope's DEK under the current KEK without touching
// the ciphertext. It returns the envelope unchanged when already current.
func (kr *Keyring) Rewrap(envelope, aad string) (string, error) {
	kekID, wrapped, ct, err := parseEnvelope(envelope)
	if err != nil {
		return "", err
	}
	if kekID == kr.current.ID {
		return envelope, nil
	}
	dek, err := kr.UnwrapDEK(kekID, wrapped, aad)
	if err != nil {
		return "", err
	}
	newID, newWrapped, err := kr.WrapDEK(dek, aad)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	return strings.Join([]string{"v1", newID, enc.EncodeToString(newWrapped), enc.EncodeToString(ct)}, "."), nil
}

// EnvelopeKEK returns the KEK id an envelope was sealed with.
func EnvelopeKEK(envelope string) (string, error) {
	id, _, _, err := parseEnvelope(envelope)
	return id, err
}

func parseEnvelope(s string) (string, []byte, []byte, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 4 || parts[0] != "v1" || parts[1] == "" {
		return "", nil, nil, ErrMalformed
	}
	enc := base64.RawURLEncoding
	wrapped, err := enc.DecodeString(parts[2])
	if err != nil {
		return "", nil, nil, ErrMalformed
	}
	ct, err := enc.DecodeString(parts[3])
	if err != nil {
		return "", nil, nil, ErrMalformed
	}
	return parts[1], wrapped, ct, nil
}

// SealWithDEK encrypts with a caller-managed DEK (used for trace buckets).
func SealWithDEK(dek, plaintext []byte, aad string) ([]byte, error) {
	return seal(dek, plaintext, []byte("val|"+aad))
}

// OpenWithDEK decrypts data sealed by SealWithDEK.
func OpenWithDEK(dek, ct []byte, aad string) ([]byte, error) {
	return open(dek, ct, []byte("val|"+aad))
}

func seal(key, plaintext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := RandomBytes(gcm.NonceSize())
	return gcm.Seal(nonce, nonce, plaintext, aad), nil
}

func open(key, data, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(data) < gcm.NonceSize()+gcm.Overhead() {
		return nil, ErrMalformed
	}
	out, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return out, nil
}
