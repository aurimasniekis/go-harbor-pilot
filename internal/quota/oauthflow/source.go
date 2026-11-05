package oauthflow

import (
	"context"
	"errors"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

// Token source modes.
const (
	// ModeFile reads a mounted CLI credential file on every poll and never
	// refreshes or modifies it (refreshing would rotate the refresh token and
	// sign the CLI out).
	ModeFile = "file"
	// ModeEnv uses an access token from a secret reference as-is.
	ModeEnv = "env"
	// ModeLogin uses a token obtained by in-app login, stored in the
	// encrypted token store and refreshed automatically.
	ModeLogin = "login"
)

// Source resolves an access token for an account.
type Source struct {
	Mode string
	// File is the credential file path (ModeFile).
	File string
	// ParseFile extracts the token from file contents (ModeFile).
	ParseFile func([]byte) (providers.OAuthToken, error)
	// Client is used for refresh (ModeLogin).
	Client Client
	// SubjectFromToken optionally derives Subject after refresh.
	SubjectFromToken func(TokenResponse) string
}

// ValidMode reports whether m is a known mode.
func ValidMode(m string) bool { return m == ModeFile || m == ModeEnv || m == ModeLogin }

// Resolve returns a usable access token or a typed error.
func (f *Flow) Resolve(ctx context.Context, cfg providers.AccountConfig, src Source, deps providers.Deps, now time.Time) (providers.OAuthToken, error) {
	switch src.Mode {
	case ModeEnv:
		s, ok := cfg.Credentials.Get("access_token")
		if !ok {
			s, ok = cfg.Credentials.Get("api_key")
		}
		if !ok {
			return providers.OAuthToken{}, providers.NewError(domain.ErrConfiguration, "credential access_token is required for token_source env")
		}
		return providers.OAuthToken{AccessToken: s}, nil
	case ModeFile:
		if deps.Files == nil {
			return providers.OAuthToken{}, providers.NewError(domain.ErrConfiguration, "file reader unavailable")
		}
		b, err := deps.Files.Read(src.File)
		if err != nil {
			if errors.Is(err, credentials.ErrOutsideRoots) {
				return providers.OAuthToken{}, providers.NewError(domain.ErrConfiguration, "credentials_file is outside the configured secret roots")
			}
			if errors.Is(err, credentials.ErrNotFound) {
				return providers.OAuthToken{}, providers.NewError(domain.ErrConfiguration, "credentials_file not found")
			}
			return providers.OAuthToken{}, providers.NewError(domain.ErrConfiguration, "credentials_file cannot be read")
		}
		t, err := src.ParseFile(b)
		if err != nil {
			return providers.OAuthToken{}, providers.NewError(domain.ErrConfiguration, "credentials_file has an unexpected format")
		}
		if t.AccessToken.IsZero() {
			return providers.OAuthToken{}, providers.NewError(domain.ErrAuthentication, "credentials_file contains no access token")
		}
		if t.Expired(now, 0) {
			return providers.OAuthToken{}, providers.NewError(domain.ErrAuthentication,
				"access token in the mounted credentials file has expired; it is refreshed only when the CLI that owns it runs (Harbor Pilot never modifies mounted files)")
		}
		return t, nil
	case ModeLogin:
		if deps.Tokens == nil {
			return providers.OAuthToken{}, providers.NewError(domain.ErrConfiguration, providers.ErrTokenStoreDisabled.Error())
		}
		store, err := deps.Tokens.Store()
		if err != nil {
			return providers.OAuthToken{}, providers.NewError(domain.ErrConfiguration, err.Error())
		}
		id := cfg.Account.ID
		t, err := store.Get(id)
		if errors.Is(err, providers.ErrNoToken) {
			return providers.OAuthToken{}, providers.NewError(domain.ErrAuthentication, "not signed in; complete the in-app login for this account")
		}
		if err != nil {
			return providers.OAuthToken{}, providers.NewError(domain.ErrInternal, "token store unavailable")
		}
		if !t.Expired(now, 5*time.Minute) {
			return t, nil
		}
		unlock := deps.Tokens.Lock()
		defer unlock()
		// Re-read: another poll may have refreshed while we waited.
		if t2, err := store.Get(id); err == nil && !t2.Expired(now, 5*time.Minute) {
			return t2, nil
		}
		if t.RefreshToken.IsZero() {
			return providers.OAuthToken{}, providers.NewError(domain.ErrAuthentication, "token expired and no refresh token is stored; sign in again")
		}
		tr, err := f.Refresh(ctx, src.Client, t.RefreshToken.Reveal())
		if err != nil {
			return providers.OAuthToken{}, err
		}
		nt := ToToken(tr, t, now)
		if src.SubjectFromToken != nil {
			if s := src.SubjectFromToken(tr); s != "" {
				nt.Subject = s
			}
		}
		if err := store.Put(id, nt); err != nil {
			return providers.OAuthToken{}, providers.NewError(domain.ErrInternal, "refreshed token could not be persisted")
		}
		return nt, nil
	}
	return providers.OAuthToken{}, providers.NewError(domain.ErrConfiguration, "token_source must be file, env or login")
}
