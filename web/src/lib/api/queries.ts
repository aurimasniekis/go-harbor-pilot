import { queryOptions } from '@tanstack/react-query'
import { apiGet } from './client'
import type { Credential, Effective, PlanVersion, AuditEntry, AuthSettings, Meta, OIDCProvider, Page, SecurityAlert, ServicePrincipal, Session, SystemStatus, User, WebSession, ApiToken } from './types'

export const qk = {
  session: ['session'] as const,
  meta: ['meta'] as const,
  users: ['users'] as const,
  principals: ['principals'] as const,
  oidc: ['oidc'] as const,
  authSettings: ['auth-settings'] as const,
  audit: ['audit'] as const,
  alerts: ['security-alerts'] as const,
  system: ['system'] as const,
  mySessions: ['my-sessions'] as const,
}

export const sessionQuery = queryOptions({
  queryKey: qk.session,
  queryFn: ({ signal }) => apiGet<Session>('/session', undefined, signal),
  staleTime: 30_000,
})

export const metaQuery = queryOptions({
  queryKey: qk.meta,
  queryFn: ({ signal }) => apiGet<Meta>('/meta', undefined, signal),
  staleTime: 60_000,
})

export const systemStatusQuery = queryOptions({
  queryKey: qk.system,
  queryFn: ({ signal }) => apiGet<SystemStatus>('/system/status', undefined, signal),
})

export const usersQuery = queryOptions({
  queryKey: qk.users,
  queryFn: ({ signal }) => apiGet<Page<User>>('/users', { limit: 500 }, signal),
})

export const principalsQuery = queryOptions({
  queryKey: qk.principals,
  queryFn: ({ signal }) => apiGet<Page<ServicePrincipal>>('/service-principals', { limit: 500 }, signal),
})

export const principalTokensQuery = (id: string) =>
  queryOptions({
    queryKey: [...qk.principals, id, 'tokens'],
    queryFn: ({ signal }) => apiGet<{ items: ApiToken[] | null }>(`/service-principals/${encodeURIComponent(id)}/tokens`, undefined, signal),
  })

export const oidcProvidersQuery = queryOptions({
  queryKey: qk.oidc,
  queryFn: ({ signal }) => apiGet<{ items: OIDCProvider[] | null }>('/auth/oidc-providers', undefined, signal),
})

export const authSettingsQuery = queryOptions({
  queryKey: qk.authSettings,
  queryFn: ({ signal }) => apiGet<AuthSettings>('/auth/settings', undefined, signal),
})

export const securityAlertsQuery = queryOptions({
  queryKey: qk.alerts,
  queryFn: ({ signal }) => apiGet<{ items: SecurityAlert[] | null }>('/security-alerts', undefined, signal),
})

export const auditQuery = (filter: { action?: string; cursor?: string; outcome?: string }) =>
  queryOptions({
    queryKey: [...qk.audit, filter],
    queryFn: ({ signal }) => apiGet<Page<AuditEntry>>('/audit', { limit: 100, ...filter }, signal),
  })

export const mySessionsQuery = queryOptions({
  queryKey: qk.mySessions,
  queryFn: ({ signal }) => apiGet<{ items: WebSession[] | null }>('/session/all', undefined, signal),
})

// ---------- managed configuration ----------

/** Resource collection paths, keyed by kind plural (URL segment). */
export type ResourcePlural =
  | 'workspaces'
  | 'projects'
  | 'providers'
  | 'provider-accounts'
  | 'subscription-entitlements'
  | 'pools'
  | 'model-aliases'
  | 'routing-plans'
  | 'client-bindings'

export const resourceKey = (plural: ResourcePlural) => ['resources', plural] as const

export function resourceListQuery<T>(plural: ResourcePlural) {
  return queryOptions({
    queryKey: resourceKey(plural),
    queryFn: ({ signal }) => apiGet<Page<T>>(`/${plural}`, { limit: 500 }, signal),
  })
}

export function resourceQuery<T>(plural: ResourcePlural, id: string) {
  return queryOptions({
    queryKey: [...resourceKey(plural), id],
    queryFn: ({ signal }) => apiGet<T>(`/${plural}/${encodeURIComponent(id)}`, undefined, signal),
  })
}

export const effectiveQuery = queryOptions({
  queryKey: ['resources', 'effective'],
  queryFn: ({ signal }) => apiGet<Effective>('/config/effective', undefined, signal),
})

export const planVersionsQuery = (planId: string) =>
  queryOptions({
    queryKey: ['resources', 'routing-plans', planId, 'versions'],
    queryFn: ({ signal }) => apiGet<{ items: PlanVersion[] }>(`/routing-plans/${encodeURIComponent(planId)}/versions`, undefined, signal),
  })

export const credentialsQuery = (bindingId: string) =>
  queryOptions({
    queryKey: ['resources', 'client-bindings', bindingId, 'credentials'],
    queryFn: ({ signal }) =>
      apiGet<{ items: Credential[] }>(`/client-bindings/${encodeURIComponent(bindingId)}/credentials`, { children: true }, signal),
  })
