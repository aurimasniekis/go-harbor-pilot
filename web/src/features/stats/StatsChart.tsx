import { useMemo } from 'react'
import { EChart, type EChartsOption, chartTheme } from '@/components/EChart'
import type { StatsGroup } from '@/lib/api/types'
import { buildSeries, type Metric } from './stats'

const axisName: Record<Metric, [string, string?]> = {
  requests: ['requests'],
  tokens: ['tokens', 'requests'],
  latency: ['ms'],
  cost: ['amount'],
}

/** Activity per period for one metric. */
export function StatsChart({ groups, metric }: { groups: StatsGroup[]; metric: Metric }) {
  const option = useMemo((): EChartsOption => {
    const t = chartTheme()
    const series = buildSeries(groups, metric).map((s) => ({
      name: s.name,
      type: s.kind,
      stack: s.stack,
      yAxisIndex: s.secondary ? 1 : 0,
      showSymbol: false,
      connectNulls: false,
      barMaxWidth: 28,
      data: s.data,
      itemStyle: { color: t[s.tone] },
      lineStyle: s.kind === 'line' ? { color: t[s.tone], width: 2 } : undefined,
    }))
    const [left, right] = axisName[metric]
    const axis = (name: string, split: boolean) => ({
      type: 'value',
      name,
      nameTextStyle: { color: t.muted },
      axisLabel: { color: t.muted },
      splitLine: split ? { lineStyle: { color: t.border } } : { show: false },
    })
    return {
      textStyle: { fontFamily: t.sans, color: t.secondary },
      grid: { left: 64, right: right ? 64 : 24, top: 48, bottom: 40 },
      legend: { top: 0, type: 'scroll', textStyle: { color: t.secondary } },
      tooltip: { trigger: 'axis', backgroundColor: t.raised, borderColor: t.border, textStyle: { color: t.text } },
      xAxis: { type: 'time', axisLine: { lineStyle: { color: t.border } }, axisLabel: { color: t.muted } },
      yAxis: right ? [axis(left, true), axis(right, false)] : [axis(left, true)],
      series,
    }
  }, [groups, metric])
  return <EChart option={option} height={300} ariaLabel={`${metric} per period`} />
}
