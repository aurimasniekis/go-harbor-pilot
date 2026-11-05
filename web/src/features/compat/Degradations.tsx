import { Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { AlertTriangle, CheckCircle2 } from 'lucide-react'
import { Badge, Banner, EmptyState, Panel } from '@/components/ui'
import { QueryError, TableSkeleton } from '@/components/states'
import { degradationsQuery, resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { Degradation, ProviderRes } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { lossLabel } from './compat'
import { PathLabel } from './Breakers'
import c from './compat.module.css'

function Effects({ d, providerName }: { d: Degradation; providerName: string }) {
  return (
    <div>
      <div className={c.inline}>
        <strong>{providerName}</strong> <PathLabel path={d.breaker.path} />
      </div>
      {d.plans.length === 0 ? (
        <p className="secondary">No published plan routes through this path.</p>
      ) : (
        <ul className={c.list}>
          {d.plans.map((p) => (
            <li key={p.plan_id}>
              <Link to="/routing/plans/$planId" params={{ planId: p.plan_id }}>{p.name}</Link>:{' '}
              {p.action === 'passthrough' ? (
                <>
                  <Badge tone="warning">native passthrough</Badge> — loses {p.losses.length ? p.losses.map(lossLabel).join(', ') : 'nothing extra'}
                </>
              ) : (
                <>
                  <Badge>candidate skipped</Badge> — requests fail over to other candidates or fail
                </>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

/** Active degradation with the exact features lost per plan. */
export function DegradationsPanel() {
  const q = useQuery(degradationsQuery)
  const providers = useQuery(resourceListQuery<ProviderRes>('providers'))
  const prov = byId(providers.data?.items)
  return (
    <Panel title="Active degradation" subtitle="What each published plan does while a breaker is open, and which pre-authorized features it gives up.">
      {q.isError ? (
        <QueryError error={q.error} retry={() => void q.refetch()} />
      ) : !q.data ? (
        <TableSkeleton rows={2} />
      ) : q.data.items.length === 0 ? (
        <EmptyState icon={CheckCircle2} title="No degraded routes">
          Every provider path is routing normally.
        </EmptyState>
      ) : (
        <div className={c.stack}>
          {q.data.items.map((d) => (
            <Effects key={d.breaker.key} d={d} providerName={prov[d.breaker.provider_id]?.name ?? d.breaker.provider_id} />
          ))}
        </div>
      )}
    </Panel>
  )
}

/** Global banner shown while any route is degraded. */
export function DegradationBanner() {
  const canRead = useCan('config.read')
  const q = useQuery({ ...degradationsQuery, enabled: canRead })
  const providers = useQuery({ ...resourceListQuery<ProviderRes>('providers'), enabled: canRead && (q.data?.items.length ?? 0) > 0 })
  const items = q.data?.items ?? []
  if (!canRead || items.length === 0) return null
  const prov = byId(providers.data?.items)
  return (
    <Banner tone="warning" icon={AlertTriangle} role="status">
      <strong>Degraded routing.</strong> {items.length === 1 ? 'One provider path has' : `${items.length} provider paths have`} an open
      compatibility breaker.{' '}
      {items.slice(0, 3).map((d, i) => {
        const pass = d.plans.filter((p) => p.action === 'passthrough')
        const losses = [...new Set(pass.flatMap((p) => p.losses))]
        return (
          <span key={d.breaker.key}>
            {i > 0 && '; '}
            {prov[d.breaker.provider_id]?.name ?? d.breaker.provider_id} ({d.breaker.path.replace('>', ' → ')}):{' '}
            {pass.length > 0 ? `passthrough in ${pass.length} plan${pass.length > 1 ? 's' : ''}, losing ${losses.map(lossLabel).join(', ') || 'nothing extra'}` : 'candidates skipped'}
          </span>
        )
      })}
      {items.length > 3 && ` and ${items.length - 3} more`}. <Link to="/compatibility">Review compatibility</Link>
    </Banner>
  )
}
