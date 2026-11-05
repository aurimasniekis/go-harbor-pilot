package controlplane

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/audit"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	"github.com/aurimasniekis/go-harbor-pilot/internal/events"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/crypto"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/ids"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// Errors returned by the service.
var (
	ErrNotFound   = errors.New("not found")
	ErrNameTaken  = errors.New("an object of this kind with this name already exists")
	ErrReferenced = errors.New("object is referenced by other objects")
	ErrImmutable  = errors.New("field is immutable")
)

// ValidationError lists field problems.
type ValidationError struct{ Errors []FieldError }

func (e *ValidationError) Error() string {
	if len(e.Errors) == 0 {
		return "validation failed"
	}
	return "validation failed: " + e.Errors[0].Location + ": " + e.Errors[0].Message
}

// RevisionError reports an If-Match mismatch.
type RevisionError struct{ Current int64 }

func (e *RevisionError) Error() string {
	return fmt.Sprintf("revision mismatch (current %d)", e.Current)
}

// ReferencedError lists referrers that block a delete.
type ReferencedError struct{ By []string }

func (e *ReferencedError) Error() string {
	return ErrReferenced.Error() + ": " + strings.Join(e.By, ", ")
}
func (e *ReferencedError) Unwrap() error { return ErrReferenced }

// Managed-by sources.
const (
	ManagedUI        = "ui"
	ManagedAPI       = "api"
	ManagedTerraform = "terraform"
	ManagedCLI       = "cli"
	ManagedImport    = "import"
	ManagedDemo      = "demo"
)

// ValidManagedBy reports whether m is a known source.
func ValidManagedBy(m string) bool {
	switch m {
	case ManagedUI, ManagedAPI, ManagedTerraform, ManagedCLI, ManagedImport, ManagedDemo:
		return true
	}
	return false
}

// SecretInfo describes a stored write-only secret without revealing it.
type SecretInfo struct {
	Set         bool      `json:"set"`
	Source      string    `json:"source" enum:"stored,reference,none"`
	Fingerprint string    `json:"fingerprint,omitempty" doc:"Short non-reversible fingerprint to detect changes."`
	UpdatedAt   time.Time `json:"updated_at,omitzero"`
}

// Object is a stored resource.
type Object struct {
	ID             string
	Kind           string
	Name           string
	Spec           json.RawMessage
	Revision       int64
	ManagedBy      string
	DriftState     string
	DriftedAt      *time.Time
	DriftedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	CreatedBy      string
	UpdatedBy      string
	ConfigRevision int64
	Secrets        map[string]SecretInfo
}

// Mutation carries request-level options.
type Mutation struct {
	ManagedBy string // source of the change
	IfMatch   string // raw If-Match header (empty = unconditional)
	Summary   string // optional human summary for config revisions
	// DryRun validates and compiles the change, then rolls it back.
	DryRun bool
}

var errDryRun = errors.New("dry run")

// Options configure the service.
type Options struct {
	DB       *store.DB
	Keyring  *crypto.Keyring
	Keys     *store.ServerKeys
	Clock    clock.Clock
	Log      *slog.Logger
	Resolver *credentials.Resolver
}

// Service is the single mutation path for managed configuration.
type Service struct {
	o        Options
	db       *store.DB
	snap     atomic.Pointer[Snapshot]
	mu       sync.Mutex // serializes snapshot swaps
	onSwap   []func(*Snapshot)
	fpKey    []byte
	compiled func(ctx context.Context, q store.Execer, now time.Time) (*Snapshot, error)
}

// New creates the service and compiles the initial snapshot.
func New(ctx context.Context, o Options) (*Service, error) {
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Resolver == nil {
		o.Resolver = credentials.NewResolver(nil)
	}
	fp, err := o.Keys.Get(ctx, store.KeyTokenPepper)
	if err != nil {
		return nil, err
	}
	s := &Service{o: o, db: o.DB, fpKey: fp, compiled: Compile}
	snap, err := Compile(ctx, o.DB.R, o.Clock.Now())
	if err != nil {
		// The stored configuration is always validated before commit, so this
		// only happens after a downgrade or manual tampering. Refuse to start
		// rather than serve an unknown configuration.
		return nil, fmt.Errorf("compile stored configuration: %w", err)
	}
	s.snap.Store(snap)
	return s, nil
}

