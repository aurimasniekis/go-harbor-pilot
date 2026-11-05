package acceptance_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
)

// A relay that gives up keeps the receiver's last HTTP status and error, so
// the delivery list explains why it failed.
func TestRelayGivingUpKeepsTheLastFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	t.Cleanup(srv.Close)
	e := apptest.New(t)
	var dst struct{ ID string }
	e.Admin.MustDo(201, "POST", "/api/v1/event-destinations", map[string]any{"name": "flaky", "spec": map[string]any{"type": "webhook", "enabled": true,
		"webhook_url": srv.URL, "allow_private_network": true, "max_attempts": 1, "filter": map[string]any{"events": []string{"routing.*"}}}}, &dst)
	ctx := context.Background()
	e.App.Relay.Round(ctx)
	switchEvent(t, e)
	e.Eventually(func() bool {
		e.App.Relay.Round(ctx)
		return e.QueryInt(`SELECT count(*) FROM event_deliveries WHERE status = 'deferred'`) == 1
	}, "first failed attempt")
	if _, err := e.App.DB.W.Exec(`UPDATE event_deliveries SET next_attempt_at = 0`); err != nil {
		t.Fatal(err)
	}
	e.Eventually(func() bool {
		e.App.Relay.Round(ctx)
		return e.QueryInt(`SELECT count(*) FROM event_deliveries WHERE status = 'dead'`) == 1
	}, "gave up")
	var list struct {
		Items []struct {
			Status     string `json:"status"`
			LastStatus *int   `json:"last_status"`
			LastError  string `json:"last_error"`
		} `json:"items"`
	}
	e.Admin.MustDo(200, "GET", "/api/v1/event-destinations/"+dst.ID+"/deliveries", nil, &list)
	if len(list.Items) != 1 {
		t.Fatalf("deliveries %+v", list.Items)
	}
	d := list.Items[0]
	if d.Status != "dead" || d.LastStatus == nil || *d.LastStatus != 500 || !strings.Contains(d.LastError, "gave up after 1 attempts") {
		t.Fatalf("delivery %+v (last status %v)", d, d.LastStatus)
	}
}

// A channel whose secret reference cannot be read says so, naming the
// reference rather than claiming nothing was configured.
func TestChannelTestNamesAnUnreadableReference(t *testing.T) {
	e := apptest.New(t)
	var ch struct{ ID string }
	e.Admin.MustDo(201, "POST", "/api/v1/notification-channels", map[string]any{"name": "tg", "spec": map[string]any{"type": "telegram", "enabled": true,
		"telegram_chat_id": "-100", "secret_ref": "env:HP_SECRET_TEST_UNSET_TELEGRAM"}}, &ch)
	var res struct {
		Delivered bool   `json:"delivered"`
		Error     string `json:"error"`
	}
	e.Admin.MustDo(200, "POST", "/api/v1/notification-channels/"+ch.ID+"/test", nil, &res)
	if res.Delivered || !strings.Contains(res.Error, "env:HP_SECRET_TEST_UNSET_TELEGRAM could not be read") {
		t.Fatalf("test result %+v", res)
	}
}

// Backups can be downloaded as an archive and deleted, and names cannot
// reach outside the backups directory.
func TestBackupDownloadAndDelete(t *testing.T) {
	e := apptest.New(t)
	var made struct {
		Path string `json:"path"`
	}
	e.Admin.MustDo(200, "POST", "/api/v1/system/backups", map[string]any{}, &made)
	name := filepath.Base(made.Path)

	code, body, hdr := e.Admin.GetRaw("/api/v1/system/backups/" + name + "/archive")
	if code != 200 || hdr.Get("Content-Type") != "application/gzip" || !strings.Contains(hdr.Get("Content-Disposition"), name) {
		t.Fatalf("download %d %v", code, hdr)
	}
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	files := map[string]bool{}
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		files[h.Name] = true
	}
	if !files[name+"/manifest.json"] || !files[name+"/harbor.db"] {
		t.Fatalf("archive files %v", files)
	}

	tests := []struct {
		name string
		path string
	}{
		{"traversal", "/api/v1/system/backups/..%2Fharbor.db"},
		{"unknown", "/api/v1/system/backups/20000101T000000Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code := e.Admin.Do("DELETE", tt.path, nil, nil); code != 404 {
				t.Fatalf("%s: %d", tt.path, code)
			}
		})
	}
	e.Admin.MustDo(204, "DELETE", "/api/v1/system/backups/"+name, nil, nil)
	if _, err := os.Stat(made.Path); !os.IsNotExist(err) {
		t.Fatalf("backup still on disk: %v", err)
	}
	if n := e.QueryInt(`SELECT count(*) FROM audit_log WHERE action IN ('system.backup.download', 'system.backup.delete')`); n != 2 {
		t.Fatalf("audited %d", n)
	}
}

// The audit log filters by time and free text on the server, across pages.
func TestAuditSinceAndSearch(t *testing.T) {
	e := apptest.New(t)
	e.Admin.MustDo(201, "POST", "/api/v1/workspaces", map[string]any{"name": "audit-findme", "spec": map[string]any{}}, nil)
	type page struct {
		Items []struct {
			Action string `json:"action"`
		} `json:"items"`
	}
	tests := []struct {
		name  string
		query string
		want  func(p page) bool
	}{
		{"text in target or details", "q=audit-findme", func(p page) bool { return len(p.Items) >= 1 }},
		{"no match", "q=nothing-like-this", func(p page) bool { return len(p.Items) == 0 }},
		{"wildcards are literal", "q=%25", func(p page) bool { return len(p.Items) == 0 }},
		{"future since", "since=2999-01-01T00:00:00Z", func(p page) bool { return len(p.Items) == 0 }},
		{"past since", "since=2000-01-01T00:00:00Z", func(p page) bool { return len(p.Items) > 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p page
			e.Admin.MustDo(200, "GET", "/api/v1/audit?"+tt.query, nil, &p)
			if !tt.want(p) {
				t.Fatalf("%s: %+v", tt.query, p.Items)
			}
		})
	}
	if code := e.Admin.Do("GET", "/api/v1/audit?since=yesterday", nil, nil); code != 422 {
		t.Fatalf("bad since: %d", code)
	}
}
