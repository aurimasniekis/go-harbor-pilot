import { createFileRoute, Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { CheckCircle2, CircleDashed } from 'lucide-react'
import { PageHeader } from '@/components/PageHeader'
import { Badge, KeyValues, Panel, Skeleton } from '@/components/ui'
import { QueryError } from '@/components/states'
import { metaQuery, oidcProvidersQuery, principalsQuery, sessionQuery, systemStatusQuery, usersQuery } from '@/lib/api/queries'
import { useCan } from '@/lib/session'
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

function Overview() {
  const session = useQuery(sessionQuery)
  const meta = useQuery(metaQuery)
  const isAdmin = useCan('users.manage')
  const system = useQuery({ ...systemStatusQuery, enabled: useCan('system.manage') })
  const users = useQuery({ ...usersQuery, enabled: isAdmin })
  const principals = useQuery({ ...principalsQuery, enabled: isAdmin })
  const sso = useQuery({ ...oidcProvidersQuery, enabled: useCan('auth.settings') })

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
