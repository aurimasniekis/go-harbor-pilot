package controlplane

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/audit"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ids"
)

// BundleAPIVersion identifies the bundle format.
const BundleAPIVersion = "harbor-pilot/v1"

// Bundle is a portable export of managed configuration. Secrets are never
// included; references between objects are written as "ref:<kind>/<name>"
// so a bundle can be imported into another instance.
type Bundle struct {
	APIVersion     string           `json:"api_version"`
	ExportedAt     time.Time        `json:"exported_at"`
	ConfigRevision int64            `json:"config_revision"`
	Resources      []BundleResource `json:"resources"`
	Plans          []BundlePlan     `json:"plans"`
}

// BundleResource is one exported object.
type BundleResource struct {
	Kind      string                `json:"kind"`
	Name      string                `json:"name"`
	Spec      json.RawMessage       `json:"spec"`
	ManagedBy string                `json:"managed_by,omitempty"`
	Secrets   map[string]SecretInfo `json:"secrets,omitempty" doc:"Presence only; values are never exported."`
}

// BundlePlan carries the published document of a routing plan.
type BundlePlan struct {
	Plan      string          `json:"plan"`
	Version   int64           `json:"version"`
	Published json.RawMessage `json:"published"`
}

const refPrefix = "ref:"

// Export builds a bundle of the selected kinds (all when empty).
func (s *Service) Export(ctx context.Context, kinds []string) (*Bundle, error) {
	snap := s.Snapshot()
	b := &Bundle{APIVersion: BundleAPIVersion, ExportedAt: s.o.Clock.Now().UTC(), ConfigRevision: snap.ConfigRevision,
		Resources: []BundleResource{}, Plans: []BundlePlan{}}
	names := map[string]string{} // id -> ref
	type item struct {
		kind Kind
		obj  *Object
	}
	var items []item
	for _, k := range Registry {
		if len(kinds) > 0 && !contains(kinds, k.KindName()) {
			continue
		}
		after := ""
		for {
			page, err := s.List(ctx, k, after, 500)
			if err != nil {
				return nil, err
			}
			for _, o := range page {
				names[o.ID] = refPrefix + k.KindName() + "/" + o.Name
				items = append(items, item{k, o})
			}
			if len(page) < 500 {
				break
			}
			after = page[len(page)-1].ID
		}
	}
	// Also allow references to objects outside the exported kinds.
	for id := range allIDs(snap) {
		if _, ok := names[id]; !ok {
			if k, n := lookupName(snap, id); n != "" {
				names[id] = refPrefix + k + "/" + n
			}
		}
	}
	for _, it := range items {
		b.Resources = append(b.Resources, BundleResource{Kind: it.kind.KindName(), Name: it.obj.Name,
			Spec: rewriteIDs(it.obj.Spec, names), ManagedBy: it.obj.ManagedBy, Secrets: it.obj.Secrets})
	}
	if len(kinds) == 0 || contains(kinds, KindPlan) {
		planIDs := make([]string, 0, len(snap.Plans))
		for id := range snap.Plans {
			planIDs = append(planIDs, id)
		}
		sort.Strings(planIDs)
		for _, id := range planIDs {
			p := snap.Plans[id]
			if p.Published == nil {
				continue
			}
			raw, _ := json.Marshal(p.Published.Document)
			b.Plans = append(b.Plans, BundlePlan{Plan: p.Name, Version: p.Published.Version, Published: rewriteIDs(raw, names)})
		}
	}
	return b, nil
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func allIDs(s *Snapshot) map[string]bool {
	out := map[string]bool{}
	for id := range s.Workspaces {
		out[id] = true
	}
	for id := range s.Projects {
		out[id] = true
	}
	for id := range s.Providers {
		out[id] = true
	}
	for id := range s.Accounts {
		out[id] = true
	}
	for id := range s.Pools {
		out[id] = true
	}
	for id := range s.Aliases {
		out[id] = true
	}
	for id := range s.Plans {
		out[id] = true
	}
	for id := range s.Bindings {
		out[id] = true
	}
	return out
}

func lookupName(s *Snapshot, id string) (string, string) {
	switch {
	case s.Workspaces[id] != nil:
		return KindWorkspace, s.Workspaces[id].Name
	case s.Projects[id] != nil:
		return KindProject, s.Projects[id].Name
	case s.Providers[id] != nil:
		return KindProvider, s.Providers[id].Name
	case s.Accounts[id] != nil:
		return KindAccount, s.Accounts[id].Name
	case s.Pools[id] != nil:
		return KindPool, s.Pools[id].Name
	case s.Aliases[id] != nil:
		return KindAlias, s.Aliases[id].Name
	case s.Plans[id] != nil:
		return KindPlan, s.Plans[id].Name
	case s.Bindings[id] != nil:
		return KindClientBinding, s.Bindings[id].Name
	}
	return "", ""
}

// rewriteIDs replaces every string value equal to a known id with its ref.
func rewriteIDs(raw json.RawMessage, names map[string]string) json.RawMessage {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return raw
	}
	out, _ := json.Marshal(walkStrings(v, func(s string) string {
		if r, ok := names[s]; ok {
			return r
		}
		return s
	}))
	return out
}

