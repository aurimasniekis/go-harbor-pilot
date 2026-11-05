package acceptance_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
)

// "Test connection" proves an account's credential with the provider's
// model list, stores the latest result and never echoes the credential.
func TestAccountConnectionCheck(t *testing.T) {
	const good, bad = "sk-good-check-key-0001", "sk-bad-check-key-0002"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("x-api-key") != good {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key ` + r.Header.Get("x-api-key") + `"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	t.Cleanup(upstream.Close)
	e := apptest.New(t)
	prov := e.Provider("anthropic", "anthropic", upstream.URL, apptest.Model{ID: "m"})
	down := e.Provider("down", "anthropic", "http://127.0.0.1:1", apptest.Model{ID: "m"})
	accOK := e.Account("ok", prov, apptest.AccountOpts{Secret: good})
	accBad := e.Account("bad", prov, apptest.AccountOpts{Secret: bad})
	accDown := e.Account("down", down, apptest.AccountOpts{Secret: good})
	var none struct {
		ID string `json:"id"`
	}
	e.Admin.MustDo(201, "POST", "/api/v1/provider-accounts", map[string]any{"name": "none", "spec": map[string]any{"provider_id": prov,
		"billing": "metered_api", "enabled": true}}, &none)

	tests := []struct {
		name, id, status string
	}{
		{"accepted credential", accOK, "ok"},
		{"rejected credential", accBad, "rejected"},
		{"unreachable provider", accDown, "unreachable"},
		{"no credential", none.ID, "no_credential"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var raw json.RawMessage
			e.Admin.MustDo(200, "POST", "/api/v1/provider-accounts/"+tt.id+"/test", nil, &raw)
			var got struct {
				Status, Message string
			}
			_ = json.Unmarshal(raw, &got)
			if got.Status != tt.status || got.Message == "" {
				t.Fatalf("got %s, want status %s", raw, tt.status)
			}
			if strings.Contains(string(raw), good) || strings.Contains(string(raw), bad) {
				t.Fatalf("the result echoes a credential: %s", raw)
			}
		})
	}
	var list struct {
		Items []struct {
			AccountID string `json:"account_id"`
			Status    string `json:"status"`
		} `json:"items"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/account-checks", nil, &list)
	if len(list.Items) != 4 {
		t.Fatalf("stored checks %+v", list.Items)
	}
	e.Admin.MustDo(204, "DELETE", "/api/v1/provider-accounts/"+accBad, nil, nil)
	e.Admin.MustDo(200, "GET", "/api/v1/account-checks", nil, &list)
	if len(list.Items) != 3 {
		t.Fatalf("a deleted account's check is still listed: %+v", list.Items)
	}
}
