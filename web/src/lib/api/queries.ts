import { queryOptions } from '@tanstack/react-query'
import { apiGet } from './client'
import type { BudgetUsage, ExternalUsageRow, Forecast, HistoryPoint, QuotaAdapter, QuotaBoard, QuotaLogin, ReconciliationRow, UsageGroupBy, UsagePoint, UsageSummary, ActivitySummary, ObservedSession, RequestDetail, RequestSummary, Credential, Effective, PlanVersion, AuditEntry, AuthSettings, Meta, OIDCProvider, Page, SecurityAlert, ServicePrincipal, Session, SystemStatus, User, WebSession, ApiToken, RoutingVariable } from './types'

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
  | 'quota-monitors'
  | 'budgets'

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

// ---------- activity ----------
export interface RequestFilter {
  status?: string
  harness?: string
  binding_id?: string
  project_id?: string
  session_id?: string
  model?: string
  cursor?: string
  limit?: number
}

export const requestsQuery = (f: RequestFilter) =>
  queryOptions({
    queryKey: ['activity', 'requests', f],
    queryFn: ({ signal }) => apiGet<Page<RequestSummary>>('/activity/requests', { limit: 50, ...f }, signal),
  })

export const requestQuery = (id: string) =>
  queryOptions({
    queryKey: ['activity', 'request', id],
    queryFn: ({ signal }) => apiGet<RequestDetail>(`/activity/requests/${encodeURIComponent(id)}`, undefined, signal),
  })

export const sessionsQuery = (f: { binding_id?: string; cursor?: string }) =>
  queryOptions({
    queryKey: ['activity', 'sessions', f],
    queryFn: ({ signal }) => apiGet<Page<ObservedSession>>('/activity/sessions', { limit: 50, ...f }, signal),
  })

export const activitySummaryQuery = (hours: number) =>
  queryOptions({
    queryKey: ['activity', 'summary', hours],
    queryFn: ({ signal }) => apiGet<ActivitySummary>('/activity/summary', { hours }, signal),
  })

// ---------- routing ----------
export const routingVariablesQuery = queryOptions({
  queryKey: ['routing', 'variables'],
  queryFn: ({ signal }) => apiGet<{ items: RoutingVariable[] }>('/routing/variables', undefined, signal),
  staleTime: Infinity,
})

// ---------- quota, usage and budgets ----------
export const quotaBoardQuery = queryOptions({
  queryKey: ['quota', 'board'],
  queryFn: ({ signal }) => apiGet<QuotaBoard>('/quota/board', undefined, signal),
  refetchInterval: 30_000,
})

export const quotaAdaptersQuery = queryOptions({
  queryKey: ['quota', 'adapters'],
  queryFn: ({ signal }) => apiGet<{ items: QuotaAdapter[] }>('/quota/adapters', undefined, signal),
  staleTime: Infinity,
})

export const quotaLoginsQuery = queryOptions({
  queryKey: ['quota', 'logins'],
  queryFn: ({ signal }) => apiGet<{ items: QuotaLogin[] }>('/quota/logins', undefined, signal),
})

export const quotaHistoryQuery = (owner: string, bucket: string, hours: number) =>
  queryOptions({
    queryKey: ['quota', 'history', owner, bucket, hours],
    queryFn: ({ signal }) => apiGet<{ points: HistoryPoint[] }>(`/quota/buckets/${encodeURIComponent(owner)}/history`, { bucket, hours }, signal),
  })

export const quotaForecastQuery = (owner: string, bucket: string) =>
  queryOptions({
    queryKey: ['quota', 'forecast', owner, bucket],
    queryFn: ({ signal }) => apiGet<Forecast>(`/quota/buckets/${encodeURIComponent(owner)}/forecast`, { bucket }, signal),
  })

export interface UsageRange {
  from: string
  to: string
}

export const usageSummaryQuery = (r: UsageRange, groupBy: UsageGroupBy) =>
  queryOptions({
    queryKey: ['usage', 'summary', r, groupBy],
    queryFn: ({ signal }) => apiGet<UsageSummary>('/usage/summary', { ...r, group_by: groupBy }, signal),
  })

export const usageSeriesQuery = (r: UsageRange, step: 'hour' | 'day') =>
  queryOptions({
    queryKey: ['usage', 'series', r, step],
    queryFn: ({ signal }) => apiGet<{ from: string; to: string; step: string; points: UsagePoint[] }>('/usage/series', { ...r, step }, signal),
  })

export const externalUsageQuery = (days: number) =>
  queryOptions({
    queryKey: ['usage', 'external', days],
    queryFn: ({ signal }) => apiGet<{ items: ExternalUsageRow[] }>('/usage/external', { days, breakdown: 'total' }, signal),
  })

export const reconciliationQuery = (days: number) =>
  queryOptions({
    queryKey: ['usage', 'reconciliation', days],
    queryFn: ({ signal }) => apiGet<{ items: ReconciliationRow[] }>('/usage/reconciliation', { days }, signal),
  })

export const budgetStatusQuery = queryOptions({
  queryKey: ['budgets', 'status'],
  queryFn: ({ signal }) => apiGet<{ items: BudgetUsage[] }>('/budgets/status', undefined, signal),
  refetchInterval: 60_000,
})
