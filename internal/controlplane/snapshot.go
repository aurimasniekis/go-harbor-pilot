package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// Res is a typed, immutable view of a stored resource.
type Res[S any] struct {
	ID        string
	Name      string
	Spec      S
	Revision  int64
	ManagedBy string
}

// PlanView is a routing plan with its published version (if any).
type PlanView struct {
	Res[PlanSpec]
	Published *VersionView
}

// VersionView is a compiled published plan version.
type VersionView struct {
	ID       string
	Version  int64
	Document PlanDocument
	// Compiled holds engine-specific compiled artifacts (Expr programs,
	// decision tables) attached by the routing engine at compile time.
	Compiled any
}

// Snapshot is the immutable compiled configuration used by the gateway.
// It is swapped atomically after every committed change.
type Snapshot struct {
	ConfigRevision int64
	CompiledAt     time.Time

	Workspaces   map[string]*Res[WorkspaceSpec]
	Projects     map[string]*Res[ProjectSpec]
	Providers    map[string]*Res[ProviderSpec]
	Accounts     map[string]*Res[AccountSpec]
	Entitlements map[string]*Res[EntitlementSpec]
	Pools        map[string]*Res[PoolSpec]
	Aliases      map[string]*Res[AliasSpec]
	AliasByName  map[string]*Res[AliasSpec]
	Plans        map[string]*PlanView
	Bindings     map[string]*Res[BindingSpec]
	Monitors     map[string]*Res[QuotaMonitorSpec]
	Budgets      map[string]*Res[BudgetSpec]
	Privacy      map[string]*Res[PrivacyPolicySpec]
	TracePols    map[string]*Res[TracePolicySpec]

	// AliasPool maps alias id to the pool id it ultimately resolves to.
	AliasPool map[string]string
	// AliasChain maps alias id to the chain of alias ids followed.
	AliasChain map[string][]string

	// Ext holds views of kinds registered by feature packages, keyed by kind.
	Ext map[string]any
}

// PlanCompiler lets the routing engine compile plan documents (Expr
// programs, decision tables) as part of snapshot compilation. Errors reject
// the configuration change.
var PlanCompiler func(s *Snapshot, p *PlanView) (any, error)

// ModelOf returns a provider's declared model, if listed.
func (s *Snapshot) ModelOf(providerID, model string) (*ModelSpec, bool) {
	p, ok := s.Providers[providerID]
	if !ok {
		return nil, false
	}
	for i := range p.Spec.Models {
		if p.Spec.Models[i].ID == model {
			return &p.Spec.Models[i], true
		}
	}
	return nil, false
}

