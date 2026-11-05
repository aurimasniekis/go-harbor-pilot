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

export const Route = createFileRoute('/')({
  component: Overview,
})

function Step({ done, children }: { done: boolean; children: React.ReactNode }) {
  return (
    <li style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
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
}

function ConfigurationPanel({ eff }: { eff: UseQueryResult<Effective> }) {
  return (
    <Panel title="Configuration" subtitle="The active compiled configuration served by the gateway.">
      {eff.isError ? (
        <QueryError error={eff.error} retry={() => void eff.refetch()} />
      ) : !eff.data ? (
        <Skeleton height={80} />
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <KeyValues
            items={[
              ['Revision', <span className="num">{eff.data.config_revision}</span>],
              ['Compiled', fmtDateTime(eff.data.compiled_at)],
              ...Object.entries(kindLabels).map(([k, label]) => [label, <span className="num">{eff.data.counts[k] ?? 0}</span>] as [React.ReactNode, React.ReactNode]),
            ]}
          />
          {eff.data.plans.length > 0 && (
            <div>
              <div className="muted" style={{ fontSize: 'var(--text-sm)' }}>Routing plans</div>
              <ul style={{ margin: 0, paddingLeft: 18 }}>
                {eff.data.plans.map((p) => (
                  <li key={p.id}>
                    <Link to="/routing/plans/$planId" params={{ planId: p.id }} className="mono">{p.name}</Link>{' '}
                    {p.published_version ? <Badge tone="success">v{p.published_version}</Badge> : <Badge tone="warning">unpublished</Badge>}{' '}
                    <span className="muted">{p.bindings} client{p.bindings === 1 ? '' : 's'}</span>
                  </li>
                ))}
              </ul>
            </div>
          )}
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

  return (
    <>
      <PageHeader
        title="Overview"
        subtitle="Harbor Pilot connects coding agents to models and subscriptions, routes each request through explicit policies, and explains what happened."
      />
      <div style={{ display: 'grid', gap: 'var(--space-4)', gridTemplateColumns: 'repeat(auto-fit, minmax(320px, 1fr))' }}>
        <Panel title="This instance">
          {meta.isError ? (
            <QueryError error={meta.error} retry={() => void meta.refetch()} />
          ) : !meta.data ? (
            <Skeleton height={80} />
          ) : (
            <KeyValues
              items={[
                ['Version', meta.data.version],
                ['Public URL', <span className="mono">{meta.data.public_url}</span>],
                ['Started', fmtDateTime(meta.data.started_at)],
                ['Signed in as', <>{session.data?.user?.email ?? session.data?.principal_kind} <Badge>{session.data?.role ?? 'service'}</Badge></>],
                ...(system.data
                  ? ([
                      ['Schema', `harbor.db v${system.data.schema_version} · traces.db v${system.data.traces_schema_version}`],
                      ['Master key', <span className="mono">{system.data.master_key_id}</span>],
                    ] as [React.ReactNode, React.ReactNode][])
                  : []),
              ]}
            />
          )}
        </Panel>
        {canConfig && <ConfigurationPanel eff={eff} />}
        {isAdmin && (
          <Panel title="Getting started" subtitle="Secure access before connecting clients and providers.">
            <ul style={{ listStyle: 'none', padding: 0, margin: 0, display: 'flex', flexDirection: 'column', gap: 8 }}>
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
