import { chartTheme, type EChartsOption } from '@/components/EChart'

/** Shared look for bar charts: quiet grid, muted axes, themed tooltip. */
export function barChartBase(opts: { categories: string[]; titles: string[]; yFormatter: (v: number) => string; legend?: boolean; compact?: boolean }): EChartsOption {
  const t = chartTheme()
  return {
    textStyle: { fontFamily: t.sans, color: t.secondary, fontSize: 12 },
    grid: { left: 8, right: 8, top: opts.legend && !opts.compact ? 36 : 12, bottom: opts.legend && opts.compact ? 36 : 4, containLabel: true },
    // Legend on top on desktop; below and wrapping (never paginated) on phones.
    legend: opts.legend
      ? {
          ...(opts.compact ? { bottom: 0 } : { top: 0 }),
          left: 0,
          type: 'plain',
          icon: 'roundRect',
          itemWidth: 10,
          itemHeight: 10,
          itemGap: 16,
          textStyle: { color: t.secondary, fontSize: 12 },
        }
      : undefined,
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'shadow', shadowStyle: { color: 'rgba(130,170,255,0.08)' } },
      backgroundColor: t.raised,
      borderColor: t.border,
      textStyle: { color: t.text, fontSize: 12 },
      formatter: (params: unknown) => {
        const ps = params as { dataIndex: number; marker: string; seriesName: string; value: number }[]
        if (!ps.length) return ''
        const head = `<div style="margin-bottom:4px;font-weight:600">${opts.titles[ps[0]!.dataIndex] ?? ''}</div>`
        return head + ps.map((p) => `<div>${p.marker} ${p.seriesName}: <b>${opts.yFormatter(Number(p.value))}</b></div>`).join('')
      },
    },
    xAxis: {
      type: 'category',
      data: opts.categories,
      axisTick: { show: false },
      axisLine: { lineStyle: { color: t.border } },
      axisLabel: { color: t.muted, hideOverlap: true, alignMinLabel: 'left', alignMaxLabel: 'right', ...(opts.compact ? { interval: Math.max(0, Math.ceil(opts.categories.length / 4) - 1) } : {}) },
    },
    yAxis: {
      type: 'value',
      splitNumber: 3,
      axisLabel: { color: t.muted, formatter: (v: number) => opts.yFormatter(v) },
      splitLine: { lineStyle: { color: t.border, opacity: 0.5 } },
    },
  }
}

/** Chart colors for the active theme; pass the scheme so callers re-read on change. */
export function tones(scheme: string) {
  const t = chartTheme()
  t.text = t.text || scheme
  return { accent: t.accent, info: t.info, purple: t.purple, warning: t.warning, success: t.success, error: t.error }
}
