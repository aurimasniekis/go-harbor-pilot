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
