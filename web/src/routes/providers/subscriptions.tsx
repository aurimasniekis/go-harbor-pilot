import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Banner } from '@/components/ui'
import { ResourceList } from '@/features/config/ResourceList'
import { EntitlementDialog } from '@/features/config/EntitlementDialog'
import { resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { AccountRes, EntitlementRes } from '@/lib/api/types'

export const Route = createFileRoute('/providers/subscriptions')({
  component: Subscriptions,
})

function Subscriptions() {
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const acc = byId(accounts.data?.items)
  return (
    <ResourceList<EntitlementRes>
      plural="subscription-entitlements"
      title="Subscriptions"
      subtitle="What each subscription includes and costs."
      noun="subscription"
      extra={<Banner tone="info">Subscription fees are not API invoices; they are tracked separately from metered charges.</Banner>}
      empty="Record a subscription plan, its fee and its quota windows."
      columns={[
        { header: 'Account', cell: (r) => <span className="mono">{acc[r.spec.account_id]?.name ?? r.spec.account_id}</span> },
        { header: 'Plan', cell: (r) => r.spec.plan_name },
        { header: 'Fee', cell: (r) => <span className="num">{r.spec.fee} {r.spec.currency} / {r.spec.period === 'yearly' ? 'year' : 'month'}</span> },
        { header: 'Windows', cell: (r) => r.spec.windows.map((w) => w.name).join(', ') || <span className="muted">none</span> },
        { header: 'Active', cell: (r) => `${r.spec.starts_on} → ${r.spec.ends_on || 'open'}` },
      ]}
      dialog={(r, close) => <EntitlementDialog ent={r} onClose={close} />}
    />
  )
}
