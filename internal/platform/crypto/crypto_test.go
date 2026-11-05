package crypto

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func mustKEK(t *testing.T) KEK {
	t.Helper()
	k, err := ParseKEK(GenerateKEKString())
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestSealOpenRoundTrip(t *testing.T) {
	kr, err := NewKeyring(mustKEK(t))
	if err != nil {
		t.Fatal(err)
	}
	env, err := kr.Seal([]byte("sk-live-secret"), "provider-account|acc_1|api_key")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(env, "sk-live") {
		t.Fatal("plaintext visible in envelope")
	}
	got, err := kr.Open(env, "provider-account|acc_1|api_key")
	if err != nil || string(got) != "sk-live-secret" {
		t.Fatalf("open: %q %v", got, err)
	}
}

func TestOpenRejects(t *testing.T) {
	kr, _ := NewKeyring(mustKEK(t))
	other, _ := NewKeyring(mustKEK(t))
	env, _ := kr.Seal([]byte("v"), "aad-a")
	tests := []struct {
		name string
		kr   *Keyring
		env  string
		aad  string
		want error
	}{
		{name: "wrong aad", kr: kr, env: env, aad: "aad-b", want: ErrDecrypt},
		{name: "unknown kek", kr: other, env: env, aad: "aad-a", want: ErrUnknownKEK},
		{name: "malformed", kr: kr, env: "v1.x.y", aad: "aad-a", want: ErrMalformed},
		{name: "tampered", kr: kr, env: env[:len(env)-2] + "AA", aad: "aad-a", want: ErrDecrypt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.kr.Open(tt.env, tt.aad); !errors.Is(err, tt.want) {
				t.Fatalf("got %v want %v", err, tt.want)
			}
		})
	}
}

func TestRotationRewrap(t *testing.T) {
	oldK, newK := mustKEK(t), mustKEK(t)
	oldRing, _ := NewKeyring(oldK)
	env, _ := oldRing.Seal([]byte("payload"), "a")

	rotating, _ := NewKeyring(newK, oldK)
	rewrapped, err := rotating.Rewrap(env, "a")
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := EnvelopeKEK(rewrapped); id != newK.ID {
		t.Fatalf("rewrapped under %s, want %s", id, newK.ID)
	}
	newOnly, _ := NewKeyring(newK)
	got, err := newOnly.Open(rewrapped, "a")
	if err != nil || !bytes.Equal(got, []byte("payload")) {
		t.Fatalf("open after rotation: %q %v", got, err)
	}
	if _, err := newOnly.Open(env, "a"); !errors.Is(err, ErrUnknownKEK) {
		t.Fatalf("old envelope should need old key, got %v", err)
	}
}

func TestParseKEK(t *testing.T) {
	if _, err := ParseKEK("too-short"); !errors.Is(err, ErrKeyMaterial) {
		t.Fatalf("got %v", err)
	}
	k := mustKEK(t)
	if strings.Contains(k.String(), string(k.key)) || !strings.HasPrefix(k.ID, "k") {
		t.Fatalf("unexpected KEK string %q", k.String())
	}
}