// Snapshot returns the active immutable configuration snapshot.
func (s *Service) Snapshot() *Snapshot { return s.snap.Load() }

// OnSwap registers a callback invoked after each snapshot swap.
func (s *Service) OnSwap(fn func(*Snapshot)) { s.onSwap = append(s.onSwap, fn) }

func (s *Service) swap(snap *Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur := s.snap.Load(); cur != nil && cur.ConfigRevision > snap.ConfigRevision {
		return
	}
	s.snap.Store(snap)
	for _, fn := range s.onSwap {
		fn(snap)
	}
}

// Reload recompiles the snapshot from the database (used after imports and
// out-of-process CLI changes).
func (s *Service) Reload(ctx context.Context) error {
	snap, err := s.compiled(ctx, s.db.R, s.o.Clock.Now())
	if err != nil {
		return err
	}
	s.swap(snap)
	return nil
}

// ---------- reads ----------

const objCols = `id, kind, name, spec, revision, managed_by, drift_state, drifted_at, drifted_by, created_at, updated_at,
	created_by, updated_by, config_revision`

func scanObject(r interface{ Scan(...any) error }) (*Object, error) {
	var o Object
	var spec string
	var driftedAt sql.NullInt64
	var driftedBy sql.NullString
	var created, updated int64
	if err := r.Scan(&o.ID, &o.Kind, &o.Name, &spec, &o.Revision, &o.ManagedBy, &o.DriftState, &driftedAt, &driftedBy,
		&created, &updated, &o.CreatedBy, &o.UpdatedBy, &o.ConfigRevision); err != nil {
		return nil, err
	}
	o.Spec = json.RawMessage(spec)
	if driftedAt.Valid {
		t := time.UnixMilli(driftedAt.Int64).UTC()
		o.DriftedAt = &t
	}
	o.DriftedBy = driftedBy.String
	o.CreatedAt, o.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
	return &o, nil
}

// Get loads an object of kind by id or name.
func (s *Service) Get(ctx context.Context, kind Kind, idOrName string) (*Object, error) {
	return s.get(ctx, s.db.R, kind, idOrName)
}

func (s *Service) get(ctx context.Context, q store.Execer, kind Kind, idOrName string) (*Object, error) {
	o, err := scanObject(q.QueryRowContext(ctx, `SELECT `+objCols+` FROM resources WHERE kind = ? AND (id = ? OR name = ?)`,
		kind.KindName(), idOrName, idOrName))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := s.loadSecrets(ctx, q, kind, o); err != nil {
		return nil, err
	}
	return o, nil
}

func (s *Service) loadSecrets(ctx context.Context, q store.Execer, kind Kind, o *Object) error {
	if len(kind.SecretFields()) == 0 {
		return nil
	}
	o.Secrets = map[string]SecretInfo{}
	stored, err := store.Query(ctx, q, func(r *sql.Rows) ([3]any, error) {
		var f, fp string
		var at int64
		err := r.Scan(&f, &fp, &at)
		return [3]any{f, fp, at}, err
	}, `SELECT field, fingerprint, updated_at FROM resource_secrets WHERE resource_id = ?`, o.ID)
	if err != nil {
		return err
	}
	var spec map[string]any
	_ = json.Unmarshal(o.Spec, &spec)
	for _, sf := range kind.SecretFields() {
		info := SecretInfo{Source: "none"}
		if sf.RefField != "" {
			if ref, _ := spec[sf.RefField].(string); ref != "" {
				info = SecretInfo{Set: true, Source: "reference"}
			}
		}
		for _, row := range stored {
			if row[0].(string) == sf.Name {
				info = SecretInfo{Set: true, Source: "stored", Fingerprint: row[1].(string), UpdatedAt: time.UnixMilli(row[2].(int64)).UTC()}
			}
		}
		o.Secrets[sf.Name] = info
	}
	return nil
}

// List lists objects of a kind ordered by id after cursor.
func (s *Service) List(ctx context.Context, kind Kind, after string, limit int) ([]*Object, error) {
	objs, err := store.Query(ctx, s.db.R, func(r *sql.Rows) (*Object, error) { return scanObject(r) },
		`SELECT `+objCols+` FROM resources WHERE kind = ? AND id > ? ORDER BY id LIMIT ?`, kind.KindName(), after, limit)
	if err != nil {
		return nil, err
	}
	for _, o := range objs {
		if err := s.loadSecrets(ctx, s.db.R, kind, o); err != nil {
			return nil, err
		}
	}
	return objs, nil
}

