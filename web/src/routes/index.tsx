import { createFileRoute, Link } from '@tanstack/react-router'
import { type UseQueryResult, useQuery } from '@tanstack/react-query'
import { CheckCircle2, CircleDashed } from 'lucide-react'
import { PageHeader } from '@/components/PageHeader'
import { Badge, Banner, KeyValues, Panel, Skeleton } from '@/components/ui'
import { QueryError } from '@/components/states'
import { effectiveQuery, metaQuery, oidcProvidersQuery, principalsQuery, sessionQuery, systemStatusQuery, usersQuery } from '@/lib/api/queries'
import { useCan } from '@/lib/session'
import type { Effective } from '@/lib/api/types'
import { fmtDateTime } from '@/lib/format'
import { ActivitySummaryPanel } from '@/features/activity/ActivityPanels'
import { QuotaBudgetPanel } from '@/features/quota/OverviewPanel'
import { BridgesPanel } from '@/features/config/BridgesPanel'
import o from '@/features/overview/overview.module.css'

export const Route = createFileRoute('/')({
  component: Overview,
})

function Step({ done, children }: { done: boolean; children: React.ReactNode }) {
  return (
    <li className={o.step}>
      {done ? <CheckCircle2 size={16} color="var(--success)" aria-label="done" /> : <CircleDashed size={16} color="var(--text-muted)" aria-label="pending" />}
      <span>{children}</span>
    </li>
  )
}

const kindLabels: Record<string, string> = {
  workspaces: 'Workspaces',
  projects: 'Projects',
  providers: 'Providers',
  provider_accounts: 'Accounts',
  subscription_entitlements: 'Subscriptions',
  pools: 'Pools',
  model_aliases: 'Model aliases',
  routing_plans: 'Routing plans',
  client_bindings: 'Clients',
  quota_monitors: 'Quota monitors',
  budgets: 'Budgets',
  privacy_policies: 'Privacy policies',
  trace_policies: 'Trace policies',
  event_destinations: 'Event destinations',
  notification_channels: 'Notify channels',
  notification_rules: 'Notify rules',
}

const countRoute: Record<string, string> = {
  workspaces: '/workspaces',
  projects: '/workspaces',
  providers: '/providers',
  provider_accounts: '/providers/accounts',
  subscription_entitlements: '/providers/subscriptions',
  pools: '/providers/pools',
  model_aliases: '/routing',
  routing_plans: '/routing/plans',
  client_bindings: '/clients',
  quota_monitors: '/quotas/monitors',
  budgets: '/usage/budgets',
  privacy_policies: '/privacy',
  trace_policies: '/privacy/trace-policies',
  event_destinations: '/events/destinations',
  notification_channels: '/notifications',
  notification_rules: '/notifications/rules',
}

const kindRoute: Record<string, string> = {
  workspace: '/workspaces',
  project: '/workspaces',
  provider: '/providers',
  provider_account: '/providers/accounts',
  subscription_entitlement: '/providers/subscriptions',
  pool: '/providers/pools',
  model_alias: '/routing',
  routing_plan: '/routing/plans',
  client_binding: '/clients',
  quota_monitor: '/quotas/monitors',
  budget: '/usage/budgets',
  privacy_policy: '/privacy',
  trace_policy: '/privacy/trace-policies',
  event_destination: '/events/destinations',
  notification_channel: '/notifications',
  notification_rule: '/notifications/rules',
}

