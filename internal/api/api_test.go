package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
)

type echoOut struct {
	Body struct {
		OK bool `json:"ok"`
	}
}

type nameIn struct {
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"8"`
	}
}

func testServer(t *testing.T, principal *auth.Principal) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	r.Use(RequestID)
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithPrincipal(req.Context(), principal)))
		})
	})
	a := New(r, "test")
	ok := func(context.Context, *struct{}) (*echoOut, error) {
		o := &echoOut{}
		o.Body.OK = true
		return o, nil
	}
	Register(a, Op{ID: "pub", Method: http.MethodGet, Path: "/pub", Permission: auth.PermPublic}, ok)
	Register(a, Op{ID: "read", Method: http.MethodGet, Path: "/read", Permission: auth.PermConfigRead}, ok)
	Register(a, Op{ID: "write", Method: http.MethodPost, Path: "/write", Permission: auth.PermConfigWrite},
		func(context.Context, *nameIn) (*echoOut, error) { return &echoOut{}, nil })
	Register(a, Op{ID: "boom", Method: http.MethodGet, Path: "/boom", Permission: auth.PermPublic},
		func(context.Context, *struct{}) (*echoOut, error) {
			return nil, errors.New("db password=hunter2 exploded")
		})
	return r
}

func do(h http.Handler, method, path, body string) (*httptest.ResponseRecorder, ErrorBody) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var eb ErrorBody
	_ = json.Unmarshal(rec.Body.Bytes(), &eb)
	return rec, eb
}

func TestPermissionsAndCSRF(t *testing.T) {
	viewer := auth.NewPrincipal(auth.KindUser, "usr_v", "v", auth.RoleViewer, nil)
	opNoCSRF := auth.NewPrincipal(auth.KindUser, "usr_o", "o", auth.RoleOperator, nil)
	opCSRF := auth.NewPrincipal(auth.KindUser, "usr_o", "o", auth.RoleOperator, nil)
	opCSRF.CSRFChecked = true
	svc := auth.NewPrincipal(auth.KindService, "pri_s", "s", "", []auth.Permission{auth.PermConfigWrite})
	svc.AuthMethod = "api_token"
	tests := []struct {
		name   string
		p      *auth.Principal
		method string
		path   string
		body   string
		status int
		code   string
	}{
		{name: "public anon", p: auth.Anonymous, method: "GET", path: "/pub", status: 200},
		{name: "read anon", p: auth.Anonymous, method: "GET", path: "/read", status: 401, code: "unauthenticated"},
		{name: "read viewer", p: viewer, method: "GET", path: "/read", status: 200},
		{name: "write viewer", p: viewer, method: "POST", path: "/write", body: `{"name":"x"}`, status: 403, code: "forbidden"},
		{name: "write operator without csrf", p: opNoCSRF, method: "POST", path: "/write", body: `{"name":"x"}`, status: 403, code: "forbidden"},
		{name: "write operator with csrf", p: opCSRF, method: "POST", path: "/write", body: `{"name":"x"}`, status: 200},
		{name: "write service token no csrf needed", p: svc, method: "POST", path: "/write", body: `{"name":"x"}`, status: 200},
		{name: "validation envelope", p: opCSRF, method: "POST", path: "/write", body: `{"name":"waytoolongname"}`, status: 422, code: "validation_failed"},
		{name: "internal error is redacted", p: auth.Anonymous, method: "GET", path: "/boom", status: 500, code: "internal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, eb := do(testServer(t, tt.p), tt.method, tt.path, tt.body)
			if rec.Code != tt.status {
				t.Fatalf("status %d want %d: %s", rec.Code, tt.status, rec.Body.String())
			}
			if tt.code != "" {
				if eb.Error.Code != tt.code {
					t.Fatalf("code %q want %q", eb.Error.Code, tt.code)
				}
				if eb.Error.RequestID == "" {
					t.Fatal("missing request_id in envelope")
				}
			}
			if strings.Contains(rec.Body.String(), "hunter2") {
				t.Fatal("internal error leaked")
			}
		})
	}
}

func TestOpenAPIHasPermissions(t *testing.T) {
	r := chi.NewRouter()
	a := New(r, "test")
	Register(a, Op{ID: "read", Method: http.MethodGet, Path: "/read", Permission: auth.PermConfigRead},
		func(context.Context, *struct{}) (*echoOut, error) { return &echoOut{}, nil })
	op := a.Huma.OpenAPI().Paths["/read"].Get
	if op.Extensions["x-permission"] != "config.read" {
		t.Fatalf("x-permission missing: %+v", op.Extensions)
	}
}

func TestRegisterWithoutPermissionPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	a := New(chi.NewRouter(), "test")
	Register(a, Op{ID: "x", Method: http.MethodGet, Path: "/x"},
		func(context.Context, *struct{}) (*echoOut, error) { return nil, nil })
}

func TestETagAndCursor(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		current  int64
		required bool
		status   int
	}{
		{name: "match", header: ETag(3), current: 3},
		{name: "weak match", header: "W/" + ETag(3), current: 3},
		{name: "star", header: "*", current: 9},
		{name: "mismatch", header: ETag(2), current: 3, status: 412},
		{name: "missing optional", current: 3},
		{name: "missing required", current: 3, required: true, status: 428},
		{name: "garbage", header: "abc", current: 3, status: 422},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckIfMatch(tt.header, tt.current, tt.required)
			var ae *Error
			switch {
			case tt.status == 0 && err != nil:
				t.Fatalf("unexpected %v", err)
			case tt.status != 0 && (!errors.As(err, &ae) || ae.GetStatus() != tt.status):
				t.Fatalf("got %v want %d", err, tt.status)
			}
		})
	}
	c := EncodeCursor("acc_01")
	if k, err := DecodeCursor(c); err != nil || k != "acc_01" {
		t.Fatalf("cursor round trip: %q %v", k, err)
	}
	if _, err := DecodeCursor("!!!"); err == nil {
		t.Fatal("expected cursor error")
	}
	p := Paginate([]string{"a", "b", "c"}, 2, func(s string) string { return s })
	if len(p.Items) != 2 || p.NextCursor == "" {
		t.Fatalf("paginate: %+v", p)
	}
}