// History returns revisions of a resource (newest first).
func (s *Service) History(ctx context.Context, id string, limit int) ([]HistoryEntry, error) {
	return store.Query(ctx, s.db.R, func(r *sql.Rows) (HistoryEntry, error) {
		var h HistoryEntry
		var spec sql.NullString
		var at int64
		err := r.Scan(&h.Revision, &spec, &h.Operation, &h.ManagedBy, &h.ActorID, &at, &h.ConfigRevision)
		if spec.Valid {
			h.Spec = json.RawMessage(spec.String)
		}
		h.At = time.UnixMilli(at).UTC()
		return h, err
	}, `SELECT revision, spec, operation, managed_by, actor_id, at, config_revision FROM resource_history
		WHERE resource_id = ? ORDER BY revision DESC LIMIT ?`, id, limit)
}

// HistoryEntry is one resource revision.
type HistoryEntry struct {
	Revision       int64           `json:"revision"`
	Spec           json.RawMessage `json:"spec,omitempty"`
	Operation      string          `json:"operation"`
	ManagedBy      string          `json:"managed_by"`
	ActorID        string          `json:"actor_id"`
	At             time.Time       `json:"at"`
	ConfigRevision int64           `json:"config_revision"`
}

// ---------- writes ----------

// SecretInput is nil to keep the current value, "" to clear, or a value.
type SecretInput = map[string]*string

func (s *Service) lookupFn(ctx context.Context, q store.Execer) func(string) (string, bool) {
	return func(id string) (string, bool) {
		var k string
		if err := q.QueryRowContext(ctx, `SELECT kind FROM resources WHERE id = ?`, id).Scan(&k); err != nil {
			return "", false
		}
		return k, true
	}
}

// Create inserts a new object.
func (s *Service) Create(ctx context.Context, kind Kind, name string, spec json.RawMessage, secrets SecretInput, m Mutation) (*Object, error) {
	if m.ManagedBy == "" {
		m.ManagedBy = ManagedAPI
	}
	var id string
	err := s.mutate(ctx, func(tx *sql.Tx, now time.Time, rev int64) (string, error) {
		var err error
		id, err = s.CreateTx(ctx, tx, now, rev, kind, name, spec, secrets, m)
		return fmt.Sprintf("create %s %s", kind.KindName(), name), err
	}, m)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, kind, id)
}

