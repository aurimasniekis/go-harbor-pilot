import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Text } from '@mantine/core'
import { Badge, Banner, KeyValues, Skeleton, Tabs } from '@/components/ui'
import { Advanced, Dialog } from '@/components/form'
import { EChart, type EChartsOption, chartTheme } from '@/components/EChart'
import { QueryError } from '@/components/states'
import { quotaForecastQuery, quotaHistoryQuery } from '@/lib/api/queries'
import type { BucketView, Forecast, HistoryPoint } from '@/lib/api/types'
import { fmtDateTime, fmtNumber, fmtRelative } from '@/lib/format'
import { useCompact } from '@/ui/kit'
import { BucketRow } from './BucketRow'
import { confidenceTone, forecastSentence, historyValue, methodText, rateUnit } from './quota'
import q from './quota.module.css'

const ranges = [
  { value: '24', label: '24 hours' },
  { value: '168', label: '7 days' },
  { value: '720', label: '30 days' },
] as const

function chartOption(hist: HistoryPoint[], fc: Forecast | undefined, unit: string, compact: boolean): EChartsOption {
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
    grid: { left: 56, right: 24, top: compact ? 16 : 40, bottom: compact ? 64 : 32 },
    legend: { ...(compact ? { bottom: 0 } : { top: 0 }), textStyle: { color: t.secondary }, data: ['Observed', 'Forecast range'] },
    tooltip: { trigger: 'axis', backgroundColor: t.raised, borderColor: t.border, textStyle: { color: t.text } },
    xAxis: { type: 'time', axisLine: { lineStyle: { color: t.border } }, axisLabel: { color: t.muted, hideOverlap: true }, splitLine: { show: false } },
    yAxis: {
      type: 'value',
      min: 0,
      axisLabel: { color: t.muted, formatter: (v: number) => (unit === '%' ? `${v}%` : fmtNumber(v)) },
      splitLine: { lineStyle: { color: t.border } },
    },
    series: [
      { name: 'Observed', type: 'line', showSymbol: false, data: observed, connectNulls: false, lineStyle: { color: t.info, width: 2 }, itemStyle: { color: t.info } },
      { name: 'Scenario low', type: 'line', stack: 'band', showSymbol: false, data: low, lineStyle: { opacity: 0 }, tooltip: { show: false } },
      { name: 'Forecast range', type: 'line', stack: 'band', showSymbol: false, data: band, lineStyle: { opacity: 0 }, areaStyle: { color: t.warning, opacity: 0.22 }, itemStyle: { color: t.warning } },
      {
        name: 'Forecast', type: 'line', showSymbol: false, data: mid, smooth: true, lineStyle: { color: t.warning, width: 1, opacity: 0.7 }, itemStyle: { color: t.warning },
        tooltip: { show: true },
        markLine: marks.length
          ? { symbol: 'none', label: { color: t.secondary, formatter: '{b}', position: 'insideEndTop' }, lineStyle: { color: t.muted, type: 'dotted' }, data: marks }
          : undefined,
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
  const compact = useCompact()
  const option = useMemo(() => chartOption(hist.data?.points ?? [], fc.data, unit, compact), [hist.data, fc.data, unit, compact])
  const f = fc.data
  return (
    <Dialog open wide title={`${ownerName} · ${bucket.label}`} onClose={onClose} dirty={false}>
      {f && (
        <Banner tone={f.unavailable_reason ? 'info' : f.exhaustion_at ? 'warning' : 'success'}>
          {forecastSentence(f, bucket.unit, fmtRelative)}
        </Banner>
      )}
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
        {fc.isError ? (
          <QueryError error={fc.error} retry={() => void fc.refetch()} />
        ) : !f ? (
          <Skeleton height={80} />
        ) : (
          <>
            {!f.unavailable_reason && (
              <KeyValues
                items={[
                  ['Uses about', f.rate ? `${fmtNumber(Math.abs(f.rate.per_hour), Math.abs(f.rate.per_hour) < 10 ? 1 : 0)} ${rateUnit(bucket.unit)}` : <span className="muted">Unknown</span>],
                  ['Runs out', f.exhaustion_at ? `${fmtRelative(f.exhaustion_at)} (${fmtDateTime(f.exhaustion_at)})` : <span className="muted">Not within the forecast horizon</span>],
                  ['Resets', f.next_reset_at ? `${fmtRelative(f.next_reset_at)} (${fmtDateTime(f.next_reset_at)})` : <span className="muted">Unknown</span>],
                  ['How sure', <Badge tone={confidenceTone(f.confidence)}>{f.confidence.charAt(0).toUpperCase() + f.confidence.slice(1)}</Badge>],
                  ...(f.stale ? ([['Data', <Badge tone="warning">Out of date</Badge>]] as [React.ReactNode, React.ReactNode][]) : []),
                ]}
              />
            )}
            <Advanced label="How we estimate">
              <Text size="sm">{methodText(f.method)}.</Text>
              {f.rate && (
                <Text size="xs" c="dimmed">
                  Rate from {f.rate.source.replaceAll('_', ' ')}. Starting value {fmtNumber(f.start_value, 1)}
                  {bucket.unit === 'percent' ? '%' : ` ${bucket.unit}`}
                  {f.restored_amount !== null ? `; ${fmtNumber(f.restored_amount, 1)} restored at reset` : ''}.
                </Text>
              )}
              <Text size="xs" c="dimmed">
                Forecasts extrapolate recent consumption; they are estimates, not provider guarantees.
              </Text>
            </Advanced>
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
