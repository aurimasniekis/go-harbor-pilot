/**
 * Formatting helpers. Every formatter takes `null | undefined` and renders
 * the explicit "Unknown" label, never 0.
 */

export const UNKNOWN = 'Unknown'

let displayTimeZone = 'UTC'

export function setDisplayTimeZone(tz: string): void {
  try {
    new Intl.DateTimeFormat('en-GB', { timeZone: tz })
    displayTimeZone = tz
  } catch {
    displayTimeZone = 'UTC'
  }
}

export function getDisplayTimeZone(): string {
  return displayTimeZone
}

let serverOffsetMs = 0

/**
 * Aligns relative times with the server clock (handles client clock skew
 * and the frozen demo clock).
 */
export function setServerTime(iso: string): void {
  const t = new Date(iso).getTime()
  if (!Number.isNaN(t)) serverOffsetMs = t - Date.now()
}

/** Current time according to the server. */
export function serverNow(): number {
  return Date.now() + serverOffsetMs
}

export function isKnown(v: number | null | undefined): v is number {
  return typeof v === 'number' && Number.isFinite(v)
}

export function fmtNumber(v: number | null | undefined, digits = 0): string {
  if (!isKnown(v)) return UNKNOWN
  return v.toLocaleString('en-US', { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

export function fmtPercent(v: number | null | undefined, digits = 0): string {
  if (!isKnown(v)) return UNKNOWN
  return `${v.toFixed(digits)}%`
}

/** Percentage points (absolute difference of percentages). */
export function fmtCompact(v: number | null | undefined): string {
  if (!isKnown(v)) return UNKNOWN
  const abs = Math.abs(v)
  if (abs >= 1e9) return `${(v / 1e9).toFixed(1)}B`
  if (abs >= 1e6) return `${(v / 1e6).toFixed(1)}M`
  if (abs >= 1e3) return `${(v / 1e3).toFixed(1)}K`
  return v.toFixed(abs < 10 && abs % 1 !== 0 ? 1 : 0)
}

/** Format an exact decimal amount string with a currency code. */
export function fmtMoney(amount: string | null | undefined, currency: string | null | undefined): string {
  if (amount === null || amount === undefined || amount === '') return UNKNOWN
  const n = Number(amount)
  const cur = currency ?? ''
  if (!Number.isFinite(n)) return `${cur} ${amount}`.trim()
  const digits = Math.abs(n) < 1 && n !== 0 ? 4 : 2
  return `${cur} ${n.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: digits })}`.trim()
}

export function fmtRate(rate: number | null | undefined, unit: string, digits = 1): string {
  if (!isKnown(rate)) return UNKNOWN
  return `${rate.toFixed(digits)} ${unit}`
}

export function fmtDateTime(iso: string | null | undefined, opts?: { seconds?: boolean }): string {
  if (!iso) return UNKNOWN
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return UNKNOWN
  return new Intl.DateTimeFormat('en-GB', {
    timeZone: displayTimeZone,
    year: 'numeric',
    month: 'short',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: opts?.seconds ? '2-digit' : undefined,
    hour12: false,
  }).format(d)
}

export function fmtTime(iso: string | null | undefined): string {
  if (!iso) return UNKNOWN
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return UNKNOWN
  return new Intl.DateTimeFormat('en-GB', { timeZone: displayTimeZone, hour: '2-digit', minute: '2-digit', hour12: false }).format(d)
}

/** Human duration such as "2h 11m" or "3d 4h". */
export function fmtDuration(ms: number | null | undefined): string {
  if (!isKnown(ms)) return UNKNOWN
  const neg = ms < 0
  let s = Math.round(Math.abs(ms) / 1000)
  const d = Math.floor(s / 86400)
  s -= d * 86400
  const h = Math.floor(s / 3600)
  s -= h * 3600
  const m = Math.floor(s / 60)
  s -= m * 60
  let out: string
  if (d > 0) out = `${d}d ${h}h`
  else if (h > 0) out = `${h}h ${m}m`
  else if (m > 0) out = `${m}m`
  else out = `${s}s`
  return neg ? `-${out}` : out
}

export function fmtRelative(iso: string | null | undefined, now: number = serverNow()): string {
  if (!iso) return UNKNOWN
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return UNKNOWN
  const diff = t - now
  if (Math.abs(diff) < 45_000) return 'just now'
  return diff > 0 ? `in ${fmtDuration(diff)}` : `${fmtDuration(-diff)} ago`
}

export function fmtWindow(seconds: number | null | undefined, cls?: string): string {
  if (cls === 'balance') return 'Balance'
  if (cls === 'budget') return 'Budget'
  if (!isKnown(seconds)) return cls ?? UNKNOWN
  if (seconds === 5 * 3600) return '5 hours'
  if (seconds === 7 * 86400) return '7 days'
  if (seconds % 86400 === 0) return `${seconds / 86400} days`
  if (seconds % 3600 === 0) return `${seconds / 3600} hours`
  return fmtDuration(seconds * 1000)
}