function ConfigurationPanel({ eff }: { eff: UseQueryResult<Effective> }) {
  return (
    <Panel title="Configuration" subtitle="The active compiled configuration served by the gateway." className={o.wide}>
      {eff.isError ? (
        <QueryError error={eff.error} retry={() => void eff.refetch()} />
      ) : !eff.data ? (
        <Skeleton height={80} />
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <div className={o.configBody}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 12, minWidth: 0 }}>
              <div className={o.meta}>
                <span>
                  Revision <span className="num" style={{ color: 'var(--text)' }}>{eff.data.config_revision}</span>
                </span>
                <span>Compiled {fmtDateTime(eff.data.compiled_at)}</span>
              </div>
              <div className={o.counts}>
                {Object.entries(kindLabels).map(([k, label]) => (
                  <Link key={k} to={countRoute[k] ?? '/'} className={o.count}>
                    <span className={o.countValue}>{eff.data.counts[k] ?? 0}</span>
                    <span className={o.countLabel} title={label}>{label}</span>
                  </Link>
                ))}
              </div>
            </div>
            {eff.data.plans.length > 0 && (
              <div>
                <div className={o.sectionLabel}>Routing plans</div>
                <div className={o.plans}>
                  {eff.data.plans.map((p) => (
                    <div key={p.id} className={o.plan}>
                      <Link to="/routing/plans/$planId" params={{ planId: p.id }} className="mono">{p.name}</Link>
                      {p.published_version ? <Badge tone="success">v{p.published_version}</Badge> : <Badge tone="warning">unpublished</Badge>}
                      <span className="muted">{p.bindings} client{p.bindings === 1 ? '' : 's'}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
          {eff.data.drifted.length > 0 && (
            <Banner tone="warning">
              <strong>Drift.</strong> These Terraform-managed objects were changed outside Terraform:{' '}
              {eff.data.drifted.map((d, i) => (
                <span key={d.id}>
                  {i > 0 && ', '}
                  <Link to={kindRoute[d.kind] ?? '/'}>{d.kind} {d.name}</Link>
                </span>
              ))}
            </Banner>
          )}
        </div>
      )}
    </Panel>
  )
}

function Overview() {
  const session = useQuery(sessionQuery)
  const meta = useQuery(metaQuery)
  const isAdmin = useCan('users.manage')
  const system = useQuery({ ...systemStatusQuery, enabled: useCan('system.manage') })
  const users = useQuery({ ...usersQuery, enabled: isAdmin })
  const principals = useQuery({ ...principalsQuery, enabled: isAdmin })
  const sso = useQuery({ ...oidcProvidersQuery, enabled: useCan('auth.settings') })
  const canConfig = useCan('config.read')
  const eff = useQuery({ ...effectiveQuery, enabled: canConfig })
  const canActivity = useCan('activity.read')
  const canUsage = useCan('usage.read')

  return (
    <>
      <PageHeader
        title="Overview"
        subtitle="Harbor Pilot connects coding agents to models and subscriptions, routes each request through explicit policies, and explains what happened."
      />
      <div className={o.grid}>
        {canActivity && <ActivitySummaryPanel />}
        {canUsage && <QuotaBudgetPanel />}
        <Panel title="This instance">
          {meta.isError ? (
            <QueryError error={meta.error} retry={() => void meta.refetch()} />
          ) : !meta.data ? (
            <Skeleton height={80} />
          ) : (
            <KeyValues
              items={[
                ['Version', <span className="mono">{meta.data.version}</span>],
                ['Public URL', <span className="ident">{meta.data.public_url}</span>],
                ['Started', fmtDateTime(meta.data.started_at)],
                ['Signed in as', <>{session.data?.user?.email ?? session.data?.principal_kind} <Badge>{session.data?.role ?? 'service'}</Badge></>],
                ...(system.data
                  ? ([
                      ['harbor.db schema', <span className="mono num">{system.data.schema_version}</span>],
                      ['traces.db schema', <span className="mono num">{system.data.traces_schema_version}</span>],
                      ['Master key', <span className="ident">{system.data.master_key_id}</span>],
                    ] as [React.ReactNode, React.ReactNode][])
                  : []),
              ]}
            />
          )}
        </Panel>
        {canConfig && <ConfigurationPanel eff={eff} />}
        {canConfig && <BridgesPanel />}
        {isAdmin && (
          <Panel title="Getting started" subtitle="Secure access before connecting clients and providers.">
            <ul className={o.steps}>
              <Step done>Create the first administrator</Step>
              <Step done={(users.data?.items?.length ?? 0) > 1}>
                <Link to="/settings/users">Invite operators and viewers</Link>
              </Step>
              <Step done={(sso.data?.items?.length ?? 0) > 0}>
                <Link to="/settings/auth">Connect single sign-on</Link> (optional)
              </Step>
              <Step done={(principals.data?.items?.length ?? 0) > 0}>
                <Link to="/settings/service-principals">Create a service principal for Terraform or CI</Link> (optional)
              </Step>
            </ul>
          </Panel>
        )}
      </div>
    </>
  )
}