func walkStrings(v any, fn func(string) string) any {
	switch t := v.(type) {
	case string:
		return fn(t)
	case []any:
		for i := range t {
			t[i] = walkStrings(t[i], fn)
		}
		return t
	case map[string]any:
		for k := range t {
			t[k] = walkStrings(t[k], fn)
		}
		return t
	}
	return v
}

// ImportOptions control an import.
type ImportOptions struct {
	// Modes per kind: "merge" (create missing, replace existing by name) or
	// "replace" (merge, then delete objects of that kind absent from the
	// bundle). Kinds not listed default to merge.
	Modes          map[string]string
	DryRun         bool
	PublishPlans   bool
	ManagedBy      string
	AllowTerraform bool // allow overwriting terraform-managed objects
}

// ImportAction is one planned or applied change.
type ImportAction struct {
	Action string `json:"action" enum:"create,update,unchanged,delete,publish,skip"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
}

// ImportResult describes an import.
type ImportResult struct {
	DryRun         bool           `json:"dry_run"`
	Actions        []ImportAction `json:"actions"`
	Warnings       []string       `json:"warnings"`
	ConfigRevision int64          `json:"config_revision"`
}

// Import applies a bundle atomically: all changes commit together and the
// resulting configuration must compile, otherwise nothing changes.
func (s *Service) Import(ctx context.Context, b *Bundle, o ImportOptions) (*ImportResult, error) {
	if b.APIVersion != BundleAPIVersion {
		return nil, &ValidationError{Errors: []FieldError{{Location: "body.api_version", Message: "unsupported bundle version " + b.APIVersion}}}
	}
	if o.ManagedBy == "" {
		o.ManagedBy = ManagedImport
	}
	for k, m := range o.Modes {
		if _, ok := KindByName(k); !ok || (m != "merge" && m != "replace") {
			return nil, &ValidationError{Errors: []FieldError{{Location: "body.modes." + k, Message: "unknown kind or mode (merge|replace)"}}}
		}
	}
	res := &ImportResult{DryRun: o.DryRun, Actions: []ImportAction{}, Warnings: []string{}}
	m := Mutation{ManagedBy: o.ManagedBy, Summary: fmt.Sprintf("import bundle (%d resources)", len(b.Resources)), DryRun: o.DryRun}
	err := s.mutate(ctx, func(tx *sql.Tx, now time.Time, rev int64) (string, error) {
		res.ConfigRevision = rev
		byKind := map[string][]BundleResource{}
		for _, r := range b.Resources {
			byKind[r.Kind] = append(byKind[r.Kind], r)
		}
		for k := range byKind {
			if _, ok := KindByName(k); !ok {
				return "", &ValidationError{Errors: []FieldError{{Location: "body.resources", Message: "unknown kind " + k}}}
			}
		}
		resolve := func(ref string) (string, bool) {
			kind, name, ok := strings.Cut(strings.TrimPrefix(ref, refPrefix), "/")
			if !ok {
				return "", false
			}
			var id string
			if err := tx.QueryRowContext(ctx, `SELECT id FROM resources WHERE kind = ? AND name = ?`, kind, name).Scan(&id); err != nil {
				return "", false
			}
			return id, true
		}
		var unresolved []string
		resolveSpec := func(raw json.RawMessage) json.RawMessage {
			var v any
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			if err := dec.Decode(&v); err != nil {
				return raw
			}
			out, _ := json.Marshal(walkStrings(v, func(s string) string {
				if !strings.HasPrefix(s, refPrefix) {
					return s
				}
				if id, ok := resolve(s); ok {
					return id
				}
				unresolved = append(unresolved, s)
				return s
			}))
			return out
		}
		for _, k := range Registry {
			items := byKind[k.KindName()]
			present := map[string]bool{}
			for _, r := range items {
				present[r.Name] = true
				spec := resolveSpec(r.Spec)
				cur, err := s.get(ctx, tx, k, r.Name)
				switch {
				case err == ErrNotFound:
					if _, err := s.CreateTx(ctx, tx, now, rev, k, r.Name, spec, nil, m); err != nil {
						return "", prefixErr(err, k.KindName()+"/"+r.Name)
					}
					res.Actions = append(res.Actions, ImportAction{Action: "create", Kind: k.KindName(), Name: r.Name})
				case err != nil:
					return "", err
				default:
					if cur.ManagedBy == ManagedTerraform && !o.AllowTerraform {
						res.Actions = append(res.Actions, ImportAction{Action: "skip", Kind: k.KindName(), Name: r.Name, Detail: "terraform-managed"})
						continue
					}
					v := NewValidator(cur.ID, s.lookupFn(ctx, tx))
					canon, _ := k.Canonicalize(spec, v)
					if v.OK() && bytes.Equal(canon, cur.Spec) {
						res.Actions = append(res.Actions, ImportAction{Action: "unchanged", Kind: k.KindName(), Name: r.Name})
						continue
					}
					if _, _, err := s.ReplaceTx(ctx, tx, now, rev, k, cur.ID, r.Name, spec, nil, m); err != nil {
						return "", prefixErr(err, k.KindName()+"/"+r.Name)
					}
					res.Actions = append(res.Actions, ImportAction{Action: "update", Kind: k.KindName(), Name: r.Name})
				}
				for name, si := range r.Secrets {
					if si.Source == "stored" {
						res.Warnings = append(res.Warnings, fmt.Sprintf("%s/%s: secret %q is not portable; set it after import", k.KindName(), r.Name, name))
					}
				}
			}
		}
		// Deletions for replace-mode kinds, in reverse dependency order.
		for i := len(Registry) - 1; i >= 0; i-- {
			k := Registry[i]
			if o.Modes[k.KindName()] != "replace" {
				continue
			}
			names, err := allNames(ctx, tx, k.KindName())
			if err != nil {
				return "", err
			}
			present := map[string]bool{}
			for _, r := range byKind[k.KindName()] {
				present[r.Name] = true
			}
			for _, n := range names {
				if present[n] {
					continue
				}
				if _, err := s.DeleteTx(ctx, tx, now, rev, k, n, m); err != nil {
					return "", prefixErr(err, k.KindName()+"/"+n)
				}
				res.Actions = append(res.Actions, ImportAction{Action: "delete", Kind: k.KindName(), Name: n})
			}
		}
		if len(unresolved) > 0 {
			return "", &ValidationError{Errors: []FieldError{{Location: "body.resources", Message: "unresolved references: " + strings.Join(unresolved, ", ")}}}
		}
		if o.PublishPlans {
			for _, p := range b.Plans {
				var planID string
				if err := tx.QueryRowContext(ctx, `SELECT id FROM resources WHERE kind = ? AND name = ?`, KindPlan, p.Plan).Scan(&planID); err != nil {
					return "", &ValidationError{Errors: []FieldError{{Location: "body.plans", Message: "unknown plan " + p.Plan}}}
				}
				_, canon, errs := canonicalDocument(resolveSpec(p.Published))
				if len(errs) > 0 {
					return "", &ValidationError{Errors: errs}
				}
				var cur sql.NullString
				_ = tx.QueryRowContext(ctx, `SELECT document FROM policy_versions WHERE plan_id = ? AND status = 'published'`, planID).Scan(&cur)
				if cur.Valid && cur.String == string(canon) {
					res.Actions = append(res.Actions, ImportAction{Action: "unchanged", Kind: "routing_plan_version", Name: p.Plan})
					continue
				}
				var next int64
				if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM policy_versions WHERE plan_id = ?`, planID).Scan(&next); err != nil {
					return "", err
				}
				if _, err := tx.ExecContext(ctx, `UPDATE policy_versions SET status = 'superseded' WHERE plan_id = ? AND status = 'published'`, planID); err != nil {
					return "", err
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO policy_versions(id, plan_id, version, status, document, note, created_by, created_at, published_by, published_at)
					VALUES (?, ?, ?, 'published', ?, 'imported', ?, ?, ?, ?)`, ids.New(ids.PolicyVersion), planID, next, string(canon),
					auth.FromContext(ctx).ID, now.UnixMilli(), auth.FromContext(ctx).ID, now.UnixMilli()); err != nil {
					return "", err
				}
				res.Actions = append(res.Actions, ImportAction{Action: "publish", Kind: "routing_plan_version", Name: p.Plan, Detail: fmt.Sprintf("v%d", next)})
			}
		}
		if err := audit.Record(ctx, tx, audit.Entry{Action: "config.import", Details: map[string]any{"actions": len(res.Actions), "dry_run": o.DryRun}}); err != nil {
			return "", err
		}
		return "import bundle", nil
	}, m)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func allNames(ctx context.Context, tx *sql.Tx, kind string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name FROM resources WHERE kind = ? ORDER BY name`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func prefixErr(err error, what string) error {
	if ve, ok := err.(*ValidationError); ok {
		out := &ValidationError{}
		for _, e := range ve.Errors {
			out.Errors = append(out.Errors, FieldError{Location: what + ":" + e.Location, Message: e.Message})
		}
		return out
	}
	return fmt.Errorf("%s: %w", what, err)
}
