package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestPasswordHashVerify(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("unexpected hash format %q", h)
	}
	if ok, rehash := VerifyPassword(h, "correct horse battery"); !ok || rehash {
		t.Fatalf("verify ok=%v rehash=%v", ok, rehash)
	}
	if ok, _ := VerifyPassword(h, "correct horse batterY"); ok {
		t.Fatal("wrong password accepted")
	}
	// NFKC: full-width characters normalize to ASCII.
	h2, _ := HashPassword("ｐａｓｓｗｏｒｄ１２３４")
	if ok, _ := VerifyPassword(h2, "password1234"); !ok {
		t.Fatal("NFKC-equivalent password rejected")
	}
}

func TestPasswordPolicy(t *testing.T) {
	tests := []struct {
		name string
		pw   string
		want error
	}{
		{name: "short", pw: "short", want: ErrPasswordTooShort},
		{name: "long", pw: strings.Repeat("a", PasswordMaxBytes+1), want: ErrPasswordTooLong},
		{name: "nul", pw: "abcdefghijkl\x00", want: ErrPasswordInvalid},
		{name: "ok", pw: "twelve-bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := HashPassword(tt.pw)
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v want %v", err, tt.want)
			}
		})
	}
}

func TestRehashDetection(t *testing.T) {
	weak := hashWith("weak-params-password", Argon2Params{Memory: 16 * 1024, Time: 1, Threads: 1, KeyLen: 32}, make([]byte, 16))
	ok, rehash := VerifyPassword(weak, "weak-params-password")
	if !ok || !rehash {
		t.Fatalf("ok=%v rehash=%v", ok, rehash)
	}
	if ok, _ := VerifyPassword("$argon2id$v=19$m=999999999,t=3,p=2$AAAA$AAAA", "x"); ok {
		t.Fatal("absurd params accepted")
	}
}
