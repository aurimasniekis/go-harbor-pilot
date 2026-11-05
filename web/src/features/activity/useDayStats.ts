import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { statsQuery } from '@/lib/api/queries'
import type { Stats } from '@/lib/api/types'
import { presetRange } from '@/features/usage/range'

type Group = Stats['totals']

/**
 * Activity of the last 24 hours (server clock): totals, the previous 24
 * hours for deltas, and one bucket per hour with empty hours filled in.
 */
export function useDayStats() {
  const range = useMemo(() => presetRange(1), [])
  const prevRange = useMemo(() => {
    const from = new Date(range.from).getTime()
    return { from: new Date(from - 86_400_000).toISOString(), to: range.from }
  }, [range])
  const q = useQuery(statsQuery(range, 'period', 'hour'))
  const prev = useQuery(statsQuery(prevRange, 'period', 'hour'))
  const hours = useMemo(() => {
    if (!q.data) return []
    const byKey = new Map(q.data.groups.map((g) => [g.key, g]))
    const end = new Date(range.to).getTime()
    const start = Math.floor(new Date(range.from).getTime() / 3_600_000) * 3_600_000
    const out: { t: string; g: Group | undefined }[] = []
    for (let t = start; t < end; t += 3_600_000) {
      const key = new Date(t).toISOString().replace('.000Z', 'Z')
      out.push({ t: key, g: byKey.get(key) })
    }
    return out
  }, [q.data, range])
  return { query: q, totals: q.data?.totals, previous: prev.data?.totals, hours }
}

export function series(hours: { g: Group | undefined }[], pick: (g: Group) => number): number[] {
  return hours.map((h) => (h.g ? pick(h.g) : 0))
}

/** "+12% vs previous 24h", or undefined when there is nothing to compare. */
export function delta(now: number, before: number | undefined): string | undefined {
  if (before === undefined || before === 0) return undefined
  const pct = Math.round(((now - before) / before) * 100)
  if (pct === 0) return 'Same as previous 24h'
  return `${pct > 0 ? '+' : ''}${pct}% vs previous 24h`
}
