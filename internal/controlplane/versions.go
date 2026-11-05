package controlplane

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"sort"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/audit"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	"github.com/aurimasniekis/go-harbor-pilot/internal/events"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ids"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// PlanSchemaVersion is the current plan document schema.
const PlanSchemaVersion = 1

// ModelMapping maps requested model names to an alias. Match is an exact
// name or a glob (path.Match syntax, e.g. "claude-*").
type ModelMapping struct {
	Match   string `json:"match" doc:"Requested model name or glob (e.g. claude-sonnet-*)."`
	AliasID string `json:"alias_id"`
	Note    string `json:"note" maxLength:"200"`
}

// PlanRestrictions are hard constraints; scores can never override them.
type PlanRestrictions struct {
	AllowedProviderIDs     []string `json:"allowed_provider_ids" doc:"Empty = any."`
	DeniedProviderIDs      []string `json:"denied_provider_ids"`
	AllowedBilling         []string `json:"allowed_billing" doc:"Subset of metered_api, subscription, free, local; empty = any."`
	AllowedClassifications []string `json:"allowed_classifications" doc:"Subset of official, partial, unofficial, local; empty = any."`
	RequiredCapabilities   []string `json:"required_capabilities"`
}

// Fallback steps.
const (
	StepRetry             = "retry"
	StepAlternateAccount  = "alternate_account"
	StepAlternateModel    = "alternate_model"
	StepAlternateProvider = "alternate_provider"
	StepWait              = "wait"
	StepStop              = "stop"
)

// FallbackPolicy controls what happens when a candidate fails before any
// byte reached the client. Ambiguous executions are never replayed.
type FallbackPolicy struct {
	Steps             []string `json:"steps" doc:"Ordered steps: retry, alternate_account, alternate_model, alternate_provider, wait, stop."`
	MaxAttempts       int      `json:"max_attempts" minimum:"1" maximum:"10"`
	AllowPaidFallback bool     `json:"allow_paid_fallback" doc:"Allow falling back from free/subscription/local candidates to metered_api candidates."`
	WaitMaxSeconds    int      `json:"wait_max_seconds" minimum:"0" maximum:"600" doc:"Upper bound for the wait step."`
}

// PlanDocument is the versioned routing logic of a plan.
type PlanDocument struct {
	SchemaVersion  int              `json:"schema_version"`
	ModelMappings  []ModelMapping   `json:"model_mappings"`
	DefaultAliasID string           `json:"default_alias_id" doc:"Alias for requests whose model matches no mapping and no alias name; empty = reject."`
	Restrictions   PlanRestrictions `json:"restrictions"`
	Fallback       FallbackPolicy   `json:"fallback"`
	Privacy        PlanPrivacy      `json:"privacy" required:"false"`
	// Engine holds routing-engine sections (rules, decision tables, ranking,
	// privacy, compatibility); validated by the engine at compile time.
	Engine json.RawMessage `json:"engine,omitempty"`
}

// NormalizeDocument applies defaults and canonical ordering.
func NormalizeDocument(d *PlanDocument) {
	if d.SchemaVersion == 0 {
		d.SchemaVersion = PlanSchemaVersion
	}
	if d.ModelMappings == nil {
		d.ModelMappings = []ModelMapping{}
	}
	r := &d.Restrictions
	r.AllowedProviderIDs = SortedUnique(r.AllowedProviderIDs)
	r.DeniedProviderIDs = SortedUnique(r.DeniedProviderIDs)
	r.AllowedBilling = SortedUnique(r.AllowedBilling)
	r.AllowedClassifications = SortedUnique(r.AllowedClassifications)
	r.RequiredCapabilities = SortedUnique(r.RequiredCapabilities)
	if d.Fallback.Steps == nil {
		d.Fallback.Steps = []string{StepAlternateAccount, StepAlternateModel, StepStop}
	}
	if d.Fallback.MaxAttempts == 0 {
		d.Fallback.MaxAttempts = 3
	}
	if len(bytes.TrimSpace(d.Engine)) == 0 || string(bytes.TrimSpace(d.Engine)) == "null" {
		d.Engine = nil
	}
}

