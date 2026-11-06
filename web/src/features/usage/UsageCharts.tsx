import { useMemo } from 'react'
import { useScheme } from '@/lib/theme'
import { EChart, type EChartsOption } from '@/components/EChart'
import { useCompact } from '@/ui/kit'
import { barChartBase, tones } from '@/ui/chart'
import { costKindName, money, short } from '@/ui/fmt'
import { type Bucket, type BucketRow, bucketLabel, bucketTitle } from './buckets'

/**
 * One single-axis bar chart: tokens per bucket (input and output stacked)
 * or cost per bucket (one bar per cost category, never stacked).
 */
export function UsageChart({ rows, bucket, mode, groups }: {
  rows: BucketRow[]
  bucket: Bucket
  mode: 'cost' | 'tokens'
  /** When set, bars stack by group (one cost category, or total tokens). */
  groups?: { label: string; rows: BucketRow[] }[]
}) {
  // Charts read theme colors when the option is built, so rebuild on a theme change.
  const scheme = useScheme()
  const compact = useCompact()
  const option = useMemo((): EChartsOption => {
    const c = tones(scheme)
    const categories = rows.map((r) => bucketLabel(r.t, bucket))
    const titles = rows.map((r) => bucketTitle(r.t, bucket))
    const palette = [c.accent, c.info, c.purple, c.warning, c.success, c.error]
    if (groups && groups.length > 0) {
      // The main cost category (most money overall); other categories are
      // not added on top of it.
      const totals = new Map<string, number>()
      for (const r of rows) for (const [k, v] of Object.entries(r.costs)) totals.set(k, (totals.get(k) ?? 0) + v)
      const main = [...totals.entries()].sort((a, b) => b[1] - a[1])[0]?.[0] ?? 'estimated_api_charge|USD'
      const [kind, currency] = main.split('|') as [string, string]
      return {
        ...barChartBase({
          categories,
          titles,
          yFormatter: (v) => (mode === 'tokens' ? short(v) : money(v, currency)),
          legend: true,
          compact,
        }),
        series: groups.map((g, i) => ({
          name: g.label,
          type: 'bar',
          stack: 'groups',
          data: g.rows.map((r) => (mode === 'tokens' ? r.input + r.output : (r.costs[main] ?? 0))),
          itemStyle: { color: palette[i % palette.length], borderRadius: i === groups.length - 1 ? [3, 3, 0, 0] : 0 },
          barMaxWidth: 28,
        })),
        ...(mode === 'cost' ? { title: { show: false, text: costKindName[kind] ?? kind } } : {}),
      }
    }
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
  }, [rows, bucket, mode, scheme, compact, groups])
  return <EChart option={option} height={compact ? 180 : 240} ariaLabel={mode === 'tokens' ? `Tokens per ${bucket}` : `Cost per ${bucket}`} />
}
