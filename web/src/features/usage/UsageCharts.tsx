import { useMemo } from 'react'
import { useStore } from '@tanstack/react-store'
import { uiStore } from '@/lib/store'
import { EChart, type EChartsOption } from '@/components/EChart'
import { useCompact } from '@/ui/kit'
import { barChartBase, tones } from '@/ui/chart'
import { costKindName, money, short } from '@/ui/fmt'
import { type Bucket, type BucketRow, bucketLabel, bucketTitle } from './buckets'

/**
 * One single-axis bar chart: tokens per bucket (input and output stacked)
 * or cost per bucket (one bar per cost category, never stacked).
 */
export function UsageChart({ rows, bucket, mode }: { rows: BucketRow[]; bucket: Bucket; mode: 'cost' | 'tokens' }) {
  // Charts read theme colors when the option is built, so rebuild on a theme change.
  const scheme = useStore(uiStore, (st) => st.theme)
  const compact = useCompact()
  const option = useMemo((): EChartsOption => {
    const c = tones(scheme)
    const categories = rows.map((r) => bucketLabel(r.t, bucket))
    const titles = rows.map((r) => bucketTitle(r.t, bucket))
    if (mode === 'tokens') {
      return {
        ...barChartBase({ categories, titles, yFormatter: (v) => short(v), legend: true, compact }),
        series: [
          { name: 'Input', type: 'bar', stack: 'tokens', data: rows.map((r) => r.input), itemStyle: { color: c.info }, barMaxWidth: 28 },
          { name: 'Output', type: 'bar', stack: 'tokens', data: rows.map((r) => r.output), itemStyle: { color: c.purple, borderRadius: [3, 3, 0, 0] }, barMaxWidth: 28 },
        ],
      }
    }
    const keys = [...new Set(rows.flatMap((r) => Object.keys(r.costs)))].sort()
    const colors = [c.accent, c.warning, c.success, c.error]
    const currency = keys[0]?.split('|')[1] ?? 'USD'
    return {
      ...barChartBase({ categories, titles, yFormatter: (v) => money(v, currency), legend: keys.length > 1, compact }),
      series: keys.map((k, i) => {
        const [kind, cur] = k.split('|') as [string, string]
        return {
          name: `${costKindName[kind] ?? kind}${keys.length > 1 ? ` (${cur})` : ''}`,
          type: 'bar',
          data: rows.map((r) => r.costs[k] ?? 0),
          itemStyle: { color: colors[i % colors.length], borderRadius: [3, 3, 0, 0] },
          barMaxWidth: 28,
        }
      }),
    }
  }, [rows, bucket, mode, scheme, compact])
  return <EChart option={option} height={compact ? 180 : 240} ariaLabel={mode === 'tokens' ? `Tokens per ${bucket}` : `Cost per ${bucket}`} />
}