// ValidateDocument checks structure (refs are checked by ValidateDocumentRefs).
func ValidateDocument(d *PlanDocument) []FieldError {
	var out []FieldError
	fail := func(loc, msg string) { out = append(out, FieldError{Location: "body.document." + loc, Message: msg}) }
	if d.SchemaVersion != PlanSchemaVersion {
		fail("schema_version", fmt.Sprintf("unsupported schema version %d (this server supports %d)", d.SchemaVersion, PlanSchemaVersion))
	}
	if len(d.ModelMappings) > 200 {
		fail("model_mappings", "at most 200 mappings")
	}
	seen := map[string]bool{}
	for i, m := range d.ModelMappings {
		loc := fmt.Sprintf("model_mappings[%d]", i)
		if m.Match == "" || len(m.Match) > 200 {
			fail(loc+".match", "is required (max 200 characters)")
		} else if _, err := path.Match(m.Match, ""); err != nil {
			fail(loc+".match", "invalid glob pattern")
		}
		if seen[m.Match] {
			fail(loc+".match", "duplicate match; the first mapping would always win")
		}
		seen[m.Match] = true
		if m.AliasID == "" {
			fail(loc+".alias_id", "is required")
		}
	}
	for _, b := range d.Restrictions.AllowedBilling {
		if !slices.Contains([]string{BillingMetered, BillingSubscription, BillingFree, BillingLocal}, b) {
			fail("restrictions.allowed_billing", "unknown billing class "+b)
		}
	}
	for _, c := range d.Restrictions.AllowedClassifications {
		if !slices.Contains([]string{"official", "partial", "unofficial", "local"}, c) {
			fail("restrictions.allowed_classifications", "unknown classification "+c)
		}
	}
	for _, c := range d.Restrictions.RequiredCapabilities {
		if !slices.Contains(Capabilities, c) {
			fail("restrictions.required_capabilities", "unknown capability "+c)
		}
	}
	for _, p := range d.Restrictions.AllowedProviderIDs {
		if slices.Contains(d.Restrictions.DeniedProviderIDs, p) {
			fail("restrictions", "provider "+p+" is both allowed and denied")
		}
	}
	valid := []string{StepRetry, StepAlternateAccount, StepAlternateModel, StepAlternateProvider, StepWait, StepStop}
	for i, st := range d.Fallback.Steps {
		if !slices.Contains(valid, st) {
			fail(fmt.Sprintf("fallback.steps[%d]", i), "unknown step "+st)
		}
		if st == StepStop && i != len(d.Fallback.Steps)-1 {
			fail(fmt.Sprintf("fallback.steps[%d]", i), "stop must be the last step")
		}
	}
	if d.Fallback.MaxAttempts < 1 || d.Fallback.MaxAttempts > 10 {
		fail("fallback.max_attempts", "must be between 1 and 10")
	}
	if d.Fallback.WaitMaxSeconds < 0 || d.Fallback.WaitMaxSeconds > 600 {
		fail("fallback.wait_max_seconds", "must be between 0 and 600")
	}
	if slices.Contains(d.Fallback.Steps, StepWait) && d.Fallback.WaitMaxSeconds == 0 {
		fail("fallback.wait_max_seconds", "must be positive when the wait step is used")
	}
	return out
}

// ValidateDocumentRefs checks references against a snapshot.
func ValidateDocumentRefs(s *Snapshot, d *PlanDocument) []FieldError {
	var out []FieldError
	fail := func(loc, msg string) { out = append(out, FieldError{Location: "body.document." + loc, Message: msg}) }
	if id := d.Privacy.PolicyID; id != "" && s.Privacy[id] == nil {
		fail("privacy.policy_id", "unknown privacy policy "+id)
	}
	if id := d.Privacy.TracePolicyID; id != "" && s.TracePols[id] == nil {
		fail("privacy.trace_policy_id", "unknown trace policy "+id)
	}
	for i, m := range d.ModelMappings {
		if _, ok := s.Aliases[m.AliasID]; m.AliasID != "" && !ok {
			fail(fmt.Sprintf("model_mappings[%d].alias_id", i), "unknown model alias "+m.AliasID)
		}
	}
	if d.DefaultAliasID != "" {
		if _, ok := s.Aliases[d.DefaultAliasID]; !ok {
			fail("default_alias_id", "unknown model alias "+d.DefaultAliasID)
		}
	}
	for _, p := range append(slices.Clone(d.Restrictions.AllowedProviderIDs), d.Restrictions.DeniedProviderIDs...) {
		if _, ok := s.Providers[p]; !ok {
			fail("restrictions", "unknown provider "+p)
		}
	}
	return out
}

