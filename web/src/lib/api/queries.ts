import { queryOptions } from '@tanstack/react-query'
import { apiGet } from './client'
import type { AuditEntry, AuthSettings, Meta, OIDCProvider, Page, SecurityAlert, ServicePrincipal, Session, SystemStatus, User, WebSession, ApiToken } from './types'

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
