import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { AlertTriangle, Gauge } from 'lucide-react'
import { Banner, EmptyState, Panel } from '@/components/ui'
import { QueryError, TableSkeleton } from '@/components/states'
import { quotaBoardQuery, resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { AccountRes } from '@/lib/api/types'
import { AccountHealthTable, MonitorCard, ObservedRateLimits } from '@/features/quota/QuotaBoard'
import q from '@/features/quota/quota.module.css'

export const Route = createFileRoute('/quotas/')({
  component: QuotaBoardPage,
})

function QuotaBoardPage() {
  const board = useQuery(quotaBoardQuery)
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const acc = byId(accounts.data?.items)
  const name = (id: string) => acc[id]?.name ?? id
  if (board.isError) return <QueryError error={board.error} retry={() => void board.refetch()} />
  if (!board.data) return <TableSkeleton />
  const b = board.data
  return (
    <div className={q.stack}>
      {b.dropped_observations > 0 && (
        <Banner tone="warning" icon={AlertTriangle}>
          Coverage reduced: {b.dropped_observations} response-derived rate-limit observations were dropped under load since the server started.
        </Banner>
      )}
      <Panel title="Quota monitors" subtitle="Polled provider quota and usage APIs. Linked monitors decide whether an account is exhausted for routing.">
        {b.monitors.length === 0 ? (
          <EmptyState icon={Gauge} title="No quota monitors">
            Add a monitor under Monitors to track a provider&apos;s quota windows, balance or budget.
          </EmptyState>
        ) : (
          <div className={q.cards}>
            {b.monitors.map((m) => (
              <MonitorCard key={m.id} m={m} accountName={m.account_id ? name(m.account_id) : undefined} />
            ))}
          </div>
        )}
      </Panel>
      <Panel title="Account routing health" subtitle="What routing sees for each provider account right now: quota exhaustion, gateway cooldowns and the tightest known window.">
        <AccountHealthTable rows={b.account_health} />
      </Panel>
      <Panel title="Observed rate limits" subtitle="Short-lived limits read from upstream response headers (fresh for two minutes; informational, never decisive).">
        <ObservedRateLimits rows={b.observed_accounts} accountName={name} />
      </Panel>
    </div>
  )
}
