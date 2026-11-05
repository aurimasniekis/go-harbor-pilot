import { useMemo, useState } from 'react'
import { createFileRoute, Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { BarChart3 } from 'lucide-react'
import { Badge, Banner, EmptyState, InfoTip, Panel, Skeleton, TableScroll, Tabs } from '@/components/ui'
import { SelectField } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { statsQuery } from '@/lib/api/queries'
import type { StatsGroup, StatsLatency } from '@/lib/api/types'
import { fmtCompact, fmtDateTime, fmtDuration, fmtNumber, fmtPercent } from '@/lib/format'
import { CostList } from '@/features/usage/CostList'
import { presetRange, rangePresets, stepFor, type RangePreset } from '@/features/usage/range'
import { StatsChart } from '@/features/stats/StatsChart'
import { dimensions, errorRate, metrics, uniqueShare, type Metric } from '@/features/stats/stats'
import s from '@/features/stats/stats.module.css'
import q from '@/features/quota/quota.module.css'

export const Route = createFileRoute('/activity/stats')({
  component: StatsPage,
})

type Dim = (typeof dimensions)[number]['value']

function StatsPage() {
  const [preset, setPreset] = useState<RangePreset>('7')
  const [by, setBy] = useState<Dim>('client')
  const [metric, setMetric] = useState<Metric>('requests')
  const days = Number(preset)
  const range = useMemo(() => presetRange(days), [days])
  const step = stepFor(days)
  const series = useQuery(statsQuery(range, 'period', step))
  const breakdown = useQuery(statsQuery(range, by, step))
  const t = series.data?.totals
  return (
    <div className={q.stack}>
      <div className={q.toolbar}>
        <Tabs label="Time range" variant="segmented" value={preset} onChange={setPreset} items={rangePresets.map((r) => ({ value: r.value, label: r.label }))} />
        <span className="muted" style={{ fontSize: 'var(--text-sm)' }}>
          {fmtDateTime(range.from)} → {fmtDateTime(range.to)}
        </span>
      </div>
      {(series.data?.truncated || breakdown.data?.truncated) && (
        <Banner tone="warning" role="status">
          More than 200,000 requests matched, so these figures cover only the oldest part of the range. Pick a shorter range.
        </Banner>
      )}
      {series.isError ? (
        <QueryError error={series.error} retry={() => void series.refetch()} />
      ) : !series.data || !t ? (
        <Skeleton height={96} />
      ) : (
        <div className={s.tiles}>
          <Tile label="Requests" value={fmtNumber(t.requests)} meta={`${fmtNumber(t.ok)} ok · ${fmtNumber(t.errors)} errors · ${fmtNumber(t.blocked)} blocked`} />
          <Tile label="Error rate" value={fmtPercent(errorRate(t), 1)} meta="Errors and cancellations; policy blocks are counted separately." />
          <Tile label="Failovers" value={fmtNumber(t.failovers)} meta={`${fmtNumber(series.data.account_switches)} account switches · ${fmtNumber(series.data.fallbacks)} fallbacks`} />
          <Tile label="Tokens" value={fmtCompact(t.input_tokens + t.output_tokens)} meta={`${fmtCompact(t.input_tokens)} in · ${fmtCompact(t.output_tokens)} out`} />
          <Tile
            label="Unknown usage"
            value={fmtNumber(t.unknown_usage)}
            meta="Completed requests whose upstream did not report usage. Counted as unknown, never as zero."
          />
          <Tile
            label="Sessions known"
            value={fmtPercent(uniqueShare(t), 0)}
            meta={`${fmtNumber(t.unique_requests)} with a session · ${fmtNumber(t.uncertain_requests)} without (history may repeat)`}
          />
          <Tile label="Duration p95" value={fmtLatency(t.duration, 'p95')} meta={`p50 ${fmtLatency(t.duration, 'p50')} · TTFT p50 ${fmtLatency(t.ttft, 'p50')}`} />
        </div>
      )}
      <Panel
        title="Over time"
        subtitle={`Per ${step}. Costs are drawn per category and currency and are never summed.`}
        actions={<Tabs label="Metric" variant="segmented" value={metric} onChange={setMetric} items={metrics} />}
      >
        {series.isError ? null : !series.data ? (
          <Skeleton height={300} />
        ) : series.data.groups.length === 0 ? (
          <EmptyState icon={BarChart3} title="No requests in this range" />
        ) : metric === 'cost' && series.data.groups.every((g) => (g.costs ?? []).length === 0) ? (
          <EmptyState icon={BarChart3} title="No priced requests in this range">
            Costs appear when the served model has a price. Subscription consumption is shown on the Usage page.
          </EmptyState>
        ) : (
          <StatsChart groups={series.data.groups} metric={metric} />
        )}
      </Panel>
      {series.data && series.data.unattributed_ledger_entries > 0 && (
        <p className="muted" style={{ fontSize: 'var(--text-sm)' }}>
          {fmtNumber(series.data.unattributed_ledger_entries)} ledger entries in this range have no client (for example canary probes) and are not in
          these figures.
        </p>
      )}
      <Panel
        title="Breakdown"
        subtitle={by === 'intent' ? 'Intent source: declared by the client, set by a rule, or inferred by the plan’s classifier.' : undefined}
        actions={<SelectField label="Group by" value={by} onChange={setBy} options={dimensions} />}
      >
        {breakdown.isError ? (
          <QueryError error={breakdown.error} retry={() => void breakdown.refetch()} />
        ) : !breakdown.data ? (
          <TableSkeleton />
        ) : breakdown.data.groups.length === 0 ? (
          <EmptyState icon={BarChart3} title="No requests in this range" />
        ) : (
          <Breakdown by={by} groups={breakdown.data.groups} />
        )}
      </Panel>
    </div>
  )
}

function Tile({ label, value, meta }: { label: string; value: string; meta?: string }) {
  return (
    <div className={s.tile}>
      <span className={s.tileLabel}>{label}</span>
      <span className={s.tileValue}>{value}</span>
      {meta && <span className={s.tileMeta}>{meta}</span>}
    </div>
  )
}

function fmtLatency(l: StatsLatency, p: 'p50' | 'p95'): string {
  const v = p === 'p50' ? l.p50_ms : l.p95_ms
  return v == null ? '—' : fmtDuration(v)
}

function GroupLabel({ by, g }: { by: Dim; g: StatsGroup }) {
  if (!g.key) return <span className="muted">(none)</span>
  if (by === 'client') {
    return (
      <Link to="/clients/$bindingId" params={{ bindingId: g.key }} className={s.label} title={g.key}>
        {g.label}
      </Link>
    )
  }
  if (by === 'session') {
    return (
      <Link to="/activity" search={{ session_id: g.key }} className={`mono ${s.label}`} title={g.key}>
        {g.label}
      </Link>
    )
  }
  return (
    <span className={by === 'model' || by === 'intent' ? `mono ${s.label}` : s.label} title={g.key}>
      {g.label}
    </span>
  )
}

function Breakdown({ by, groups }: { by: Dim; groups: StatsGroup[] }) {
  const intent = by === 'intent'
  return (
    <TableScroll label="Statistics breakdown">
      <table className="data-table" style={{ width: '100%' }}>
        <thead>
          <tr>
            <th>{dimensions.find((d) => d.value === by)?.label}</th>
            <th className="num">Requests</th>
            <th className="num">Errors</th>
            <th className="num">Blocked</th>
            <th className="num">Failovers</th>
            <th className="num">Tokens in / out</th>
            <th className="num">
              Unknown usage <InfoTip text="Completed requests whose upstream did not report usage." />
            </th>
            <th>Cost by category</th>
            <th className="num">Duration p50 / p95</th>
            <th className="num">
              Unique / uncertain <InfoTip text="Requests with a known session (history deduplicated) and without one (history may repeat)." />
            </th>
            <th className="num">Tool calls</th>
            {intent && <th className="num">Classifier runs</th>}
            {intent && <th>Classifier cost</th>}
          </tr>
        </thead>
        <tbody>
          {groups.map((g) => (
            <tr key={g.key || '(none)'}>
              <td>
                <GroupLabel by={by} g={g} />
              </td>
              <td className="num">{fmtNumber(g.requests)}</td>
              <td className="num">
                {fmtNumber(g.errors)}
                {g.errors > 0 && <span className={s.sub}>{fmtPercent(errorRate(g), 1)}</span>}
              </td>
              <td className="num">{fmtNumber(g.blocked)}</td>
              <td className="num">{fmtNumber(g.failovers)}</td>
              <td className="num">
                {fmtCompact(g.input_tokens)} / {fmtCompact(g.output_tokens)}
              </td>
              <td className="num">{g.unknown_usage > 0 ? <Badge tone="warning">{fmtNumber(g.unknown_usage)}</Badge> : '0'}</td>
              <td>
                <CostList costs={g.costs} />
              </td>
              <td className="num">
                {fmtLatency(g.duration, 'p50')} / {fmtLatency(g.duration, 'p95')}
              </td>
              <td className="num">
                {fmtNumber(g.unique_requests)} / {fmtNumber(g.uncertain_requests)}
              </td>
              <td className="num">{fmtNumber(g.tool_calls)}</td>
              {intent && (
                <td className="num">
                  {fmtNumber(g.classifier_runs)}
                  {g.classifier_runs > 0 && (
                    <span className={s.sub}>
                      {fmtCompact(g.classifier_tokens)} tokens · p50 {fmtLatency(g.classifier_latency, 'p50')}
                    </span>
                  )}
                </td>
              )}
              {intent && (
                <td>
                  <CostList costs={g.classifier_costs} />
                </td>
              )}
            </tr>
          ))}
        </tbody>
      </table>
    </TableScroll>
  )
}