// Version is a stored plan version.
type Version struct {
	ID          string          `json:"id"`
	PlanID      string          `json:"plan_id"`
	Version     int64           `json:"version"`
	Status      string          `json:"status" enum:"draft,published,superseded,archived"`
	Document    PlanDocument    `json:"document"`
	Validation  json.RawMessage `json:"validation"`
	Note        string          `json:"note"`
	CreatedBy   string          `json:"created_by"`
	CreatedAt   time.Time       `json:"created_at"`
	PublishedBy string          `json:"published_by,omitempty"`
	PublishedAt *time.Time      `json:"published_at,omitempty"`
	RollbackOf  string          `json:"rollback_of,omitempty"`
}

const verCols = `id, plan_id, version, status, document, validation, note, created_by, created_at, published_by, published_at, rollback_of`

func scanVersion(r interface{ Scan(...any) error }) (*Version, error) {
	var v Version
	var doc, val string
	var created int64
	var pubBy, rb sql.NullString
	var pubAt sql.NullInt64
	if err := r.Scan(&v.ID, &v.PlanID, &v.Version, &v.Status, &doc, &val, &v.Note, &v.CreatedBy, &created, &pubBy, &pubAt, &rb); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(doc), &v.Document); err != nil {
		return nil, err
	}
	v.Validation = json.RawMessage(val)
	v.CreatedAt = time.UnixMilli(created).UTC()
	v.PublishedBy, v.RollbackOf = pubBy.String, rb.String
	if pubAt.Valid {
		t := time.UnixMilli(pubAt.Int64).UTC()
		v.PublishedAt = &t
	}
	return &v, nil
}

// ListVersions lists a plan's versions newest first.
func (s *Service) ListVersions(ctx context.Context, planID string) ([]*Version, error) {
	return store.Query(ctx, s.db.R, func(r *sql.Rows) (*Version, error) { return scanVersion(r) },
		`SELECT `+verCols+` FROM policy_versions WHERE plan_id = ? ORDER BY version DESC LIMIT 500`, planID)
}

