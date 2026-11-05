import { isKnown } from '@/lib/format'

/** 22700 → "22.7k"; small numbers stay exact. */
export function short(v: number | null | undefined): string {
  if (!isKnown(v)) return '—'
  const a = Math.abs(v)
  const trim = (x: number) => x.toFixed(x >= 100 ? 0 : 1).replace(/\.0$/, '')
  if (a >= 1e9) return `${trim(v / 1e9)}B`
  if (a >= 1e6) return `${trim(v / 1e6)}M`
  if (a >= 1e4) return `${trim(v / 1e3)}k`
  return v.toLocaleString('en-US')
}

/** Milliseconds as "420 ms" or "1.24 s". */
export function ms(v: number | null | undefined): string {
  if (!isKnown(v)) return '—'
  if (v < 1000) return `${Math.round(v)} ms`
  return `${(v / 1000).toFixed(v < 10_000 ? 2 : 1)} s`
}

const symbols: Record<string, string> = { USD: '$', EUR: '€', GBP: '£' }

/**
 * Money with 2 decimals ("$0.11"); amounts under 0.01 keep 2 significant
 * digits ("$0.0026") so small per-request costs are not shown as zero.
 */
export function money(amount: string | number | null | undefined, currency: string): string {
  if (amount === null || amount === undefined || amount === '') return '—'
  const n = typeof amount === 'number' ? amount : Number(amount)
  if (!Number.isFinite(n)) return `${currency} ${amount}`
  const a = Math.abs(n)
  let body: string
  if (a === 0) body = '0.00'
  else if (a < 0.01) body = Number(a.toPrecision(2)).toString()
  else body = a.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
  const sym = symbols[currency]
  const sign = n < 0 ? '-' : ''
  return sym ? `${sign}${sym}${body}` : `${sign}${currency} ${body}`
}

/** Plain names for cost categories (they are always shown separately). */
export const costKindName: Record<string, string> = {
  estimated_api_charge: 'Estimated cost',
  actual_api_charge: 'Billed cost',
  list_equivalent: 'Subscription value',
  subscription_fee: 'Subscription fees',
  allocation: 'Subscription fees',
  none: 'No cost',
}

/** What each cost category means, for tooltips. */
export const costKindHelp: Record<string, string> = {
  estimated_api_charge: 'Computed by the gateway from token counts and the prices configured on the provider.',
  actual_api_charge: 'Reported by the provider’s billing API.',
  list_equivalent: 'Subscription usage priced at public list prices for comparison. It is not a charge or an invoice.',
  subscription_fee: 'Fixed subscription fees prorated to the range. Not a usage charge.',
  allocation: 'Subscription fees split across the clients and projects that used them.',
  none: 'No price is configured for this model.',
}

/** Tool (harness) names. */
export const harnessName: Record<string, string> = {
  claude_code: 'Claude Code',
  codex: 'Codex',
  opencode: 'OpenCode',
  pi: 'Pi',
  generic: 'API client',
}

/** Human labels for routing and status enums. */
export const reasonName: Record<string, string> = {
  rate_limited: 'Rate limited',
  unavailable: 'Paused',
  cooldown: 'Paused',
  auth_failed: 'Sign-in failed',
  quota_exhausted: 'Quota used up',
  budget_exceeded: 'Over budget',
  capability: 'Missing a capability',
  restricted: 'Not allowed by the plan',
  model_not_allowed: 'Model not allowed on this account',
  compat_breaker_open: 'Protocol issue',
  concurrency: 'Too many requests at once',
  disabled: 'Turned off',
  alternate_account: 'Fell back to another account',
  alternate_model: 'Fell back to another model',
  alternate_provider: 'Fell back to another provider',
  retry: 'Retried',
  wait: 'Waited and retried',
  upstream_error: 'Provider error',
  timeout: 'Timed out',
}

/** "rate_limited" → "Rate limited" for anything not in the dictionary. */
export function human(v: string | null | undefined): string {
  if (!v) return ''
  if (reasonName[v]) return reasonName[v]!
  const t = v.replaceAll('_', ' ')
  return t.charAt(0).toUpperCase() + t.slice(1)
}

export const protocolName: Record<string, string> = {
  anthropic_messages: 'Anthropic Messages',
  openai_chat: 'OpenAI Chat',
  openai_responses: 'OpenAI Responses',
  gemini: 'Gemini',
  claude_code_bridge: 'Claude Code CLI',
  codex_bridge: 'Codex CLI',
}

export const statusName: Record<string, string> = {
  ok: 'OK',
  error: 'Failed',
  blocked: 'Blocked',
  canceled: 'Canceled',
  in_progress: 'Running',
}