// Compile loads every resource and published plan version and builds a
// snapshot, enforcing cross-object invariants.
func Compile(ctx context.Context, q store.Execer, now time.Time) (*Snapshot, error) {
	s := &Snapshot{CompiledAt: now,
		Workspaces: map[string]*Res[WorkspaceSpec]{}, Projects: map[string]*Res[ProjectSpec]{}, Providers: map[string]*Res[ProviderSpec]{},
		Accounts: map[string]*Res[AccountSpec]{}, Entitlements: map[string]*Res[EntitlementSpec]{}, Pools: map[string]*Res[PoolSpec]{},
		Aliases: map[string]*Res[AliasSpec]{}, AliasByName: map[string]*Res[AliasSpec]{}, Plans: map[string]*PlanView{},
		Bindings: map[string]*Res[BindingSpec]{}, AliasPool: map[string]string{}, AliasChain: map[string][]string{}, Ext: map[string]any{},
		Monitors: map[string]*Res[QuotaMonitorSpec]{}, Budgets: map[string]*Res[BudgetSpec]{},
		Privacy: map[string]*Res[PrivacyPolicySpec]{}, TracePols: map[string]*Res[TracePolicySpec]{}}
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision), 0) FROM config_revisions`).Scan(&s.ConfigRevision); err != nil {
		return nil, err
	}
	type row struct {
		id, kind, name, spec, managedBy string
		rev                             int64
	}
	rows, err := store.Query(ctx, q, func(r *sql.Rows) (row, error) {
		var x row
		err := r.Scan(&x.id, &x.kind, &x.name, &x.spec, &x.rev, &x.managedBy)
		return x, err
	}, `SELECT id, kind, name, spec, revision, managed_by FROM resources ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for _, x := range rows {
		raw := json.RawMessage(x.spec)
		var derr error
		switch x.kind {
		case KindWorkspace:
			derr = put(s.Workspaces, x.id, x.name, x.rev, x.managedBy, raw)
		case KindProject:
			derr = put(s.Projects, x.id, x.name, x.rev, x.managedBy, raw)
		case KindProvider:
			derr = put(s.Providers, x.id, x.name, x.rev, x.managedBy, raw)
		case KindAccount:
			derr = put(s.Accounts, x.id, x.name, x.rev, x.managedBy, raw)
		case KindEntitlement:
			derr = put(s.Entitlements, x.id, x.name, x.rev, x.managedBy, raw)
		case KindPool:
			derr = put(s.Pools, x.id, x.name, x.rev, x.managedBy, raw)
		case KindAlias:
			derr = put(s.Aliases, x.id, x.name, x.rev, x.managedBy, raw)
		case KindPlan:
			var spec PlanSpec
			derr = json.Unmarshal(raw, &spec)
			s.Plans[x.id] = &PlanView{Res: Res[PlanSpec]{ID: x.id, Name: x.name, Spec: spec, Revision: x.rev, ManagedBy: x.managedBy}}
		case KindClientBinding:
			derr = put(s.Bindings, x.id, x.name, x.rev, x.managedBy, raw)
		case KindQuotaMonitor:
			derr = put(s.Monitors, x.id, x.name, x.rev, x.managedBy, raw)
		case KindBudget:
			derr = put(s.Budgets, x.id, x.name, x.rev, x.managedBy, raw)
		case KindPrivacyPolicy:
			derr = put(s.Privacy, x.id, x.name, x.rev, x.managedBy, raw)
		case KindTracePolicy:
			derr = put(s.TracePols, x.id, x.name, x.rev, x.managedBy, raw)
		default:
			if h, ok := extraKinds[x.kind]; ok {
				derr = h(s, x.id, x.name, x.rev, x.managedBy, raw)
			}
		}
		if derr != nil {
			return nil, fmt.Errorf("%s %s: stored spec is unreadable: %w", x.kind, x.name, derr)
		}
	}
	for _, a := range s.Aliases {
		s.AliasByName[a.Name] = a
	}
	var problems []FieldError
	fail := func(loc, msg string) { problems = append(problems, FieldError{Location: loc, Message: msg}) }

	// Alias resolution and cycle detection (deterministic order; each cycle
	// is reported once).
	aliasIDs := make([]string, 0, len(s.Aliases))
	for id := range s.Aliases {
		aliasIDs = append(aliasIDs, id)
	}
	sort.Strings(aliasIDs)
	reported := map[string]bool{}
	for _, id := range aliasIDs {
		chain := []string{id}
		cur := s.Aliases[id]
		for cur.Spec.AliasID != "" {
			if i := slices.Index(chain, cur.Spec.AliasID); i >= 0 {
				cycle := chain[i:]
				key := slices.Clone(cycle)
				sort.Strings(key)
				if !reported[strings.Join(key, ",")] {
					reported[strings.Join(key, ",")] = true
					names := make([]string, 0, len(cycle)+1)
					for _, c := range append(slices.Clone(cycle), cur.Spec.AliasID) {
						names = append(names, s.Aliases[c].Name)
					}
					fail("body.spec.alias_id", "alias cycle: "+strings.Join(names, " -> "))
				}
				break
			}
			next, ok := s.Aliases[cur.Spec.AliasID]
			if !ok {
				fail("body.spec.alias_id", "alias "+cur.Name+" targets a missing alias")
				break
			}
			chain = append(chain, next.ID)
			cur = next
			if len(chain) > 8 {
				fail("body.spec.alias_id", "alias chain longer than 8 starting at "+s.Aliases[id].Name)
				break
			}
		}
		s.AliasChain[id] = chain
		if cur.Spec.PoolID != "" {
			s.AliasPool[id] = cur.Spec.PoolID
		}
	}

	// Pool members must reference models the account may serve.
	poolIDs := make([]string, 0, len(s.Pools))
	for id := range s.Pools {
		poolIDs = append(poolIDs, id)
	}
	sort.Strings(poolIDs)
	for _, pid := range poolIDs {
		p := s.Pools[pid]
		for i, m := range p.Spec.Members {
			acc, ok := s.Accounts[m.AccountID]
			if !ok {
				fail(fmt.Sprintf("body.spec.members[%d].account_id", i), "pool "+p.Name+" references a missing account")
				continue
			}
			prov := s.Providers[acc.Spec.ProviderID]
			if prov != nil && len(prov.Spec.Models) > 0 {
				if _, ok := s.ModelOf(prov.ID, m.Model); !ok {
					fail(fmt.Sprintf("body.spec.members[%d].model", i), fmt.Sprintf("pool %s: model %q is not declared by provider %s", p.Name, m.Model, prov.Name))
				}
			}
			if len(acc.Spec.AllowedModels) > 0 && !slices.Contains(acc.Spec.AllowedModels, m.Model) {
				fail(fmt.Sprintf("body.spec.members[%d].model", i), fmt.Sprintf("pool %s: account %s is not allowed to use model %q", p.Name, acc.Name, m.Model))
			}
		}
	}

	// Published plan versions.
	type ver struct {
		id, planID, doc string
		version         int64
	}
	vers, err := store.Query(ctx, q, func(r *sql.Rows) (ver, error) {
		var v ver
		err := r.Scan(&v.id, &v.planID, &v.version, &v.doc)
		return v, err
	}, `SELECT id, plan_id, version, document FROM policy_versions WHERE status = 'published'`)
	if err != nil {
		return nil, err
	}
	for _, v := range vers {
		p, ok := s.Plans[v.planID]
		if !ok {
			continue
		}
		var doc PlanDocument
		if err := json.Unmarshal([]byte(v.doc), &doc); err != nil {
			return nil, fmt.Errorf("plan %s v%d: stored document unreadable: %w", p.Name, v.version, err)
		}
		vv := &VersionView{ID: v.id, Version: v.version, Document: doc}
		for _, fe := range ValidateDocumentRefs(s, &doc) {
			fail(fe.Location, fmt.Sprintf("published plan %s v%d: %s", p.Name, v.version, fe.Message))
		}
		p.Published = vv
		if PlanCompiler != nil {
			c, err := PlanCompiler(s, p)
			if err != nil {
				fail("body", fmt.Sprintf("published plan %s v%d: %v", p.Name, v.version, err))
			}
			vv.Compiled = c
		}
	}
	for _, fn := range extraChecks {
		for _, fe := range fn(s) {
			fail(fe.Location, fe.Message)
		}
	}
	if len(problems) > 0 {
		return nil, &ValidationError{Errors: problems}
	}
	return s, nil
}

func put[S any](m map[string]*Res[S], id, name string, rev int64, mb string, raw json.RawMessage) error {
	var spec S
	if err := json.Unmarshal(raw, &spec); err != nil {
		return err
	}
	m[id] = &Res[S]{ID: id, Name: name, Spec: spec, Revision: rev, ManagedBy: mb}
	return nil
}

// Extension points for kinds registered by feature packages.
var (
	extraKinds  = map[string]func(s *Snapshot, id, name string, rev int64, mb string, raw json.RawMessage) error{}
	extraChecks []func(s *Snapshot) []FieldError
)

// RegisterSnapshotKind lets a feature package load its kind into the
// snapshot (stored in Snapshot via the provided closure).
func RegisterSnapshotKind(kind string, fn func(s *Snapshot, id, name string, rev int64, mb string, raw json.RawMessage) error) {
	extraKinds[kind] = fn
}

// RegisterSnapshotCheck adds a cross-object invariant.
func RegisterSnapshotCheck(fn func(s *Snapshot) []FieldError) { extraChecks = append(extraChecks, fn) }