// CreateTx inserts an object inside an existing mutation transaction.
func (s *Service) CreateTx(ctx context.Context, tx *sql.Tx, now time.Time, rev int64, kind Kind, name string, spec json.RawMessage, secrets SecretInput, m Mutation) (string, error) {
	if !kind.ValidName(name) {
		return "", &ValidationError{Errors: []FieldError{{Location: "body.name", Message: "name must be " + kind.NameRule()}}}
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM resources WHERE kind = ? AND name = ?`, kind.KindName(), name).Scan(&exists); err != nil {
		return "", err
	}
	if exists > 0 {
		return "", ErrNameTaken
	}
	id := ids.New(kind.KindPrefix())
	v := NewValidator("", s.lookupFn(ctx, tx))
	canon, refs := kind.Canonicalize(spec, v)
	s.checkSecretInputs(v, kind, canon, secrets, true)
	if !v.OK() {
		return "", &ValidationError{Errors: v.Errors}
	}
	actor := auth.FromContext(ctx).ID
	if _, err := tx.ExecContext(ctx, `INSERT INTO resources(id, kind, name, spec, revision, managed_by, created_at, updated_at,
		created_by, updated_by, config_revision) VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?)`,
		id, kind.KindName(), name, string(canon), m.ManagedBy, now.UnixMilli(), now.UnixMilli(), actor, actor, rev); err != nil {
		return "", err
	}
	if err := s.writeRefs(ctx, tx, id, refs); err != nil {
		return "", err
	}
	if err := s.writeSecrets(ctx, tx, id, kind, secrets, now); err != nil {
		return "", err
	}
	if err := s.history(ctx, tx, id, 1, kind.KindName(), name, canon, "create", m.ManagedBy, now, rev); err != nil {
		return "", err
	}
	return id, s.record(ctx, tx, "create", kind, id, name, m.ManagedBy, rev)
}

// Replace overwrites the spec of an existing object. Names are immutable.
func (s *Service) Replace(ctx context.Context, kind Kind, idOrName, name string, spec json.RawMessage, secrets SecretInput, m Mutation) (*Object, error) {
	if m.ManagedBy == "" {
		m.ManagedBy = ManagedAPI
	}
	var id string
	err := s.mutate(ctx, func(tx *sql.Tx, now time.Time, rev int64) (string, error) {
		var err error
		var n string
		id, n, err = s.ReplaceTx(ctx, tx, now, rev, kind, idOrName, name, spec, secrets, m)
		return fmt.Sprintf("update %s %s", kind.KindName(), n), err
	}, m)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, kind, id)
}

// ReplaceTx replaces an object inside an existing mutation transaction.
func (s *Service) ReplaceTx(ctx context.Context, tx *sql.Tx, now time.Time, rev int64, kind Kind, idOrName, name string, spec json.RawMessage, secrets SecretInput, m Mutation) (string, string, error) {
	cur, err := s.get(ctx, tx, kind, idOrName)
	if err != nil {
		return "", "", err
	}
	if err := checkIfMatch(m.IfMatch, cur.Revision); err != nil {
		return "", "", err
	}
	if name != "" && name != cur.Name {
		return "", "", &ValidationError{Errors: []FieldError{{Location: "body.name", Message: "name is immutable; create a new object instead"}}}
	}
	v := NewValidator(cur.ID, s.lookupFn(ctx, tx))
	canon, refs := kind.Canonicalize(spec, v)
	s.checkSecretInputs(v, kind, canon, secrets, false)
	if !v.OK() {
		return "", "", &ValidationError{Errors: v.Errors}
	}
	if err := s.guardDestinationChange(ctx, tx, kind, cur, canon, secrets); err != nil {
		return "", "", err
	}
	managedBy, drift, driftedAt, driftedBy := s.driftAfter(ctx, cur, m.ManagedBy, now)
	newRev := cur.Revision + 1
	if _, err := tx.ExecContext(ctx, `UPDATE resources SET spec = ?, revision = ?, managed_by = ?, drift_state = ?, drifted_at = ?,
		drifted_by = ?, updated_at = ?, updated_by = ?, config_revision = ? WHERE id = ?`,
		string(canon), newRev, managedBy, drift, driftedAt, driftedBy, now.UnixMilli(), auth.FromContext(ctx).ID, rev, cur.ID); err != nil {
		return "", "", err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM resource_refs WHERE from_id = ?`, cur.ID); err != nil {
		return "", "", err
	}
	if err := s.writeRefs(ctx, tx, cur.ID, refs); err != nil {
		return "", "", err
	}
	if err := s.writeSecrets(ctx, tx, cur.ID, kind, secrets, now); err != nil {
		return "", "", err
	}
	if err := s.history(ctx, tx, cur.ID, newRev, kind.KindName(), cur.Name, canon, "update", m.ManagedBy, now, rev); err != nil {
		return "", "", err
	}
	return cur.ID, cur.Name, s.record(ctx, tx, "update", kind, cur.ID, cur.Name, m.ManagedBy, rev)
}

// driftAfter computes ownership and drift after a change by source.
// Terraform-managed objects edited by any other source become "drifted" and
// stay Terraform-managed so the next plan shows the difference; a Terraform
// apply returns them to "in_sync".
func (s *Service) driftAfter(ctx context.Context, cur *Object, source string, now time.Time) (managedBy, drift string, driftedAt, driftedBy any) {
	if source == ManagedTerraform {
		return ManagedTerraform, "in_sync", nil, nil
	}
	if cur.ManagedBy == ManagedTerraform {
		return ManagedTerraform, "drifted", now.UnixMilli(), auth.FromContext(ctx).ID
	}
	return source, "in_sync", nil, nil
}

// Delete removes an object unless other objects reference it.
func (s *Service) Delete(ctx context.Context, kind Kind, idOrName string, m Mutation) error {
	if m.ManagedBy == "" {
		m.ManagedBy = ManagedAPI
	}
	return s.mutate(ctx, func(tx *sql.Tx, now time.Time, rev int64) (string, error) {
		n, err := s.DeleteTx(ctx, tx, now, rev, kind, idOrName, m)
		return fmt.Sprintf("delete %s %s", kind.KindName(), n), err
	}, m)
}

