import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Badge } from '@/components/ui'
import { BillingBadge } from '@/components/ResourceBadges'
import { ResourceList } from '@/features/config/ResourceList'
import { AccountDialog } from '@/features/config/AccountDialog'
import { resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { AccountRes, ProviderRes } from '@/lib/api/types'

export const Route = createFileRoute('/providers/accounts')({
  component: Accounts,
})

function Accounts() {
  const providers = useQuery(resourceListQuery<ProviderRes>('providers'))
  const prov = byId(providers.data?.items)
  return (
    <ResourceList<AccountRes>
      plural="provider-accounts"
      title="Accounts"
      subtitle="Authorized accounts at a provider. Credentials are write-only and never displayed."
      noun="account"
      writePerm="secrets.write"
      empty="Add an account with an API key, a setup token or a secret reference."
      columns={[
        { header: 'Provider', cell: (r) => <span className="mono">{prov[r.spec.provider_id]?.name ?? r.spec.provider_id}</span> },
        { header: 'Billing', cell: (r) => <BillingBadge value={r.spec.billing} /> },
        {
          header: 'Credential',
          cell: (r) => {
            const s = r.secrets?.credential
            if (!s?.set) return <Badge tone="warning">not set</Badge>
            return <Badge tone="success" title={s.fingerprint ? `fingerprint ${s.fingerprint}` : undefined}>{s.source}</Badge>
          },
        },
        { header: 'Limits', cell: (r) => <span className="secondary">{r.spec.max_concurrency || '∞'} concurrent · {r.spec.requests_per_minute || '∞'} rpm</span> },
        { header: 'Status', cell: (r) => (r.spec.enabled ? <Badge tone="success">enabled</Badge> : <Badge>disabled</Badge>) },
      ]}
      dialog={(r, close) => <AccountDialog account={r} onClose={close} />}
    />
  )
}
