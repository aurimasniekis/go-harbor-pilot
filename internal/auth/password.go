package auth

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/crypto"
)

// Argon2Params are versioned password hashing parameters.
type Argon2Params struct {
	Memory  uint32 // KiB
	Time    uint32
	Threads uint8
	KeyLen  uint32
	SaltLen uint32
}

// CurrentParams are used for new hashes. Hashes with weaker parameters are
// upgraded transparently on the next successful login.
var CurrentParams = Argon2Params{Memory: 64 * 1024, Time: 3, Threads: 2, KeyLen: 32, SaltLen: 16}

// Password policy.
const (
	PasswordMinBytes = 12
	PasswordMaxBytes = 1024
)

// Password errors (safe to show to users).
var (
	ErrPasswordTooShort = fmt.Errorf("password must be at least %d bytes", PasswordMinBytes)
	ErrPasswordTooLong  = fmt.Errorf("password must be at most %d bytes", PasswordMaxBytes)
	ErrPasswordInvalid  = errors.New("password contains invalid characters")
	errHashFormat       = errors.New("unrecognized password hash format")
)

// hashSem bounds concurrent argon2 computations (64 MiB each).
var hashSem = make(chan struct{}, 4)

// NormalizePassword applies NFKC and enforces length bounds. Over-long input
// is rejected, never truncated.
func NormalizePassword(pw string) (string, error) {
	if strings.ContainsRune(pw, 0) {
		return "", ErrPasswordInvalid
	}
	n := norm.NFKC.String(pw)
	switch {
	case len(n) < PasswordMinBytes:
		return "", ErrPasswordTooShort
	case len(n) > PasswordMaxBytes:
		return "", ErrPasswordTooLong
	}
	return n, nil
}

// HashPassword returns a PHC-format argon2id hash.
func HashPassword(pw string) (string, error) {
	n, err := NormalizePassword(pw)
	if err != nil {
		return "", err
	}
	return hashWith(n, CurrentParams, crypto.RandomBytes(int(CurrentParams.SaltLen))), nil
}

func hashWith(pw string, p Argon2Params, salt []byte) string {
	hashSem <- struct{}{}
	key := argon2.IDKey([]byte(pw), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	<-hashSem
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads, enc.EncodeToString(salt), enc.EncodeToString(key))
}

func parseHash(h string) (Argon2Params, []byte, []byte, error) {
	parts := strings.Split(h, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return Argon2Params{}, nil, nil, errHashFormat
	}
	var p Argon2Params
	var threads uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &threads); err != nil {
		return p, nil, nil, errHashFormat
	}
	if p.Memory < 8*1024 || p.Memory > 1024*1024 || p.Time < 1 || p.Time > 16 || threads < 1 || threads > 16 {
		return p, nil, nil, errHashFormat
	}
	p.Threads = uint8(threads)
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return p, nil, nil, errHashFormat
	}
	key, err := enc.DecodeString(parts[5])
	if err != nil || len(key) < 16 {
		return p, nil, nil, errHashFormat
	}
	p.KeyLen = uint32(len(key))
	p.SaltLen = uint32(len(salt))
	return p, salt, key, nil
}

// VerifyPassword checks pw against hash in constant time. needsRehash is true
// when the stored parameters are weaker than CurrentParams.
func VerifyPassword(hash, pw string) (ok, needsRehash bool) {
	p, salt, key, err := parseHash(hash)
	if err != nil {
		return false, false
	}
	n := norm.NFKC.String(pw)
	if len(n) > PasswordMaxBytes || strings.ContainsRune(n, 0) {
		return false, false
	}
	hashSem <- struct{}{}
	got := argon2.IDKey([]byte(n), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	<-hashSem
	if subtle.ConstantTimeCompare(got, key) != 1 {
		return false, false
	}
	return true, p.Memory < CurrentParams.Memory || p.Time < CurrentParams.Time || p.KeyLen < CurrentParams.KeyLen
}

var (
	dummyOnce sync.Once
	dummy     string
)

// VerifyDummy spends the same work as a real verification so response time
// does not reveal whether an account exists.
func VerifyDummy(pw string) {
	dummyOnce.Do(func() { dummy = hashWith("dummy-password-for-timing", CurrentParams, make([]byte, 16)) })
	_, _ = VerifyPassword(dummy, pw)
}
