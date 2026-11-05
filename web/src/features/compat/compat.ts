import type { Tone } from '@/components/ui'
import type { PlanCompat, WorkStatus } from '@/lib/api/types'

/** Features a plan may pre-authorize losing while degraded. */
export const degradableFeatures: readonly { value: string; label: string; hint: string }[] = [
  { value: 'detailed_usage', label: 'Detailed usage', hint: 'Token counts are read best-effort from the raw stream; they may be missing.' },
  { value: 'tool_statistics', label: 'Tool statistics', hint: 'Tool calls in the response are not inspected or counted.' },
  { value: 'masking', label: 'Masking', hint: "The client's original payload is forwarded unmasked. Refused anyway when the masking policy is required without availability-first fallback." },
  { value: 'intent_inspection', label: 'Intent inspection', hint: 'Response-side intent signals are not inspected.' },
  { value: 'traces', label: 'Traces', hint: 'Content capture is suspended while passthrough serves a request.' },
]

/** Losses native passthrough always incurs; both must be authorized. */
export const passthroughRequired = ['detailed_usage', 'tool_statistics'] as const

export function lossLabel(v: string): string {
  return degradableFeatures.find((f) => f.value === v)?.label ?? v.replace(/_/g, ' ')
}

/** The plan's compatibility section with server defaults applied. */
export function withDefaults(c: PlanCompat | undefined): Required<PlanCompat> {
  return {
    on_breaker_open: c?.on_breaker_open ?? 'fail',
    allowed_losses: c?.allowed_losses ?? [],
    unknown_usage: c?.unknown_usage ?? 'record',
    unenforceable_budget: c?.unenforceable_budget ?? 'block',
  }
}

/** Required losses missing for passthrough to be authorized. */
export function missingForPassthrough(c: PlanCompat | undefined): string[] {
  const d = withDefaults(c)
  if (d.on_breaker_open !== 'passthrough') return []
  return passthroughRequired.filter((l) => !d.allowed_losses.includes(l))
}

/** True when the section equals the server defaults (it can be omitted). */
export function isDefault(c: PlanCompat | undefined): boolean {
  const d = withDefaults(c)
  return d.on_breaker_open === 'fail' && d.allowed_losses.length === 0 && d.unknown_usage === 'record' && d.unenforceable_budget === 'block'
}

export const breakerTone: Record<string, Tone> = { closed: 'success', open: 'error', half_open: 'warning' }
export const breakerLabel: Record<string, string> = { closed: 'Closed', open: 'Open', half_open: 'Half-open' }

/** Effective state after operator overrides. */
export function effectiveState(state: string, override: string): string {
  if (override === 'force_open') return 'open'
  if (override === 'force_closed') return 'closed'
  return state
}

/** "in 1m 20s" style countdown; "due" once passed; "—" when unknown. */
export function countdown(iso: string | null | undefined, now: number): string {
  if (!iso) return '—'
  const ms = new Date(iso).getTime() - now
  if (Number.isNaN(ms)) return '—'
  if (ms <= 0) return 'due now'
  const s = Math.round(ms / 1000)
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const sec = s % 60
  if (h > 0) return `in ${h}h ${m}m`
  if (m > 0) return `in ${m}m ${sec}s`
  return `in ${sec}s`
}

/** Splits a breaker path "inbound>upstream". */
export function splitPath(path: string): { inbound: string; upstream: string } {
  const [inbound = '', upstream = ''] = path.split('>')
  return { inbound, upstream }
}

export const changeKinds: Record<string, string> = {
  new_event: 'New event',
  new_field: 'New field',
  new_enum_value: 'New enum value',
  type_change: 'Type change',
  expected_absent: 'Expected field absent',
  unsupported_sequence: 'Unsupported sequence',
  schema_rejection: 'Schema rejection',
  parse_failure: 'Parse failure',
  restored: 'Compatibility restored',
}

export const impactTone: Record<string, Tone> = { breaking: 'error', additive: 'info', unknown: 'neutral' }
export const confidenceTone: Record<string, Tone> = { high: 'success', medium: 'info', low: 'warning' }

export const workStatuses: readonly { value: WorkStatus; label: string }[] = [
  { value: 'new', label: 'New' },
  { value: 'acknowledged', label: 'Acknowledged' },
  { value: 'investigating', label: 'Investigating' },
  { value: 'fix_available', label: 'Fix available' },
  { value: 'verified', label: 'Verified' },
  { value: 'resolved', label: 'Resolved' },
  { value: 'ignored', label: 'Ignored' },
]

export const workTone: Record<string, Tone> = {
  new: 'warning',
  acknowledged: 'info',
  investigating: 'info',
  fix_available: 'purple',
  verified: 'success',
  resolved: 'success',
  ignored: 'neutral',
}

export function statusLabel(v: string): string {
  return workStatuses.find((s) => s.value === v)?.label ?? v
}

/** Pretty JSON for sanitized structural data (never content). */
export function prettyJSON(v: unknown): string {
  if (v === null || v === undefined) return ''
  try {
    return JSON.stringify(v, null, 2)
  } catch {
    return String(v)
  }
}
