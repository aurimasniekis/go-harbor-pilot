package acceptance_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
)

// Quota monitors reach private or loopback endpoints only when they opt in
// with allow_private_network; otherwise the credential never leaves.
func TestQuotaMonitorPrivateNetworkOptIn(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"12.50","granted_balance":"0","topped_up_balance":"12.50"}]}`)
	}))
	defer srv.Close()
	e := apptest.New(t)
	type board struct {
		Monitors []struct {
			Name    string `json:"name"`
			Health  string `json:"health"`
			Buckets []struct {
				Key string `json:"key"`
			} `json:"buckets"`
		} `json:"monitors"`
	}
	tests := []struct {
		name      string
		allow     bool
		wantHits  bool
		wantKinds bool
	}{
		{"loopback without opt-in", false, false, false},
		{"loopback with opt-in", true, true, true},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mon struct {
				ID string `json:"id"`
			}
			name := fmt.Sprintf("balance-%d", i)
			e.Admin.MustDo(201, "POST", "/api/v1/quota-monitors", map[string]any{"name": name, "spec": map[string]any{"adapter": "deepseek",
				"enabled": true, "allow_private_network": tt.allow, "options": map[string]any{"base_url": srv.URL}},
				"secrets": map[string]string{"api_key": "sk-test-quota"}}, &mon)
			before := hits.Load()
			e.Admin.MustDo(200, "POST", "/api/v1/quota/monitors/"+mon.ID+"/refresh", nil, nil)
			var b board
			e.Eventually(func() bool {
				b = board{}
				e.Admin.MustDo(200, "GET", "/api/v1/quota/board", nil, &b)
				for _, m := range b.Monitors {
					if m.Name == name {
						return m.Health != "pending"
					}
				}
				return false
			}, "the monitor was polled")
			if got := hits.Load() > before; got != tt.wantHits {
				t.Fatalf("upstream reached=%v, want %v", got, tt.wantHits)
			}
			for _, m := range b.Monitors {
				if m.Name == name && (len(m.Buckets) > 0) != tt.wantKinds {
					t.Fatalf("monitor %+v", m)
				}
			}
		})
	}
}

// A stored secret does not silently follow a destination change: channels
// and destinations must re-enter it, and a provider's stored account
// credentials are cleared when its base URL moves to another origin.
func TestStoredSecretsDoNotFollowDestinationChanges(t *testing.T) {
	e := apptest.New(t)
	var ch struct {
		ID string `json:"id"`
	}
	spec := func(url string) map[string]any { return map[string]any{"type": "webhook", "enabled": false, "webhook_url": url} }
	e.Admin.MustDo(201, "POST", "/api/v1/notification-channels", map[string]any{"name": "hook", "spec": spec("https://hooks.example.test/a"),
		"secrets": map[string]string{"secret": "whsec-test"}}, &ch)
	tests := []struct {
		name    string
		url     string
		secrets map[string]string
		code    int
	}{
		{"same origin, other path", "https://hooks.example.test/b", nil, 200},
		{"new origin without the secret", "https://attacker.example.test/x", nil, 422},
		{"new origin with the secret re-entered", "https://attacker.example.test/x", map[string]string{"secret": "whsec-new"}, 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := map[string]any{"name": "hook", "spec": spec(tt.url)}
			if tt.secrets != nil {
				body["secrets"] = tt.secrets
			}
			if code := e.Admin.Do("PUT", "/api/v1/notification-channels/"+ch.ID, body, nil); code != tt.code {
				t.Fatalf("status %d, want %d", code, tt.code)
			}
		})
	}

	prov := e.Provider("p", "openai_compatible", "https://api.example.test/v1", apptest.Model{ID: "m"})
	acc := e.Account("a", prov, apptest.AccountOpts{Secret: "sk-test-provider"})
	stored := func() int64 {
		return e.QueryInt(`SELECT count(*) FROM resource_secrets WHERE resource_id = ? AND field = 'credential'`, acc)
	}
	if stored() != 1 {
		t.Fatal("account credential not stored")
	}
	var p struct {
		Spec map[string]any `json:"spec"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/providers/"+prov, nil, &p)
	p.Spec["base_url"] = "https://api.example.test/v2" // same origin
	e.Admin.MustDo(200, "PUT", "/api/v1/providers/"+prov, map[string]any{"name": "p", "spec": p.Spec}, nil)
	if stored() != 1 {
		t.Fatal("a same-origin change cleared the credential")
	}
	p.Spec["base_url"] = "https://collector.example.test/v1"
	e.Admin.MustDo(200, "PUT", "/api/v1/providers/"+prov, map[string]any{"name": "p", "spec": p.Spec}, nil)
	if stored() != 0 {
		t.Fatal("the account credential would be sent to the provider's new origin")
	}
	p.Spec["base_url"], p.Spec["allow_private_network"] = "http://api.example.test/v1", false
	if code := e.Admin.Do("PUT", "/api/v1/providers/"+prov, map[string]any{"name": "p", "spec": p.Spec}, nil); code != 422 {
		t.Fatalf("plain http to a public host accepted: %d", code)
	}
}
