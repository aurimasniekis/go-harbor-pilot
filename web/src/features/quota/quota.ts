import type { BucketView, Forecast, HistoryPoint } from '@/lib/api/types'
import { fmtNumber, isKnown } from '@/lib/format'

/** Remaining percent of a bucket, or null when unknown (never 0 by default). */
export function bucketPercent(b: Pick<BucketView, 'remaining_fraction' | 'reset_passed'>): number | null {
  if (b.reset_passed) return null
  return isKnown(b.remaining_fraction) ? b.remaining_fraction * 100 : null
}

const unitLabel: Record<string, string> = {
  percent: '%',
  tokens: 'tokens',
  requests: 'requests',
  credits: 'credits',
  currency: '',
  other: '',
}

/** "45 of 100 requests left", using exact amounts for monetary buckets. */
export function bucketValueText(b: BucketView): string {
  if (b.unit === 'currency' || b.remaining_amount || b.limit_amount) {
    const cur = b.currency ?? ''
    if (b.remaining_amount && b.limit_amount) return `${cur} ${b.remaining_amount} of ${b.limit_amount} left`.trim()
    if (b.remaining_amount) return `${cur} ${b.remaining_amount} left`.trim()
    if (b.used_amount && b.limit_amount) return `${cur} ${b.used_amount} of ${b.limit_amount} used`.trim()
  }
  const u = unitLabel[b.unit] ?? b.unit
  if (b.unit === 'percent') {
    if (isKnown(b.remaining)) return `${fmtNumber(b.remaining, 1)}% left`
    if (isKnown(b.used)) return `${fmtNumber(b.used, 1)}% used`
    return 'Remaining unknown'
  }
  if (isKnown(b.remaining) && isKnown(b.limit)) return `${fmtNumber(b.remaining)} of ${fmtNumber(b.limit)} ${u} left`
  if (isKnown(b.remaining)) return `${fmtNumber(b.remaining)} ${u} left`
  if (isKnown(b.used) && isKnown(b.limit)) return `${fmtNumber(b.used)} of ${fmtNumber(b.limit)} ${u} used`
  if (isKnown(b.used)) return `${fmtNumber(b.used)} ${u} used`
  return 'Remaining unknown'
}

/** Remaining value of a history point in bucket units, null when unknown. */
export function historyValue(p: HistoryPoint): number | null {
  if (isKnown(p.remaining)) return p.remaining
  if (isKnown(p.limit) && isKnown(p.used)) return p.limit - p.used
  return null
}

/** Converts a 1e-10 fixed-point amount to a decimal string. */
export function e10(n: number | null | undefined): string | null {
  if (!isKnown(n)) return null
  // Up to six decimals, trailing zeros trimmed, at least two kept.
  const fixed = (n / 1e10).toFixed(6)
  return fixed.replace(/(\.\d\d\d*?)0+$/, '$1')
}

export const confidenceTone = (c: string): 'success' | 'warning' | 'neutral' | 'info' =>
  c === 'high' ? 'success' : c === 'medium' ? 'info' : c === 'low' ? 'warning' : 'neutral'

/** Short forecast headline. */
export function forecastHeadline(f: Forecast): string {
  if (f.unavailable_reason) return `No forecast: ${f.unavailable_reason}`
  if (f.exhaustion_at) return f.exhausts_before_reset ? 'Estimated to run out before the next reset' : 'Estimated to run out'
  return 'Not expected to run out within the forecast horizon'
}
