import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { ResourceList } from '@/features/config/ResourceList'
import { AliasDialog } from '@/features/config/AliasDialog'
import { effectiveQuery, resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { AliasRes, PoolRes } from '@/lib/api/types'

export const Route = createFileRoute('/routing/')({
  component: Aliases,
})

function Aliases() {
  const pools = useQuery(resourceListQuery<PoolRes>('pools'))
  const aliases = useQuery(resourceListQuery<AliasRes>('model-aliases'))
  const eff = useQuery(effectiveQuery)
  const pool = byId(pools.data?.items)
  const alias = byId(aliases.data?.items)
  const resolved = byId(eff.data?.aliases)
  return (
    <ResourceList<AliasRes>
      plural="model-aliases"
      title="Model aliases"
      subtitle="Client-facing model names. Plans map requested models to aliases; aliases resolve to pools."
      noun="alias"
      empty="Create an alias such as coding/default that points at a pool."
      columns={[
        {
          header: 'Target',
          cell: (r) =>
            r.spec.pool_id ? (
              <>pool <span className="mono">{pool[r.spec.pool_id]?.name ?? r.spec.pool_id}</span></>
            ) : (
              <>alias <span className="mono">{alias[r.spec.alias_id]?.name ?? r.spec.alias_id}</span></>
            ),
        },
        {
          header: 'Resolves to',
          cell: (r) => {
            const e = resolved[r.id]
            if (!e) return <span className="muted">—</span>
            return (
              <span className="mono">
                {e.chain.join(' → ')} → {pool[e.pool_id]?.name ?? (e.pool_id || 'unresolved')}
              </span>
            )
          },
        },
        { header: 'Requires', cell: (r) => r.spec.required_capabilities.join(', ') || <span className="muted">—</span> },
      ]}
      dialog={(r, close) => <AliasDialog alias={r} onClose={close} />}
    />
  )
}
