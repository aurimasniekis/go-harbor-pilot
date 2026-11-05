import { useMemo } from 'react'
import { EChart, type EChartsOption, chartTheme } from '@/components/EChart'
import type { UsagePoint } from '@/lib/api/types'

const kindLabel: Record<string, string> = {
  estimated_api_charge: 'Estimated API charge',
  actual_api_charge: 'Actual API charge',
  list_equivalent: 'List-price equivalent (not an invoice)',
}

/** Tokens per step as bars and cost per category and currency as lines (separate axis). */
export function UsageChart({ points }: { points: UsagePoint[] }) {
  const option = useMemo((): EChartsOption => {
    const t = chartTheme()
    const series: Record<string, unknown>[] = [
      { name: 'Input tokens', type: 'bar', stack: 'tokens', data: points.map((p) => [p.t, p.tokens.input]), itemStyle: { color: t.info } },
      { name: 'Output tokens', type: 'bar', stack: 'tokens', data: points.map((p) => [p.t, p.tokens.output]), itemStyle: { color: t.purple } },
    ]
    const keys = new Set<string>()
    for (const p of points) for (const c of p.costs) keys.add(`${c.kind}|${c.currency}`)
    const colors = [t.accent, t.warning, t.success, t.error]
    ;[...keys].sort().forEach((k, i) => {
      const [kind, cur] = k.split('|') as [string, string]
      series.push({
        name: `${kindLabel[kind] ?? kind} (${cur})`,
        type: 'line',
        yAxisIndex: 1,
        showSymbol: false,
        data: points.map((p) => [p.t, Number(p.costs.find((c) => c.kind === kind && c.currency === cur)?.amount ?? 0)]),
        lineStyle: { color: colors[i % colors.length], width: 2 },
        itemStyle: { color: colors[i % colors.length] },
      })
    })
    return {
      textStyle: { fontFamily: t.sans, color: t.secondary },
      grid: { left: 64, right: 64, top: 48, bottom: 40 },
      legend: { top: 0, type: 'scroll', textStyle: { color: t.secondary } },
      tooltip: { trigger: 'axis', backgroundColor: t.raised, borderColor: t.border, textStyle: { color: t.text } },
      xAxis: { type: 'time', axisLine: { lineStyle: { color: t.border } }, axisLabel: { color: t.muted } },
      yAxis: [
        { type: 'value', name: 'tokens', nameTextStyle: { color: t.muted }, axisLabel: { color: t.muted }, splitLine: { lineStyle: { color: t.border } } },
        { type: 'value', name: 'cost', nameTextStyle: { color: t.muted }, axisLabel: { color: t.muted }, splitLine: { show: false } },
      ],
      series,
    }
  }, [points])
  return <EChart option={option} height={300} ariaLabel="Tokens and cost per category over time" />
}
