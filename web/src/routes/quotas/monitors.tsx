import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { z } from 'zod'
import { Badge, TierBadge } from '@/components/ui'
import { ResourceList } from '@/features/config/ResourceList'
import { MonitorDialog } from '@/features/quota/MonitorDialog'
import { LoginCallback, LoginPanel } from '@/features/quota/LoginPanel'
import { quotaAdaptersQuery, resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { AccountRes, QuotaMonitorRes, Tier } from '@/lib/api/types'
import q from '@/features/quota/quota.module.css'

const search = z.object({
  login: z.string().optional().catch(undefined),
  state: z.string().optional().catch(undefined),
  code: z.string().optional().catch(undefined),
})

export const Route = createFileRoute('/quotas/monitors')({
  validateSearch: (s) => search.parse(s),
  component: Monitors,
})

function Monitors() {
  const { login, state, code } = Route.useSearch()
  const navigate = useNavigate()
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const adapters = useQuery(quotaAdaptersQuery)
  const acc = byId(accounts.data?.items)
  const ad = byId(adapters.data?.items)
  return (
    <div className={q.stack}>
      {login && state && code && (
        <LoginCallback monitorID={login} state={state} code={code} onDone={() => void navigate({ to: '/quotas/monitors', search: {} })} />
      )}
      <ResourceList<QuotaMonitorRes>
        plural="quota-monitors"
        title="Quota monitors"
        subtitle="Each monitor polls one provider quota or usage API with its own write-only credentials."
        noun="monitor"
        empty="Add a monitor to track quota windows, balances or budgets. Link it to a provider account to let routing avoid exhausted accounts."
        columns={[
          {
            header: 'Adapter',
            cell: (r) => (
              <span className={q.badges}>
                {ad[r.spec.adapter]?.display_name ?? r.spec.adapter}
                {ad[r.spec.adapter] && <TierBadge tier={ad[r.spec.adapter]!.tier as Tier} />}
              </span>
            ),
          },
          { header: 'Routing account', cell: (r) => (r.spec.account_id ? <span className="mono">{acc[r.spec.account_id]?.name ?? r.spec.account_id}</span> : <span className="muted">monitor only</span>) },
          { header: 'Poll', cell: (r) => (r.spec.poll_interval_secs ? `every ${Math.round(r.spec.poll_interval_secs / 60)} min` : <span className="muted">adapter default</span>) },
          {
            header: 'Credentials',
            cell: (r) => {
              const set = Object.entries(r.secrets ?? {}).filter(([, v]) => v.set)
              return set.length ? set.map(([k, v]) => <Badge key={k}>{k.replaceAll('_', ' ')} · {v.source}</Badge>) : <span className="muted">none</span>
            },
          },
          { header: 'State', cell: (r) => (r.spec.enabled ? <Badge tone="success">Enabled</Badge> : <Badge>Disabled</Badge>) },
        ]}
        dialog={(r, close) => <MonitorDialog monitor={r} onClose={close} />}
      />
      <LoginPanel />
    </div>
  )
}
