import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { NameCell, ResourceList } from '@/features/config/ResourceList'
import { PoolDialog, strategies } from '@/features/config/PoolDialog'
import { resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { AccountRes, AliasRes, PoolRes, ProviderRes } from '@/lib/api/types'
import { accountLabel } from '@/features/routing/names'
import { poolUsers } from '@/features/routing/usage'

export const Route = createFileRoute('/routing/pools')({
  component: Pools,
})

const strategyName: Record<string, string> = Object.fromEntries(strategies.map((x) => [x.value, x.label]))

function Pools() {
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const providers = useQuery(resourceListQuery<ProviderRes>('providers'))
  const aliases = useQuery(resourceListQuery<AliasRes>('model-aliases'))
  const acc = byId(accounts.data?.items)
  const prov = byId(providers.data?.items)
  const users = poolUsers(aliases.data?.items ?? [])
  return (
    <ResourceList<PoolRes>
      plural="pools"
      title="Fallback groups"
      hideTitle
      explain="The accounts and models a model name can use, tried in order (or by the group's strategy)."
      noun="fallback group"
      nameCell={(r) => <NameCell name={r.name} display={r.spec.display_name} />}
      usedBy={(r) => users[r.id] ?? []}
      empty="Group account and model pairs that can serve the same requests."
      columns={[
        { header: 'How accounts are chosen', mobile: 'meta', cell: (r) => strategyName[r.spec.strategy || 'ordered'] ?? r.spec.strategy },
        { header: 'Conversations', mobile: 'meta', cell: (r) => (r.spec.affinity === 'none' ? 'Any account per request' : 'Stay on one account') },
        {
          header: 'Accounts',
          mobile: 'line',
          cell: (r) =>
            r.spec.members.length === 0 ? (
              <span className="muted">No accounts (rejects every request)</span>
            ) : (
              <ol style={{ margin: 0, paddingLeft: 18 }}>
                {[...r.spec.members].sort((a, b) => (a.priority ?? 0) - (b.priority ?? 0)).map((m) => (
                  <li key={m.account_id + m.model}>
                    {accountLabel(acc[m.account_id], prov, m.account_id)} <span className="muted">· {m.model}</span>
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
