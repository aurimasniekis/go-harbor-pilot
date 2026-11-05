import { useMemo } from 'react'
import { EChart, type EChartsOption, chartTheme } from '@/components/EChart'
import type { StatsGroup } from '@/lib/api/types'
import { useCompact } from '@/ui/kit'
import { money, ms, short } from '@/ui/fmt'
import { buildSeries, type Metric } from './stats'

/** Axis value formatter per metric (no axis titles: the legend names the series). */
function axisFormatter(metric: Metric, currency: string): (v: number) => string {
  switch (metric) {
    case 'latency':
      return (v) => ms(v)
    case 'cost':
      return (v) => money(v, currency)
    default:
      return (v) => short(v)
  }
}

/** Day ticks as "5 Nov", hour ticks as "14:00" (the axis spans the chosen range). */
export function tickLabel(value: number, step: 'hour' | 'day'): string {
  const d = new Date(value)
  if (step === 'day' || (d.getHours() === 0 && d.getMinutes() === 0)) {
    return d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short' })
  }
  return d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })
}

/** Activity per period for one metric, over exactly the selected range. */
export function StatsChart({ groups, metric, range, step }: {
  groups: StatsGroup[]
  metric: Metric
  range: { from: string; to: string }
  step: 'hour' | 'day'
}) {
  const compact = useCompact()
  const option = useMemo((): EChartsOption => {
    const t = chartTheme()
    const built = buildSeries(groups, metric)
    const currency = built.find((s) => s.currency)?.currency ?? 'USD'
    // Lines with one or two points are invisible without symbols.
    const sparse = groups.length < 3
    const series = built.map((s) => ({
      name: s.name,
      type: s.kind,
      stack: s.stack,
      yAxisIndex: s.secondary ? 1 : 0,
      showSymbol: s.kind === 'line' ? sparse : undefined,
      symbolSize: 7,
      connectNulls: false,
      barMaxWidth: 28,
      data: s.data,
      itemStyle: { color: t[s.tone] },
      lineStyle: s.kind === 'line' ? { color: t[s.tone], width: 2 } : undefined,
    }))
    const fmt = axisFormatter(metric, currency)
    const hasSecondary = built.some((s) => s.secondary)
    const axis = (split: boolean, f: (v: number) => string) => ({
      type: 'value' as const,
      axisLabel: { color: t.muted, formatter: f },
      splitLine: split ? { lineStyle: { color: t.border } } : { show: false },
    })
    return {
      textStyle: { fontFamily: t.sans, color: t.secondary },
      grid: { left: 64, right: hasSecondary ? 56 : 24, top: compact ? 16 : 40, bottom: compact ? 72 : 32 },
      legend: compact
        ? { bottom: 0, type: 'scroll', textStyle: { color: t.secondary } }
        : { top: 0, type: 'scroll', textStyle: { color: t.secondary } },
      tooltip: {
        trigger: 'axis',
        backgroundColor: t.raised,
        borderColor: t.border,
        textStyle: { color: t.text },
        valueFormatter: (v: unknown) => (typeof v === 'number' ? fmt(v) : '—'),
      },
      xAxis: {
        type: 'time',
        min: range.from,
        max: range.to,
        axisLine: { lineStyle: { color: t.border } },
        axisLabel: { color: t.muted, hideOverlap: true, formatter: (v: number) => tickLabel(v, step) },
      },
      yAxis: hasSecondary ? [axis(true, fmt), axis(false, (v) => short(v))] : [axis(true, fmt)],
      series,
    }
  }, [groups, metric, range.from, range.to, step, compact])
  const what: Record<Metric, string> = { requests: 'Requests', tokens: 'Tokens', latency: 'Latency', cost: 'Cost' }
  return <EChart option={option} height={compact ? 280 : 300} ariaLabel={`${what[metric]} per ${step}`} />
}