// GetVersion loads a version of a plan.
func (s *Service) GetVersion(ctx context.Context, planID, versionID string) (*Version, error) {
	v, err := scanVersion(s.db.R.QueryRowContext(ctx, `SELECT `+verCols+` FROM policy_versions WHERE plan_id = ? AND id = ?`, planID, versionID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return v, err
}

func canonicalDocument(raw json.RawMessage) (PlanDocument, []byte, []FieldError) {
	var d PlanDocument
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return d, nil, []FieldError{{Location: "body.document", Message: "invalid document: " + err.Error()}}
	}
	NormalizeDocument(&d)
	b, _ := json.Marshal(d)
	return d, b, ValidateDocument(&d)
}

// SaveDraft creates a new draft (versionID == "") or replaces a draft.
func (s *Service) SaveDraft(ctx context.Context, planID, versionID string, raw json.RawMessage, note string, m Mutation) (*Version, error) {
	doc, canon, errs := canonicalDocument(raw)
	if len(errs) > 0 {
		return nil, &ValidationError{Errors: errs}
	}
	_ = doc
	if len(note) > 500 {
		return nil, &ValidationError{Errors: []FieldError{{Location: "body.note", Message: "at most 500 characters"}}}
	}
	id := versionID
	err := s.mutate(ctx, func(tx *sql.Tx, now time.Time, rev int64) (string, error) {
		plan, err := s.get(ctx, tx, PlanDef, planID)
		if err != nil {
			return "", err
		}
		if id == "" {
			var next int64
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM policy_versions WHERE plan_id = ?`, plan.ID).Scan(&next); err != nil {
				return "", err
			}
			id = ids.New(ids.PolicyVersion)
			if _, err := tx.ExecContext(ctx, `INSERT INTO policy_versions(id, plan_id, version, status, document, note, created_by, created_at)
				VALUES (?, ?, ?, 'draft', ?, ?, ?, ?)`, id, plan.ID, next, string(canon), note, auth.FromContext(ctx).ID, now.UnixMilli()); err != nil {
				return "", err
			}
			return fmt.Sprintf("draft v%d of plan %s", next, plan.Name), audit.Record(ctx, tx, audit.Entry{Action: "routing_plan.version.create",
				TargetKind: KindPlan, TargetID: plan.ID, Details: map[string]any{"version_id": id, "version": next}})
		}
		res, err := tx.ExecContext(ctx, `UPDATE policy_versions SET document = ?, note = ?, validation = '{}' WHERE id = ? AND plan_id = ? AND status = 'draft'`,
			string(canon), note, id, plan.ID)
		if err != nil {
			return "", err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return "", &ValidationError{Errors: []FieldError{{Location: "path.version_id", Message: "only draft versions can be edited"}}}
		}
		return "update draft of plan " + plan.Name, audit.Record(ctx, tx, audit.Entry{Action: "routing_plan.version.update",
			TargetKind: KindPlan, TargetID: plan.ID, Details: map[string]any{"version_id": id}})
	}, m)
	if err != nil {
		return nil, err
	}
	pl, err := s.Get(ctx, PlanDef, planID)
	if err != nil {
		return nil, err
	}
	return s.GetVersion(ctx, pl.ID, id)
}

// ValidationReport is the outcome of validating a version.
type ValidationReport struct {
	Valid     bool         `json:"valid"`
	Errors    []FieldError `json:"errors"`
	Warnings  []FieldError `json:"warnings"`
	CheckedAt time.Time    `json:"checked_at"`
	// ConfigRevision the version was validated against.
	ConfigRevision int64 `json:"config_revision"`
}

// ValidateVersion validates a version against the current snapshot
// (including engine compilation) and stores the report.
func (s *Service) ValidateVersion(ctx context.Context, planID, versionID string) (*ValidationReport, error) {
	v, err := s.GetVersion(ctx, planID, versionID)
	if err != nil {
		return nil, err
	}
	snap := s.Snapshot()
	rep := &ValidationReport{CheckedAt: s.o.Clock.Now().UTC(), ConfigRevision: snap.ConfigRevision, Errors: []FieldError{}, Warnings: []FieldError{}}
	rep.Errors = append(rep.Errors, ValidateDocument(&v.Document)...)
	rep.Errors = append(rep.Errors, ValidateDocumentRefs(snap, &v.Document)...)
	if PlanCompiler != nil && len(rep.Errors) == 0 {
		plan := snap.Plans[planID]
		if plan == nil {
			return nil, ErrNotFound
		}
		trial := *plan
		trial.Published = &VersionView{ID: v.ID, Version: v.Version, Document: v.Document}
		if _, err := PlanCompiler(snap, &trial); err != nil {
			var ve *ValidationError
			if errors.As(err, &ve) {
				rep.Errors = append(rep.Errors, ve.Errors...)
			} else {
				rep.Errors = append(rep.Errors, FieldError{Location: "body.document.engine", Message: err.Error()})
			}
		}
	}
	rep.Warnings = append(rep.Warnings, documentWarnings(snap, &v.Document)...)
	rep.Valid = len(rep.Errors) == 0
	b, _ := json.Marshal(rep)
	_, _ = s.db.W.ExecContext(ctx, `UPDATE policy_versions SET validation = ? WHERE id = ?`, string(b), v.ID)
	return rep, nil
}

func documentWarnings(s *Snapshot, d *PlanDocument) []FieldError {
	var out []FieldError
	if len(d.ModelMappings) == 0 && d.DefaultAliasID == "" {
		out = append(out, FieldError{Location: "body.document", Message: "no mappings and no default alias: only requests naming an alias directly will route"})
	}
	for i, m := range d.ModelMappings {
		if pid, ok := s.AliasPool[m.AliasID]; ok {
			if p := s.Pools[pid]; p != nil && len(p.Spec.Members) == 0 {
				out = append(out, FieldError{Location: fmt.Sprintf("body.document.model_mappings[%d]", i), Message: "target pool has no members"})
			}
		}
	}
	return out
}

// Publish makes a version the active one. The previous published version
// becomes superseded. rollbackOf is recorded for rollbacks.
func (s *Service) Publish(ctx context.Context, planID, versionID string, m Mutation) (*Version, error) {
	rep, err := s.ValidateVersion(ctx, planID, versionID)
	if err != nil {
		return nil, err
	}
	if !rep.Valid {
		return nil, &ValidationError{Errors: rep.Errors}
	}
	var planName string
	err = s.mutate(ctx, func(tx *sql.Tx, now time.Time, rev int64) (string, error) {
		plan, err := s.get(ctx, tx, PlanDef, planID)
		if err != nil {
			return "", err
		}
		planName = plan.Name
		var status string
		var version int64
		if err := tx.QueryRowContext(ctx, `SELECT status, version FROM policy_versions WHERE id = ? AND plan_id = ?`, versionID, plan.ID).Scan(&status, &version); err != nil {
			return "", ErrNotFound
		}
		if status != "draft" {
			return "", &ValidationError{Errors: []FieldError{{Location: "path.version_id", Message: "only drafts can be published; use rollback to restore an older version"}}}
		}
		var prevID sql.NullString
		_ = tx.QueryRowContext(ctx, `SELECT id FROM policy_versions WHERE plan_id = ? AND status = 'published'`, plan.ID).Scan(&prevID)
		if _, err := tx.ExecContext(ctx, `UPDATE policy_versions SET status = 'superseded' WHERE plan_id = ? AND status = 'published'`, plan.ID); err != nil {
			return "", err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE policy_versions SET status = 'published', published_by = ?, published_at = ? WHERE id = ?`,
			auth.FromContext(ctx).ID, now.UnixMilli(), versionID); err != nil {
			return "", err
		}
		if err := audit.Record(ctx, tx, audit.Entry{Action: "routing_plan.publish", TargetKind: KindPlan, TargetID: plan.ID,
			Details: map[string]any{"version_id": versionID, "version": version, "previous_version_id": prevID.String, "config_revision": rev}}); err != nil {
			return "", err
		}
		_, err = events.Append(ctx, tx, events.Event{Kind: events.Kind{Area: "policy", Entity: "plan", Action: "published"},
			Subjects: map[string]string{"plan_id": plan.ID, "version_id": versionID},
			Data:     map[string]any{"plan": plan.Name, "version": version, "previous_version_id": prevID.String, "config_revision": rev}})
		return fmt.Sprintf("publish plan %s v%d", plan.Name, version), err
	}, m)
	if err != nil {
		return nil, err
	}
	_ = planName
	return s.GetVersion(ctx, planID, versionID)
}

// Rollback creates a new version copying targetVersionID's document and
// publishes it, leaving history immutable.
func (s *Service) Rollback(ctx context.Context, planID, targetVersionID string, m Mutation) (*Version, error) {
	target, err := s.GetVersion(ctx, planID, targetVersionID)
	if err != nil {
		return nil, err
	}
	if target.Status == "draft" {
		return nil, &ValidationError{Errors: []FieldError{{Location: "body.version_id", Message: "roll back to a previously published version, not a draft"}}}
	}
	raw, _ := json.Marshal(target.Document)
	draft, err := s.SaveDraft(ctx, planID, "", raw, fmt.Sprintf("rollback to v%d", target.Version), m)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.W.ExecContext(ctx, `UPDATE policy_versions SET rollback_of = ? WHERE id = ?`, target.ID, draft.ID); err != nil {
		return nil, err
	}
	return s.Publish(ctx, planID, draft.ID, m)
}

// DiscardDraft deletes an unpublished draft.
func (s *Service) DiscardDraft(ctx context.Context, planID, versionID string, m Mutation) error {
	return s.mutate(ctx, func(tx *sql.Tx, now time.Time, rev int64) (string, error) {
		res, err := tx.ExecContext(ctx, `DELETE FROM policy_versions WHERE id = ? AND plan_id = ? AND status = 'draft'`, versionID, planID)
		if err != nil {
			return "", err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return "", ErrNotFound
		}
		return "discard draft", audit.Record(ctx, tx, audit.Entry{Action: "routing_plan.version.discard", TargetKind: KindPlan, TargetID: planID,
			Details: map[string]any{"version_id": versionID}})
	}, m)
}

// ResolveModel maps a requested model name to an alias using a plan's
// published document: exact alias name, then mappings in order, then the
// default alias. It returns the alias and how it matched.
func (s *Snapshot) ResolveModel(plan *PlanView, requested string) (*Res[AliasSpec], string, error) {
	if plan == nil || plan.Published == nil {
		return nil, "", errors.New("routing plan has no published version")
	}
	d := plan.Published.Document
	if a, ok := s.AliasByName[requested]; ok {
		return a, "alias_name", nil
	}
	for _, m := range d.ModelMappings {
		if ok, _ := path.Match(m.Match, requested); ok || m.Match == requested {
			return s.Aliases[m.AliasID], "mapping:" + m.Match, nil
		}
	}
	if d.DefaultAliasID != "" {
		return s.Aliases[d.DefaultAliasID], "default_alias", nil
	}
	return nil, "", fmt.Errorf("model %q is not mapped by plan %s", requested, plan.Name)
}

// MappedModelNames lists client-visible model names for a plan (aliases and
// exact mappings), used by GET /v1/models.
func (s *Snapshot) MappedModelNames(plan *PlanView) []string {
	set := map[string]bool{}
	for _, a := range s.Aliases {
		set[a.Name] = true
	}
	if plan != nil && plan.Published != nil {
		for _, m := range plan.Published.Document.ModelMappings {
			if ok, _ := path.Match(m.Match, "x"); !ok && !containsGlob(m.Match) {
				set[m.Match] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func containsGlob(s string) bool { return bytes.ContainsAny([]byte(s), "*?[") }
