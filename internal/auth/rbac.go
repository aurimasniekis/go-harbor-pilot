// Package auth implements human and automation authentication and the
// role/permission model shared by the control-plane API and UI.
package auth

import (
	"context"
	"slices"
	"sort"
)

// Permission is a capability checked by API operations. Every operation
// declares exactly one permission (exposed as x-permission in OpenAPI).
type Permission string

// Pseudo permissions.
const (
	// PermPublic needs no authentication.
	PermPublic Permission = "public"
	// PermSession needs any authenticated principal.
	PermSession Permission = "session"
)

// Concrete permissions.
const (
	PermConfigRead        Permission = "config.read"
	PermConfigWrite       Permission = "config.write"
	PermPolicyPublish     Permission = "policy.publish"
	PermSecretsWrite      Permission = "secrets.write"
	PermTokensManage      Permission = "tokens.manage"
	PermUsageRead         Permission = "usage.read"
	PermActivityRead      Permission = "activity.read"
	PermTraceMetadataRead Permission = "traces.metadata.read"
	PermTraceDelete       Permission = "traces.delete"
	PermEventsManage      Permission = "events.manage"
	PermNotifyManage      Permission = "notifications.manage"
	PermCompatManage      Permission = "compat.manage"
	PermAuditRead         Permission = "audit.read"
	PermUsersManage       Permission = "users.manage"
	PermAuthSettings      Permission = "auth.settings"
	PermSystemManage      Permission = "system.manage"

	// Trace content permissions are never implied by a role. They are granted
	// to individual users or service principals explicitly and audited.
	PermTraceContentRead       Permission = "traces.content.read"
	PermTraceContentUnredacted Permission = "traces.content.unredacted"
	PermTraceExport            Permission = "traces.export"
)

// Role is a named bundle of permissions.
type Role string

// Built-in roles.
const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
)

// Roles lists the built-in roles from most to least privileged.
var Roles = []Role{RoleAdmin, RoleOperator, RoleViewer}

var viewerPerms = []Permission{
	PermConfigRead, PermUsageRead, PermActivityRead, PermTraceMetadataRead,
}

var operatorPerms = append(slices.Clone(viewerPerms),
	PermConfigWrite, PermPolicyPublish, PermSecretsWrite, PermTokensManage,
	PermEventsManage, PermNotifyManage, PermCompatManage, PermAuditRead, PermTraceDelete,
)

var adminPerms = append(slices.Clone(operatorPerms),
	PermUsersManage, PermAuthSettings, PermSystemManage,
)

// ContentPermissions are grantable only explicitly.
var ContentPermissions = []Permission{PermTraceContentRead, PermTraceContentUnredacted, PermTraceExport}

// RolePermissions returns the permissions implied by a role.
func RolePermissions(r Role) []Permission {
	switch r {
	case RoleAdmin:
		return adminPerms
	case RoleOperator:
		return operatorPerms
	case RoleViewer:
		return viewerPerms
	}
	return nil
}

// ValidRole reports whether r is a built-in role.
func ValidRole(r Role) bool { return slices.Contains(Roles, r) }

// AllPermissions lists every concrete permission (for service scopes).
func AllPermissions() []Permission {
	out := append(slices.Clone(adminPerms), ContentPermissions...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ValidPermission reports whether p is a concrete permission.
func ValidPermission(p Permission) bool { return slices.Contains(AllPermissions(), p) }

// PrincipalKind distinguishes humans from automation.
type PrincipalKind string

// Principal kinds.
const (
	KindAnonymous PrincipalKind = "anonymous"
	KindUser      PrincipalKind = "user"
	KindService   PrincipalKind = "service"
	KindSystem    PrincipalKind = "system"
)

// Principal is the authenticated caller of a control-plane request.
type Principal struct {
	Kind        PrincipalKind
	ID          string // usr_… or pri_… (service) or "system"
	Name        string
	Role        Role
	Perms       map[Permission]bool
	AuthMethod  string // password | oidc | breakglass | api_token | system
	SessionID   string // web session display id, empty for tokens
	CSRFChecked bool   // true when a cookie-authenticated mutation passed CSRF/origin checks
	Limited     bool   // session restricted (e.g. setup or recovery flow)
}

// Anonymous is the unauthenticated principal.
var Anonymous = &Principal{Kind: KindAnonymous, ID: "anonymous"}

// System is used for background work and CLI operations run with direct
// database access.
var System = &Principal{Kind: KindSystem, ID: "system", AuthMethod: "system", Perms: allPermsSet()}

func allPermsSet() map[Permission]bool {
	m := map[Permission]bool{}
	for _, p := range AllPermissions() {
		m[p] = true
	}
	return m
}

// NewPrincipal builds a principal from a role plus explicit grants.
func NewPrincipal(kind PrincipalKind, id, name string, role Role, grants []Permission) *Principal {
	p := &Principal{Kind: kind, ID: id, Name: name, Role: role, Perms: map[Permission]bool{}}
	for _, perm := range RolePermissions(role) {
		p.Perms[perm] = true
	}
	for _, g := range grants {
		if ValidPermission(g) {
			p.Perms[g] = true
		}
	}
	return p
}

// Authenticated reports whether the principal is not anonymous.
func (p *Principal) Authenticated() bool { return p != nil && p.Kind != KindAnonymous }

// Can reports whether the principal holds a permission.
func (p *Principal) Can(perm Permission) bool {
	switch perm {
	case PermPublic:
		return true
	case PermSession:
		return p.Authenticated()
	}
	return p != nil && p.Perms[perm]
}

// PermissionList returns the sorted permission list.
func (p *Principal) PermissionList() []string {
	out := make([]string, 0, len(p.Perms))
	for k, v := range p.Perms {
		if v {
			out = append(out, string(k))
		}
	}
	sort.Strings(out)
	return out
}

type ctxKey struct{}

// WithPrincipal stores a principal on the context.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext returns the principal on the context, or Anonymous.
func FromContext(ctx context.Context) *Principal {
	if p, ok := ctx.Value(ctxKey{}).(*Principal); ok && p != nil {
		return p
	}
	return Anonymous
}
