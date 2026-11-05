import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { ResourceList } from '@/features/config/ResourceList'
import { PoolDialog } from '@/features/config/PoolDialog'
import { resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { AccountRes, PoolRes } from '@/lib/api/types'

export const Route = createFileRoute('/providers/pools')({
  component: Pools,
})

function Pools() {
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const acc = byId(accounts.data?.items)
  return (
    <ResourceList<PoolRes>
      plural="pools"
      title="Pools"
      subtitle="Eligible account/model candidates that aliases route to."
      noun="pool"
      empty="Group account and model candidates into a pool."
      columns={[
        { header: 'Strategy', cell: (r) => r.spec.strategy },
        { header: 'Affinity', cell: (r) => r.spec.affinity },
        {
          header: 'Members',
          cell: (r) =>
            r.spec.members.length === 0 ? (
              <span className="muted">none</span>
            ) : (
              <ul style={{ margin: 0, paddingLeft: 16 }}>
                {r.spec.members.map((m) => (
                  <li key={m.account_id + m.model} className="mono">
                    {acc[m.account_id]?.name ?? m.account_id} → {m.model}
                  </li>
                ))}
              </ul>
            ),
        },
      ]}
      dialog={(r, close) => <PoolDialog pool={r} onClose={close} />}
    />
  )
}
