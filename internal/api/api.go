// Package api hosts the versioned control-plane API. Operations are declared
// with huma so the OpenAPI 3.1 document is generated from Go types; the
// committed api/openapi.yaml is checked against the generated document in
// tests. Every operation declares exactly one permission (x-permission).
package api

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
)

// Version is the API version segment.
const Version = "v1"

// Prefix is the mount point of the control-plane API.
const Prefix = "/api/" + Version

// API wraps the huma API with Harbor Pilot conventions.
type API struct {
	Huma huma.API
}

// New mounts a huma API on r (which should already be routed at Prefix).
func New(r chi.Router, version string) *API {
	cfg := huma.DefaultConfig("Harbor Pilot API", version)
	cfg.Info.Description = "Control-plane API for Harbor Pilot: configuration, routing plans, " +
		"accounting, activity, traces, events and notifications. The gateway data plane " +
		"(/v1/messages, /v1/chat/completions, /v1/responses, /v1beta/models) is documented separately."
	cfg.Info.License = &huma.License{Name: "Apache-2.0", Identifier: "Apache-2.0"}
	cfg.Servers = []*huma.Server{{URL: Prefix}}
	cfg.OpenAPIPath = "/openapi"
	cfg.DocsPath = ""
	cfg.SchemasPath = ""
	cfg.CreateHooks = nil // no $schema links in responses
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"session": {Type: "apiKey", In: "cookie", Name: "__Host-hp_session",
			Description: "Browser session cookie. Mutations additionally require X-CSRF-Token."},
		"bearer": {Type: "http", Scheme: "bearer",
			Description: "Service principal API token (hpa_…)."},
	}
	cfg.Security = []map[string][]string{{"session": {}}, {"bearer": {}}}
	return &API{Huma: humachi.New(r, cfg)}
}

// Op describes one operation.
type Op struct {
	ID          string
	Method      string
	Path        string
	Summary     string
	Description string
	Tags        []string
	Permission  auth.Permission
	Status      int
	// Idempotent marks POST operations that accept an Idempotency-Key.
	Idempotent bool
	// AllowLimited lets limited (setup/recovery) sessions call the operation.
	AllowLimited bool
	MaxBodyBytes int64
}

// Register adds a typed operation guarded by its permission.
func Register[I, O any](a *API, op Op, h func(ctx context.Context, in *I) (*O, error)) {
	if op.Permission == "" {
		panic("api: operation " + op.ID + " has no permission")
	}
	ho := huma.Operation{
		OperationID:   op.ID,
		Method:        op.Method,
		Path:          op.Path,
		Summary:       op.Summary,
		Description:   op.Description,
		Tags:          op.Tags,
		DefaultStatus: op.Status,
		MaxBodyBytes:  op.MaxBodyBytes,
		Extensions:    map[string]any{"x-permission": string(op.Permission)},
	}
	if op.Permission == auth.PermPublic {
		ho.Security = []map[string][]string{}
	}
	if op.Idempotent {
		ho.Extensions["x-idempotent"] = true
	}
	mutating := op.Method != http.MethodGet && op.Method != http.MethodHead
	huma.Register(a.Huma, ho, func(ctx context.Context, in *I) (*O, error) {
		out, err := guard(ctx, op, mutating, func() (*O, error) { return h(ctx, in) })
		if err != nil {
			var ae *Error
			if errors.As(err, &ae) {
				ae.ErrorBody.Error.RequestID = RequestIDFrom(ctx)
				return nil, ae
			}
		}
		return out, err
	})
}

func guard[O any](ctx context.Context, op Op, mutating bool, h func() (*O, error)) (*O, error) {
	{
		p := auth.FromContext(ctx)
		if op.Permission != auth.PermPublic {
			if !p.Authenticated() {
				return nil, Unauthorized()
			}
			if p.Limited && !op.AllowLimited {
				return nil, Forbidden("this session is limited to completing setup or recovery")
			}
			if mutating && p.AuthMethod != "api_token" && p.Kind != auth.KindSystem && !p.CSRFChecked {
				if p.CSRFOrigin != "" {
					return nil, Errorf(http.StatusForbidden, "origin_mismatch",
						"this page is open at "+p.CSRFOrigin+", but the server only accepts changes from its public URL; open Harbor Pilot there or set public_url")
				}
				return nil, Errorf(http.StatusForbidden, "csrf_invalid", "your session changed since this page loaded; reload the page and try again")
			}
			if !p.Can(op.Permission) {
				return nil, Forbidden("permission " + string(op.Permission) + " required")
			}
		}
		return h()
	}
}

// ---------- pagination ----------

// PageParams are embedded in list inputs.
type PageParams struct {
	Cursor string `query:"cursor" doc:"Opaque cursor from a previous page's next_cursor."`
	Limit  int    `query:"limit" minimum:"1" maximum:"500" default:"50" doc:"Maximum items to return."`
}

// Page is a cursor-paginated list body.
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty" doc:"Present when more items are available."`
}

// EncodeCursor encodes an opaque cursor from a sort key.
func EncodeCursor(key string) string {
	if key == "" {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte("c1|" + key))
}

// DecodeCursor decodes a cursor produced by EncodeCursor.
func DecodeCursor(c string) (string, error) {
	if c == "" {
		return "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil || !strings.HasPrefix(string(b), "c1|") {
		return "", Invalid("invalid cursor", FieldError{Location: "query.cursor", Message: "malformed cursor"})
	}
	return strings.TrimPrefix(string(b), "c1|"), nil
}

// Paginate trims items fetched with limit+1 and computes next_cursor using key.
func Paginate[T any](items []T, limit int, key func(T) string) Page[T] {
	if items == nil {
		items = []T{}
	}
	if len(items) > limit {
		items = items[:limit]
		return Page[T]{Items: items, NextCursor: EncodeCursor(key(items[len(items)-1]))}
	}
	return Page[T]{Items: items}
}

// NormalizeLimit applies the default and bounds.
func (p PageParams) NormalizeLimit() int {
	switch {
	case p.Limit <= 0:
		return 50
	case p.Limit > 500:
		return 500
	}
	return p.Limit
}

// ---------- optimistic concurrency ----------

// ETag formats an object revision as a strong entity tag.
func ETag(revision int64) string { return `"r` + strconv.FormatInt(revision, 10) + `"` }

// ParseETag parses an If-Match value produced by ETag. "*" matches any.
func ParseETag(v string) (rev int64, any bool, err error) {
	v = strings.TrimSpace(v)
	if v == "*" {
		return 0, true, nil
	}
	v = strings.TrimPrefix(v, "W/")
	if len(v) < 4 || v[0] != '"' || v[len(v)-1] != '"' || v[1] != 'r' {
		return 0, false, fmt.Errorf("malformed entity tag")
	}
	n, err := strconv.ParseInt(v[2:len(v)-1], 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("malformed entity tag")
	}
	return n, false, nil
}

// CheckIfMatch validates an If-Match header against the current revision.
// required=true rejects a missing header with 428.
func CheckIfMatch(header string, current int64, required bool) error {
	if header == "" {
		if required {
			return PreconditionRequired()
		}
		return nil
	}
	rev, anyRev, err := ParseETag(header)
	if err != nil {
		return Invalid("invalid If-Match header", FieldError{Location: "header.If-Match", Message: err.Error()})
	}
	if anyRev || rev == current {
		return nil
	}
	return PreconditionFailed(ETag(current))
}
