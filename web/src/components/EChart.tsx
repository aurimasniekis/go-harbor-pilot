import { useEffect, useRef } from 'react'
import * as echarts from 'echarts/core'
import { BarChart, LineChart, ScatterChart } from 'echarts/charts'
import {
  AriaComponent,
  GridComponent,
  LegendComponent,
  MarkAreaComponent,
  MarkLineComponent,
  MarkPointComponent,
  TooltipComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

echarts.use([
  LineChart,
  BarChart,
  ScatterChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  MarkLineComponent,
  MarkAreaComponent,
  MarkPointComponent,
  AriaComponent,
  CanvasRenderer,
])

export type EChartsOption = echarts.EChartsCoreOption

/** Reads theme tokens from CSS variables so charts follow the active theme. */
export function chartTheme() {
  const cs = getComputedStyle(document.documentElement)
  const v = (n: string, fallback: string) => cs.getPropertyValue(n).trim() || fallback
  return {
    text: v('--text', '#eee6d2'),
    secondary: v('--text-secondary', '#b8af98'),
    muted: v('--text-muted', '#8c979d'),
    border: v('--border', '#2b3b47'),
    surface: v('--surface', '#16212a'),
    raised: v('--surface-raised', '#1d2b36'),
    accent: v('--accent', '#d77a59'),
    success: v('--success', '#72bc87'),
    info: v('--info', '#79aed0'),
    warning: v('--warning', '#d9b65a'),
    error: v('--error', '#d76d6d'),
    purple: v('--purple', '#b49ad6'),
    mono: v('--font-mono', 'monospace'),
    sans: v('--font-sans', 'system-ui'),
  }
}

export const prefersReducedMotion = () =>
  typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches

export function EChart({ option, height = 320, ariaLabel }: { option: EChartsOption; height?: number; ariaLabel: string }) {
  const ref = useRef<HTMLDivElement>(null)
  const chart = useRef<echarts.ECharts | null>(null)

  useEffect(() => {
    if (!ref.current) return
    const c = echarts.init(ref.current, undefined, { renderer: 'canvas' })
    chart.current = c
    const ro = new ResizeObserver(() => c.resize())
    ro.observe(ref.current)
    return () => {
      ro.disconnect()
      c.dispose()
      chart.current = null
    }
  }, [])

  const animated = useRef(false)
  const lastKey = useRef<string | null>(null)

  useEffect(() => {
    const c = chart.current
    if (!c) return
    // Parents may rebuild an identical option on unrelated re-renders
    // (refetches, SSE, scrolling); skip those instead of redrawing.
    let key: string | null = null
    try {
      key = JSON.stringify(option)
    } catch {
      key = null
    }
    if (key !== null && key === lastKey.current) return
    lastKey.current = key
    // Only the first draw animates; later data updates replace the chart
    // in place instead of re-growing every series from zero.
    const animate = !animated.current && !prefersReducedMotion() && option.animation !== false
    c.setOption({ aria: { enabled: true }, ...option, animation: animate }, { notMerge: true })
    animated.current = true
  }, [option])

  return <div ref={ref} role="img" aria-label={ariaLabel} style={{ width: '100%', height }} />
}
