import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Badge, Banner, KeyValues, Skeleton, Tabs } from '@/components/ui'
import { Dialog } from '@/components/form'
import { EChart, type EChartsOption, chartTheme } from '@/components/EChart'
import { QueryError } from '@/components/states'
import { quotaForecastQuery, quotaHistoryQuery } from '@/lib/api/queries'
import type { BucketView, Forecast, HistoryPoint } from '@/lib/api/types'
import { fmtDateTime, fmtNumber, fmtRate, fmtRelative } from '@/lib/format'
import { BucketRow } from './BucketRow'
import { confidenceTone, forecastHeadline, historyValue } from './quota'
import q from './quota.module.css'

const ranges = [
  { value: '24', label: '24 hours' },
  { value: '168', label: '7 days' },
  { value: '720', label: '30 days' },
] as const

function chartOption(hist: HistoryPoint[], fc: Forecast | undefined, unit: string): EChartsOption {
  const t = chartTheme()
  const observed = hist.map((p) => [p.t, historyValue(p)])
  const pts = fc && !fc.unavailable_reason ? fc.points : []
  const low = pts.map((p) => [p.t, p.low])
  const band = pts.map((p) => [p.t, Math.max(0, p.high - p.low)])
  const mid = pts.map((p) => [p.t, p.remaining])
  const marks: { xAxis: string; name: string }[] = []
  if (fc?.exhaustion_at) marks.push({ xAxis: fc.exhaustion_at, name: 'Estimated exhaustion' })
  if (fc?.next_reset_at) marks.push({ xAxis: fc.next_reset_at, name: 'Next reset' })
  return {
    textStyle: { fontFamily: t.sans, color: t.secondary },
    grid: { left: 56, right: 24, top: 36, bottom: 40 },
    legend: { top: 0, textStyle: { color: t.secondary }, data: ['Observed', 'Forecast (estimate)', 'Scenario band'] },
    tooltip: { trigger: 'axis', backgroundColor: t.raised, borderColor: t.border, textStyle: { color: t.text } },
    xAxis: { type: 'time', axisLine: { lineStyle: { color: t.border } }, axisLabel: { color: t.muted }, splitLine: { show: false } },
    yAxis: { type: 'value', name: unit, nameTextStyle: { color: t.muted }, min: 0, axisLabel: { color: t.muted }, splitLine: { lineStyle: { color: t.border } } },
    series: [
      { name: 'Observed', type: 'line', showSymbol: false, data: observed, connectNulls: false, lineStyle: { color: t.info, width: 2 }, itemStyle: { color: t.info } },
      { name: 'Scenario low', type: 'line', stack: 'band', showSymbol: false, data: low, lineStyle: { opacity: 0 }, tooltip: { show: false } },
      { name: 'Scenario band', type: 'line', stack: 'band', showSymbol: false, data: band, lineStyle: { opacity: 0 }, areaStyle: { color: t.warning, opacity: 0.15 }, itemStyle: { color: t.warning } },
      {
        name: 'Forecast (estimate)', type: 'line', showSymbol: false, data: mid, lineStyle: { color: t.warning, type: 'dashed', width: 2 }, itemStyle: { color: t.warning },
        markLine: marks.length ? { symbol: 'none', label: { color: t.secondary, formatter: '{b}' }, lineStyle: { color: t.muted, type: 'dotted' }, data: marks } : undefined,
      },
    ],
  }
}

/** History and forecast for one quota bucket. Forecasts are estimates. */
export function BucketDialog({ owner, ownerName, bucket, onClose }: { owner: string; ownerName: string; bucket: BucketView; onClose: () => void }) {
  const [hours, setHours] = useState<(typeof ranges)[number]['value']>('168')
  const hist = useQuery(quotaHistoryQuery(owner, bucket.key, Number(hours)))
  const fc = useQuery(quotaForecastQuery(owner, bucket.key))
  const unit = bucket.unit === 'percent' ? '%' : bucket.unit
  const option = useMemo(() => chartOption(hist.data?.points ?? [], fc.data, unit), [hist.data, fc.data, unit])
  const f = fc.data
  return (
    <Dialog open wide title={`${ownerName} · ${bucket.label}`} onClose={onClose}>
      <BucketRow b={bucket} />
      <Tabs label="History range" variant="segmented" value={hours} onChange={setHours} items={ranges.map((r) => ({ value: r.value, label: r.label }))} />
      {hist.isError ? (
        <QueryError error={hist.error} retry={() => void hist.refetch()} />
      ) : !hist.data ? (
        <Skeleton height={300} />
      ) : (hist.data.points ?? []).length === 0 ? (
        <p className="muted">No history recorded for this bucket in the selected range.</p>
      ) : (
        <EChart option={option} height={300} ariaLabel={`Remaining ${bucket.label} over time with forecast`} />
      )}
      <section aria-label="Forecast" className={q.stack}>
        <h3 style={{ fontSize: 'var(--text-md)', margin: 0 }}>
          Forecast <Badge tone="info">Estimate</Badge>
        </h3>
        {fc.isError ? (
          <QueryError error={fc.error} retry={() => void fc.refetch()} />
        ) : !f ? (
          <Skeleton height={80} />
        ) : (
          <>
            <Banner tone={f.unavailable_reason ? 'info' : f.exhaustion_at ? 'warning' : 'info'}>
              {forecastHeadline(f)}. Forecasts extrapolate recent consumption; they are not provider guarantees.
            </Banner>
            {!f.unavailable_reason && (
              <KeyValues
                items={[
                  ['Confidence', <Badge tone={confidenceTone(f.confidence)}>{f.confidence}</Badge>],
                  ['Method', f.method],
                  ['Burn rate', f.rate ? <span>{fmtRate(f.rate.per_hour, `${f.rate.unit}/h`, 2)} <span className="muted">({f.rate.source})</span></span> : <span className="muted">Unknown</span>],
                  ['Starting value', <span className="num">{fmtNumber(f.start_value, 1)}</span>],
                  ['Estimated exhaustion', f.exhaustion_at ? `${fmtDateTime(f.exhaustion_at)} (${fmtRelative(f.exhaustion_at)})` : <span className="muted">not within horizon</span>],
                  ['Next reset', f.next_reset_at ? `${fmtDateTime(f.next_reset_at)} (${fmtRelative(f.next_reset_at)})` : <span className="muted">Unknown</span>],
                  ['Restored at reset', f.restored_amount !== null ? <span className="num">{fmtNumber(f.restored_amount, 1)}</span> : <span className="muted">Unknown</span>],
                  ['Based on stale data', f.stale ? <Badge tone="warning">yes</Badge> : 'no'],
                ]}
              />
            )}
            {f.downtime.length > 0 && (
              <div>
                <div className="muted" style={{ fontSize: 'var(--text-sm)' }}>Predicted unavailable windows</div>
                <ul className={q.list}>
                  {f.downtime.map((d) => (
                    <li key={d.from}>
                      {fmtDateTime(d.from)} → {d.to ? fmtDateTime(d.to) : 'unknown end'}
                    </li>
                  ))}
                </ul>
              </div>
            )}
            {f.caveats.length > 0 && (
              <div>
                <div className="muted" style={{ fontSize: 'var(--text-sm)' }}>Caveats</div>
                <ul className={q.list}>
                  {f.caveats.map((c) => (
                    <li key={c}>{c}</li>
                  ))}
                </ul>
              </div>
            )}
          </>
        )}
      </section>
    </Dialog>
  )
}
