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
      title="Model names"
      subtitle="Names your tools ask for, like coding/default. Each points at a fallback group (or another model name), and plans map other requested models onto them."
      noun="model name"
      empty="Create a model name such as coding/default that points at a fallback group."
      columns={[
        {
          header: 'Goes to',
          mobile: 'line',
          cell: (r) => {
            if (r.spec.alias_id) return <>Model name {alias[r.spec.alias_id]?.name ?? r.spec.alias_id}</>
            const pl = pool[r.spec.pool_id]
            const e = resolved[r.id]
            if (!pl) return <span className="muted">{e?.pool_id ? 'Unknown fallback group' : 'Nothing'}</span>
            const n = pl.spec.members.length
            return (
              <>
                Fallback group {pl.name} <span className="muted">({n} {n === 1 ? 'account' : 'accounts'})</span>
              </>
            )
          },
        },
        { header: 'Needs', mobile: 'meta', cell: (r) => r.spec.required_capabilities.join(', ') || <span className="muted">—</span> },
        { header: 'Description', mobile: 'meta', cell: (r) => r.spec.description || <span className="muted">—</span> },
      ]}
      dialog={(r, close) => <AliasDialog alias={r} onClose={close} />}
    />
  )
}
