import { useMemo, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Coins } from 'lucide-react'
import { Badge, Banner, EmptyState, Panel, Skeleton, TableScroll, Tabs } from '@/components/ui'
import { SelectField } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { externalUsageQuery, reconciliationQuery, resourceListQuery, usageSeriesQuery, usageSummaryQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { AccountRes, QuotaMonitorRes, UsageGroupBy } from '@/lib/api/types'
import { fmtDateTime, fmtMoney, fmtNumber } from '@/lib/format'
import { TokenValue } from '@/features/activity/bits'
import { CostList } from '@/features/usage/CostList'
import { UsageChart } from '@/features/usage/UsageChart'
import { presetRange, rangePresets, stepFor, type RangePreset } from '@/features/usage/range'
import q from '@/features/quota/quota.module.css'

export const Route = createFileRoute('/usage/')({
  component: UsagePage,
})

const groupOptions: { value: UsageGroupBy; label: string }[] = [
  { value: 'account', label: 'Provider account' },
  { value: 'provider', label: 'Provider' },
  { value: 'model', label: 'Model' },
  { value: 'binding', label: 'Client' },
  { value: 'project', label: 'Project' },
  { value: 'workspace', label: 'Workspace' },
  { value: 'billing', label: 'Billing class' },
]

function UsagePage() {
  const [preset, setPreset] = useState<RangePreset>('30')
  const [groupBy, setGroupBy] = useState<UsageGroupBy>('account')
  const days = Number(preset)
  // Recompute the range only when the preset changes so queries stay stable.
  const range = useMemo(() => presetRange(days), [days])
  const summary = useQuery(usageSummaryQuery(range, groupBy))
  const series = useQuery(usageSeriesQuery(range, stepFor(days)))
  const s = summary.data
  return (
    <div className={q.stack}>
      <div className={q.toolbar}>
        <Tabs label="Time range" variant="segmented" value={preset} onChange={setPreset} items={rangePresets.map((r) => ({ value: r.value, label: r.label }))} />
        <SelectField label="Group by" value={groupBy} onChange={setGroupBy} options={groupOptions} />
        <span className="muted" style={{ fontSize: 'var(--text-sm)' }}>
          {fmtDateTime(range.from)} → {fmtDateTime(range.to)}
        </span>
      </div>
      <Panel title="Over time" subtitle={`Tokens per ${stepFor(days)} and cost per category (one line per category and currency).`}>
        {series.isError ? (
          <QueryError error={series.error} retry={() => void series.refetch()} />
        ) : !series.data ? (
          <Skeleton height={300} />
        ) : (series.data.points ?? []).length === 0 ? (
          <EmptyState icon={Coins} title="No usage in this range" />
        ) : (
          <UsageChart points={series.data.points ?? []} />
        )}
      </Panel>
      {summary.isError ? (
        <QueryError error={summary.error} retry={() => void summary.refetch()} />
      ) : !s ? (
        <TableSkeleton />
      ) : (
        <>
          <Panel title="Totals by category" subtitle="Each category and currency is reported on its own; they are never summed.">
            <CostList costs={s.totals} empty="No costs in this range." />
            <details style={{ marginTop: 12 }}>
              <summary className="muted" style={{ cursor: 'pointer', fontSize: 'var(--text-sm)' }}>What the categories mean</summary>
              <ul className={q.list}>
                {s.categories.map((c) => (
                  <li key={c}>{c}</li>
                ))}
              </ul>
            </details>
          </Panel>
          <Panel title={`By ${groupOptions.find((g) => g.value === groupBy)?.label.toLowerCase()}`}>
            {s.groups.length === 0 ? (
              <EmptyState icon={Coins} title="No usage in this range" />
            ) : (
              <TableScroll label="Usage by group">
                <table className="data-table" style={{ width: '100%' }}>
                  <thead>
                    <tr>
                      <th>{groupOptions.find((g) => g.value === groupBy)?.label}</th>
                      <th>Attempts</th>
                      <th>Input</th>
                      <th>Output</th>
                      <th>Cache read / write</th>
                      <th>Usage not reported</th>
                      <th>Costs</th>
                      <th>Allocated subscription fees</th>
                    </tr>
                  </thead>
                  <tbody>
                    {s.groups.map((g) => (
                      <tr key={g.key || '(none)'}>
                        <td>
                          <span className="mono">{g.label || <span className="muted">(none)</span>}</span>
                          {g.billing && groupBy === 'account' && <> <Badge>{g.billing.replaceAll('_', ' ')}</Badge></>}
                        </td>
                        <td className="num">{fmtNumber(g.attempts)}</td>
                        <td><TokenValue v={g.tokens.input} /></td>
                        <td><TokenValue v={g.tokens.output} /></td>
                        <td>
                          <TokenValue v={g.tokens.cache_read} /> / <TokenValue v={g.tokens.cache_write} />
                        </td>
                        <td className="num">{g.tokens.unknown_usage_attempts > 0 ? <Badge tone="warning">{g.tokens.unknown_usage_attempts}</Badge> : 0}</td>
                        <td><CostList costs={g.costs} /></td>
                        <td><CostList costs={g.allocated} /></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </TableScroll>
            )}
          </Panel>
          <Panel title="Subscription fees" subtitle="Entitlement fees prorated to this range. Fees are not usage charges and are not invoices.">
            {s.subscription_fees.length === 0 ? (
              <span className="muted">No subscription with a fee is active in this range.</span>
            ) : (
              <TableScroll label="Subscription fees">
                <table className="data-table" style={{ width: '100%' }}>
                  <thead>
                    <tr>
                      <th>Plan</th>
                      <th>Fee</th>
                      <th>Share of this range</th>
                    </tr>
                  </thead>
                  <tbody>
                    {s.subscription_fees.map((f) => (
                      <tr key={f.entitlement_id}>
                        <td>{f.plan_name}</td>
                        <td className="num">{fmtMoney(f.fee, f.currency)} / {f.period === 'yearly' ? 'year' : 'month'}</td>
                        <td className="num">{fmtMoney(f.prorated, f.currency)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </TableScroll>
            )}
          </Panel>
        </>
      )}
      <ProviderReported />
    </div>
  )
}

function ProviderReported() {
  const [days, setDays] = useState<'7' | '14' | '30'>('14')
  const ext = useQuery(externalUsageQuery(Number(days)))
  const rec = useQuery(reconciliationQuery(Number(days)))
  const monitors = useQuery(resourceListQuery<QuotaMonitorRes>('quota-monitors'))
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const mon = byId(monitors.data?.items)
  const acc = byId(accounts.data?.items)
  return (
    <>
      <Panel
        title="Reconciliation"
        subtitle="Provider-reported usage compared with what the gateway observed for the linked account. Differences are usage that bypassed the gateway, reporting lag or price differences."
        actions={<Tabs label="Days" variant="segmented" value={days} onChange={setDays} items={[{ value: '7', label: '7 days' }, { value: '14', label: '14 days' }, { value: '30', label: '30 days' }]} />}
      >
        {rec.isError ? (
          <QueryError error={rec.error} retry={() => void rec.refetch()} />
        ) : !rec.data ? (
          <TableSkeleton rows={3} />
        ) : (rec.data.items ?? []).length === 0 ? (
          <span className="muted">No monitor linked to a provider account reports usage in this range.</span>
        ) : (
          <TableScroll label="Reconciliation">
            <table className="data-table" style={{ width: '100%' }}>
              <thead>
                <tr>
                  <th>Day</th>
                  <th>Monitor → account</th>
                  <th>Provider input / output</th>
                  <th>Gateway input / output</th>
                  <th>Not seen by gateway (input)</th>
                  <th>Provider charge</th>
                  <th>Gateway estimate</th>
                </tr>
              </thead>
              <tbody>
                {(rec.data.items ?? []).map((r) => (
                  <tr key={r.monitor_id + r.day}>
                    <td>{r.day.slice(0, 10)}{r.partial && <> <Badge tone="info">partial</Badge></>}</td>
                    <td className="mono">{mon[r.monitor_id]?.name ?? r.monitor_id} → {acc[r.account_id]?.name ?? r.account_id}</td>
                    <td><TokenValue v={r.provider_input_tokens} /> / <TokenValue v={r.provider_output_tokens} /></td>
                    <td><TokenValue v={r.gateway_input_tokens} /> / <TokenValue v={r.gateway_output_tokens} /></td>
                    <td className="num">
                      {r.unattributed_input_tokens === null ? (
                        <span className="muted">Unknown</span>
                      ) : r.unattributed_input_tokens !== 0 ? (
                        <span className={q.highlight}>{fmtNumber(r.unattributed_input_tokens)}</span>
                      ) : (
                        0
                      )}
                    </td>
                    <td className="num">{r.provider_actual_cost ? `${fmtMoney(r.provider_actual_cost, r.currency)} actual` : <span className="muted">—</span>}</td>
                    <td className="num">{r.gateway_estimated_cost ? `${fmtMoney(r.gateway_estimated_cost, r.currency)} est.` : <span className="muted">—</span>}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </TableScroll>
        )}
      </Panel>
      <Panel title="Provider-reported usage" subtitle="Totals from provider usage and billing APIs (the total breakdown only, so rows are not double counted).">
        {ext.isError ? (
          <QueryError error={ext.error} retry={() => void ext.refetch()} />
        ) : !ext.data ? (
          <TableSkeleton rows={3} />
        ) : (ext.data.items ?? []).length === 0 ? (
          <span className="muted">No provider-reported usage in this range.</span>
        ) : (
          <>
            {(ext.data.items ?? []).length >= 5000 && <Banner tone="warning">Showing the first 5,000 rows.</Banner>}
            <TableScroll label="Provider-reported usage" style={{ maxHeight: 420 }}>
              <table className="data-table" style={{ width: '100%' }}>
                <thead>
                  <tr>
                    <th>Interval</th>
                    <th>Monitor</th>
                    <th>Requests</th>
                    <th>Input / output</th>
                    <th>Actual charge</th>
                    <th>Estimated</th>
                    <th>Quality</th>
                  </tr>
                </thead>
                <tbody>
                  {(ext.data.items ?? []).map((r) => (
                    <tr key={r.monitor_id + r.interval_start + r.dimensions}>
                      <td>{fmtDateTime(r.interval_start)}{r.partial && <> <Badge tone="info">partial</Badge></>}</td>
                      <td className="mono">{mon[r.monitor_id]?.name ?? r.monitor_id}</td>
                      <td><TokenValue v={r.requests} /></td>
                      <td><TokenValue v={r.input_tokens} /> / <TokenValue v={r.output_tokens} /></td>
                      <td className="num">{r.actual_cost ? fmtMoney(r.actual_cost, r.currency) : <span className="muted">—</span>}</td>
                      <td className="num">{r.estimated_cost ? fmtMoney(r.estimated_cost, r.currency) : <span className="muted">—</span>}</td>
                      <td><Badge>{r.quality.replaceAll('_', ' ')}</Badge></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </TableScroll>
          </>
        )}
      </Panel>
    </>
  )
}
