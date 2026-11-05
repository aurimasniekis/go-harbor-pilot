import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { ResourceList } from '@/features/config/ResourceList'
import { PoolDialog } from '@/features/config/PoolDialog'
import { resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { AccountRes, PoolRes } from '@/lib/api/types'

export const Route = createFileRoute('/routing/pools')({
  component: Pools,
})

const strategyName: Record<string, string> = {
  ordered: 'Tried in order',
  weighted: 'Spread by weight',
  reset_soonest: 'Quota resetting soonest first',
  least_used: 'Least used first',
  round_robin: 'Round robin',
}

function Pools() {
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const acc = byId(accounts.data?.items)
  return (
    <ResourceList<PoolRes>
      plural="pools"
      title="Fallback groups"
      subtitle="Account and model pairs a model name routes to, tried in order (or by the group’s strategy)."
      noun="fallback group"
      empty="Group account and model pairs that can serve the same requests."
      columns={[
        { header: 'Order', mobile: 'meta', cell: (r) => strategyName[r.spec.strategy || 'ordered'] ?? r.spec.strategy },
        { header: 'Sessions', mobile: 'meta', cell: (r) => (r.spec.affinity === 'none' ? 'Any member per request' : 'Same member for a session') },
        {
          header: 'Members',
          mobile: 'line',
          cell: (r) =>
            r.spec.members.length === 0 ? (
              <span className="muted">none</span>
            ) : (
              <ol style={{ margin: 0, paddingLeft: 18 }}>
                {r.spec.members.map((m) => (
                  <li key={m.account_id + m.model}>
                    {acc[m.account_id]?.name ?? m.account_id} <span className="muted">· {m.model}</span>
                  </li>
                ))}
              </ol>
            ),
        },
      ]}
      dialog={(r, close) => <PoolDialog pool={r} onClose={close} />}
    />
  )
}
