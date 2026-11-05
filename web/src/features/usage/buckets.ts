import type { UsagePoint } from '@/lib/api/types'
import { getDisplayTimeZone } from '@/lib/format'

export type Bucket = 'hour' | 'day' | 'week'

/** Bucket size that keeps a chart readable for a range of days. */
export function bucketFor(days: number): Bucket {
  if (days <= 1) return 'hour'
  if (days <= 31) return 'day'
  return 'week'
}

/** The API step that feeds a bucket (weeks are summed from days). */
export function stepFor(b: Bucket): 'hour' | 'day' {
  return b === 'hour' ? 'hour' : 'day'
}

const HOUR = 3_600_000
const DAY = 86_400_000

function floor(t: number, b: Bucket): number {
  if (b === 'hour') return Math.floor(t / HOUR) * HOUR
  const day = Math.floor(t / DAY) * DAY
  if (b === 'day') return day
  // Weeks start on Monday (UTC).
  const dow = (new Date(day).getUTCDay() + 6) % 7
  return day - dow * DAY
}

function next(t: number, b: Bucket): number {
  return t + (b === 'hour' ? HOUR : b === 'day' ? DAY : 7 * DAY)
}

export interface BucketRow {
  t: number
  attempts: number
  input: number
  output: number
  cacheRead: number
  /** Cost per "kind|currency"; categories are never added together. */
  costs: Record<string, number>
}

/**
 * Every bucket in [from, to), including empty ones, so bars line up with
 * the axis and a quiet period shows as zero instead of disappearing.
 */
export function bucketize(points: UsagePoint[], from: string, to: string, b: Bucket): BucketRow[] {
  const start = floor(new Date(from).getTime(), b)
  const end = new Date(to).getTime()
  const rows = new Map<number, BucketRow>()
  for (let t = start; t < end; t = next(t, b)) rows.set(t, { t, attempts: 0, input: 0, output: 0, cacheRead: 0, costs: {} })
  for (const p of points) {
    const row = rows.get(floor(new Date(p.t).getTime(), b))
    if (!row) continue
    row.attempts += p.attempts
    row.input += p.tokens.input
    row.output += p.tokens.output
    row.cacheRead += p.tokens.cache_read
    for (const c of p.costs) {
      const k = `${c.kind}|${c.currency}`
      row.costs[k] = (row.costs[k] ?? 0) + Number(c.amount)
    }
  }
  return [...rows.values()]
}

/** Axis label for a bucket start. */
export function bucketLabel(t: number, b: Bucket): string {
  const tz = getDisplayTimeZone()
  if (b === 'hour') {
    return new Intl.DateTimeFormat('en-GB', { timeZone: tz, hour: '2-digit', minute: '2-digit', hour12: false }).format(t)
  }
  return new Intl.DateTimeFormat('en-GB', { timeZone: tz, day: 'numeric', month: 'short' }).format(t)
}

/** Tooltip title for a bucket. */
export function bucketTitle(t: number, b: Bucket): string {
  const tz = getDisplayTimeZone()
  if (b === 'hour') {
    return new Intl.DateTimeFormat('en-GB', { timeZone: tz, day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', hour12: false }).format(t)
  }
  const day = new Intl.DateTimeFormat('en-GB', { timeZone: tz, weekday: 'short', day: 'numeric', month: 'short' }).format(t)
  return b === 'week' ? `Week of ${day}` : day
}