// DeleteTx deletes an object inside an existing mutation transaction.
func (s *Service) DeleteTx(ctx context.Context, tx *sql.Tx, now time.Time, rev int64, kind Kind, idOrName string, m Mutation) (string, error) {
	cur, err := s.get(ctx, tx, kind, idOrName)
	if err != nil {
		return "", err
	}
	if err := checkIfMatch(m.IfMatch, cur.Revision); err != nil {
		return "", err
	}
	by, err := store.Query(ctx, tx, func(r *sql.Rows) (string, error) {
		var k, n string
		err := r.Scan(&k, &n)
		return k + " " + n, err
	}, `SELECT DISTINCT r.kind, r.name FROM resource_refs x JOIN resources r ON r.id = x.from_id WHERE x.to_id = ? ORDER BY r.kind, r.name LIMIT 20`, cur.ID)
	if err != nil {
		return "", err
	}
	// Published plan documents reference aliases, pools and policies by id
	// without resource_refs rows; ids are unique prefixed ULIDs, so a text
	// match is exact.
	plans, err := store.Query(ctx, tx, func(r *sql.Rows) (string, error) {
		var n string
		err := r.Scan(&n)
		return "routing_plan " + n, err
	}, `SELECT DISTINCT p.name FROM policy_versions v JOIN resources p ON p.id = v.plan_id
		WHERE v.status = 'published' AND instr(v.document, ?) > 0 ORDER BY p.name LIMIT 20`, `"`+cur.ID+`"`)
	if err != nil {
		return "", err
	}
	by = append(by, plans...)
	if len(by) > 0 {
		return "", &ReferencedError{By: by}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM resources WHERE id = ?`, cur.ID); err != nil {
		return "", err
	}
	if err := s.history(ctx, tx, cur.ID, cur.Revision+1, kind.KindName(), cur.Name, nil, "delete", m.ManagedBy, now, rev); err != nil {
		return "", err
	}
	return cur.Name, s.record(ctx, tx, "delete", kind, cur.ID, cur.Name, m.ManagedBy, rev)
}

// mutate runs fn in a write transaction with a new config revision, then
// compiles the would-be snapshot inside the same transaction. Any compile
// error rolls the change back, so committed configuration always compiles.
func (s *Service) mutate(ctx context.Context, fn func(tx *sql.Tx, now time.Time, rev int64) (string, error), m Mutation) error {
	now := s.o.Clock.Now()
	var snap *Snapshot
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		p := auth.FromContext(ctx)
		var rev int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO config_revisions(created_at, actor_id, actor_kind, source, summary, request_id)
			VALUES (?, ?, ?, ?, '', ?) RETURNING revision`, now.UnixMilli(), p.ID, string(p.Kind), m.ManagedBy, nil).Scan(&rev); err != nil {
			return err
		}
		summary, err := fn(tx, now, rev)
		if err != nil {
			return err
		}
		if m.Summary != "" {
			summary = m.Summary
		}
		if _, err := tx.ExecContext(ctx, `UPDATE config_revisions SET summary = ? WHERE revision = ?`, summary, rev); err != nil {
			return err
		}
		snap, err = s.compiled(ctx, tx, now)
		if err != nil {
			var ve *ValidationError
			if errors.As(err, &ve) {
				return err
			}
			return &ValidationError{Errors: []FieldError{{Location: "body", Message: err.Error()}}}
		}
		if m.DryRun {
			return errDryRun
		}
		return nil
	})
	if errors.Is(err, errDryRun) {
		return nil
	}
	if err != nil {
		return err
	}
	s.swap(snap)
	return nil
}

// Mutate runs a custom change (plan versions, credentials) through the same
// transaction, revision, compile and swap path.
func (s *Service) Mutate(ctx context.Context, m Mutation, fn func(tx *sql.Tx, now time.Time, rev int64) (string, error)) error {
	return s.mutate(ctx, fn, m)
}

func checkIfMatch(header string, current int64) error {
	if header == "" || header == "*" {
		return nil
	}
	h := strings.TrimPrefix(strings.TrimSpace(header), "W/")
	if h != fmt.Sprintf(`"r%d"`, current) {
		return &RevisionError{Current: current}
	}
	return nil
}

