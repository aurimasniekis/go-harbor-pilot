package acceptance_test

import (
	"context"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/traces"
)

// Trace export needs a recent sign-in; confirming the password restores it.
func TestTraceExportNeedsRecentSignIn(t *testing.T) {
	clk := clock.NewFake(time.Now().UTC())
	r := newPrivacyRig(t, nil, map[string]any{"mode": "masked_full", "retention": "3d", "allow_export": true}, apptest.WithClock(clk))
	resp := r.e.Gateway("POST", "/v1/messages", r.tok, map[string]any{"model": "x", "max_tokens": 16,
		"messages": []any{map[string]any{"role": "user", "content": "release notes"}}})
	_ = apptest.ReadAll(resp)
	r.e.Flush()
	r.e.App.TraceSvc.Flush(context.Background())
	items, _ := r.e.App.TraceSvc.List(context.Background(), traces.Filter{})
	if len(items) != 1 {
		t.Fatalf("traces %d", len(items))
	}
	export := "/api/v1/traces/" + items[0].ID + "/export"
	if code := r.e.Admin.Do("GET", export, nil, nil); code != 200 {
		t.Fatalf("export right after sign-in: %d", code)
	}
	clk.Advance(11 * time.Minute)
	var denied struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if code := r.e.Admin.Do("GET", export, nil, &denied); code != 403 || denied.Error.Code != "reauth_required" {
		t.Fatalf("export after the step-up window: %d %+v", code, denied)
	}
	if code := r.e.Admin.Do("POST", "/api/v1/session/reauth", map[string]string{"password": "wrong-password!"}, nil); code != 401 {
		t.Fatalf("reauth with a wrong password: %d", code)
	}
	r.e.Admin.MustDo(200, "POST", "/api/v1/session/reauth", map[string]string{"password": "correct-horse-battery"}, nil)
	if code := r.e.Admin.Do("GET", export, nil, nil); code != 200 {
		t.Fatalf("export after re-authenticating: %d", code)
	}
}

// A user who signs in only through SSO cannot add a password from an old
// session: the first password needs a recent sign-in.
func TestFirstPasswordNeedsRecentSignIn(t *testing.T) {
	clk := clock.NewFake(time.Now().UTC())
	e := apptest.New(t, apptest.WithClock(clk))
	var created struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	e.Admin.MustDo(201, "POST", "/api/v1/users", map[string]any{"email": "sso@example.test", "role": "viewer", "invite": false}, &created)
	tok, _, err := e.App.Auth.CreateSession(context.Background(), e.App.DB.W, created.User.ID, "oidc", "", false)
	if err != nil {
		t.Fatal(err)
	}
	c := e.SessionClient(tok)
	clk.Advance(11 * time.Minute)
	var denied struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if code := c.Do("POST", "/api/v1/session/password", map[string]string{"current_password": "", "new_password": "a-brand-new-password"}, &denied); code != 403 || denied.Error.Code != "reauth_required" {
		t.Fatalf("first password from an old session: %d %+v", code, denied)
	}
	fresh, _, err := e.App.Auth.CreateSession(context.Background(), e.App.DB.W, created.User.ID, "oidc", "", false)
	if err != nil {
		t.Fatal(err)
	}
	e.SessionClient(fresh).MustDo(204, "POST", "/api/v1/session/password", map[string]string{"current_password": "", "new_password": "a-brand-new-password"}, nil)
}