func (s *Service) writeRefs(ctx context.Context, tx *sql.Tx, id string, refs []Ref) error {
	for _, r := range refs {
		if r.ID == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO resource_refs(from_id, to_id, field) VALUES (?, ?, ?)`, id, r.ID, r.Field); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) checkSecretInputs(v *Validator, kind Kind, canon json.RawMessage, in SecretInput, create bool) {
	declared := map[string]SecretField{}
	for _, sf := range kind.SecretFields() {
		declared[sf.Name] = sf
	}
	for name := range in {
		if _, ok := declared[name]; !ok {
			v.Fail("secrets."+name, "unknown secret field for this kind")
		}
	}
	var spec map[string]any
	_ = json.Unmarshal(canon, &spec)
	for _, sf := range kind.SecretFields() {
		val := in[sf.Name]
		if val != nil && len(*val) > 64<<10 {
			v.Fail("secrets."+sf.Name, "secret too large (max 64 KiB)")
		}
		if sf.RefField != "" {
			if ref, _ := spec[sf.RefField].(string); ref != "" && val != nil && *val != "" {
				v.Fail("secrets."+sf.Name, "set either the stored secret or spec."+sf.RefField+", not both")
			}
		}
	}
	_ = create
}

func (s *Service) writeSecrets(ctx context.Context, tx *sql.Tx, id string, kind Kind, in SecretInput, now time.Time) error {
	var spec map[string]any
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT spec FROM resources WHERE id = ?`, id).Scan(&raw); err == nil {
		_ = json.Unmarshal([]byte(raw), &spec)
	}
	for _, sf := range kind.SecretFields() {
		val, provided := in[sf.Name]
		// A secret reference replaces any stored value.
		if sf.RefField != "" {
			if ref, _ := spec[sf.RefField].(string); ref != "" {
				if _, err := tx.ExecContext(ctx, `DELETE FROM resource_secrets WHERE resource_id = ? AND field = ?`, id, sf.Name); err != nil {
					return err
				}
				continue
			}
		}
		if !provided || val == nil {
			continue
		}
		if *val == "" {
			if _, err := tx.ExecContext(ctx, `DELETE FROM resource_secrets WHERE resource_id = ? AND field = ?`, id, sf.Name); err != nil {
				return err
			}
			continue
		}
		env, err := s.o.Keyring.Seal([]byte(*val), SecretAAD(id, sf.Name))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO resource_secrets(resource_id, field, envelope, fingerprint, updated_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(resource_id, field) DO UPDATE SET envelope = excluded.envelope, fingerprint = excluded.fingerprint, updated_at = excluded.updated_at`,
			id, sf.Name, env, s.fingerprint(*val), now.UnixMilli()); err != nil {
			return err
		}
	}
	return nil
}

// destinationFields are the spec fields (dotted paths) that decide where a
// kind's stored secrets are sent.
var destinationFields = map[string][]string{
	KindChannel:      {"webhook_url", "api_base_url"},
	KindDestination:  {"webhook_url", "pulse_url"},
	KindQuotaMonitor: {"options.base_url", "options.url"},
	KindProvider:     {"base_url"},
}

func specField(raw json.RawMessage, path string) string {
	var cur any
	if json.Unmarshal(raw, &cur) != nil {
		return ""
	}
	for _, k := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[k]
	}
	v, _ := cur.(string)
	return v
}

// origin reduces a URL to scheme://host:port (lower case); "" when unset.
func origin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return strings.ToLower(strings.TrimSpace(raw))
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443", "nats": "4222", "tls": "4222"}[strings.ToLower(u.Scheme)]
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Hostname()) + ":" + port
}

// guardDestinationChange stops a stored secret from silently following a
// destination change: whoever points an object somewhere new must supply
// its secrets again. For providers the secrets live on their accounts, so
// those stored credentials are cleared instead and must be re-entered.
func (s *Service) guardDestinationChange(ctx context.Context, tx *sql.Tx, kind Kind, cur *Object, canon json.RawMessage, in SecretInput) error {
	var changed []string
	for _, f := range destinationFields[kind.KindName()] {
		if origin(specField(cur.Spec, f)) != origin(specField(canon, f)) {
			changed = append(changed, f)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	if kind.KindName() == KindProvider {
		res, err := tx.ExecContext(ctx, `DELETE FROM resource_secrets WHERE field = 'credential' AND resource_id IN
			(SELECT id FROM resources WHERE kind = ? AND json_extract(spec, '$.provider_id') = ?)`, KindAccount, cur.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return audit.Record(ctx, tx, audit.Entry{Action: "provider.credentials_cleared", TargetKind: KindProvider, TargetID: cur.ID,
				Details: map[string]any{"reason": "base_url origin changed", "accounts": n}})
		}
		return nil
	}
	stored, err := store.Query(ctx, tx, func(r *sql.Rows) (string, error) {
		var f string
		err := r.Scan(&f)
		return f, err
	}, `SELECT field FROM resource_secrets WHERE resource_id = ?`, cur.ID)
	if err != nil {
		return err
	}
	var errs []FieldError
	for _, f := range stored {
		if v, ok := in[f]; !ok || v == nil {
			errs = append(errs, FieldError{Location: "body.secrets." + f,
				Message: "re-enter this secret when changing " + strings.Join(changed, ", ") + "; stored secrets are not sent to a new destination"})
		}
	}
	if len(errs) > 0 {
		return &ValidationError{Errors: errs}
	}
	return nil
}

// SecretAAD is the associated data binding a sealed secret to its row.
func SecretAAD(id, field string) string { return "resource-secret|" + id + "|" + field }

func (s *Service) fingerprint(v string) string {
	m := hmac.New(sha256.New, s.fpKey)
	m.Write([]byte("harbor-pilot/secret-fingerprint/v1|" + v))
	return hex.EncodeToString(m.Sum(nil))[:12]
}

// ResolveSecret returns a resource's secret value from its stored envelope
// or its configured reference. The value never leaves process memory.
func (s *Service) ResolveSecret(ctx context.Context, kind Kind, id, field string) (credentials.Secret, error) {
	var env string
	err := s.db.R.QueryRowContext(ctx, `SELECT envelope FROM resource_secrets WHERE resource_id = ? AND field = ?`, id, field).Scan(&env)
	if err == nil {
		b, err := s.o.Keyring.Open(env, SecretAAD(id, field))
		if err != nil {
			return credentials.Secret{}, fmt.Errorf("secret %s of %s cannot be decrypted with the configured master key", field, id)
		}
		return credentials.NewSecret(string(b)), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return credentials.Secret{}, err
	}
	o, err := s.get(ctx, s.db.R, kind, id)
	if err != nil {
		return credentials.Secret{}, err
	}
	var spec map[string]any
	_ = json.Unmarshal(o.Spec, &spec)
	for _, sf := range kind.SecretFields() {
		if sf.Name != field || sf.RefField == "" {
			continue
		}
		ref, _ := spec[sf.RefField].(string)
		if ref == "" {
			break
		}
		r, err := auth.ParseSecretRef(ref)
		if err != nil {
			return credentials.Secret{}, err
		}
		return s.o.Resolver.Resolve(r)
	}
	return credentials.Secret{}, fmt.Errorf("%s has no %s configured", id, field)
}

func (s *Service) history(ctx context.Context, tx *sql.Tx, id string, rev int64, kind, name string, spec json.RawMessage, op, by string, now time.Time, cfgRev int64) error {
	var sp any
	if spec != nil {
		sp = string(spec)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO resource_history(resource_id, revision, kind, name, spec, operation, managed_by, actor_id, at, config_revision)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, rev, kind, name, sp, op, by, auth.FromContext(ctx).ID, now.UnixMilli(), cfgRev)
	return err
}

func (s *Service) record(ctx context.Context, tx *sql.Tx, op string, kind Kind, id, name, by string, rev int64) error {
	if err := audit.Record(ctx, tx, audit.Entry{Action: kind.KindName() + "." + op, TargetKind: kind.KindName(), TargetID: id,
		Details: map[string]any{"name": name, "managed_by": by, "config_revision": rev}}); err != nil {
		return err
	}
	action := map[string]string{"create": "created", "update": "updated", "delete": "deleted"}[op]
	_, err := events.Append(ctx, tx, events.Event{
		Kind:     events.Kind{Area: "config", Entity: kind.KindName(), Action: action},
		Subjects: map[string]string{"resource_id": id},
		Data:     map[string]any{"name": name, "managed_by": by, "config_revision": rev},
	})
	return err
}

// DB exposes the store for read-only reporting queries.
func (s *Service) DB() *store.DB { return s.db }
