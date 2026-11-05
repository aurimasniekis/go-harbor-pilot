import type { Finding, PrivacyPolicySpec, TracePolicySpec } from '@/lib/api/types'

export interface DetectorInfo {
  id: string
  label: string
  hint: string
}

/** Built-in credential detectors (internal/privacy/detectors.go). */
export const credentialDetectors: DetectorInfo[] = [
  { id: 'private_key', label: 'Private keys', hint: 'PEM private key blocks (RSA, EC, OpenSSH, PGP).' },
  { id: 'aws_access_key', label: 'AWS access key ids', hint: 'AKIA… / ASIA… identifiers.' },
  { id: 'aws_secret_key', label: 'AWS secret access keys', hint: 'Values assigned to aws_secret_access_key.' },
  { id: 'github_token', label: 'GitHub tokens', hint: 'ghp_, gho_, ghu_, ghs_, ghr_ and fine-grained github_pat_ tokens.' },
  { id: 'anthropic_key', label: 'Anthropic API keys', hint: 'sk-ant-… keys.' },
  { id: 'openai_key', label: 'OpenAI API keys', hint: 'sk-…, sk-proj-… and service account keys.' },
  { id: 'slack_token', label: 'Slack tokens', hint: 'xoxb-, xoxp- and similar tokens.' },
  { id: 'google_api_key', label: 'Google API keys', hint: 'AIza… keys.' },
  { id: 'stripe_key', label: 'Stripe keys', hint: 'sk_live_, rk_test_ and similar keys.' },
  { id: 'jwt', label: 'JSON Web Tokens', hint: 'Signed tokens of the form eyJ….eyJ….sig.' },
  { id: 'secret_assignment', label: 'Secret assignments', hint: 'Values after password=, api_key:, token= and similar.' },
  { id: 'credential_url', label: 'Credentials in URLs', hint: 'The password part of scheme://user:password@host.' },
  { id: 'gateway_token', label: 'Harbor Pilot tokens', hint: 'Gateway and API tokens issued by this server.' },
  { id: 'configured_secrets', label: 'Configured secret values', hint: 'Exact values you list as the policy’s write-only secret values.' },
]

/** Optional personal-data detectors. */
export const personalDetectors: DetectorInfo[] = [
  { id: 'email', label: 'Email addresses', hint: '' },
  { id: 'phone', label: 'Phone numbers', hint: 'Sequences of at least nine digits in phone formats.' },
  { id: 'credit_card', label: 'Payment card numbers', hint: 'Validated with the Luhn checksum.' },
  { id: 'iban', label: 'IBANs', hint: 'Validated with the mod-97 checksum.' },
  { id: 'ip_address', label: 'IPv4 addresses', hint: '' },
]

/** Heuristic detectors (warn only by default). */
export const heuristicDetectors: DetectorInfo[] = [
  { id: 'high_entropy', label: 'High-entropy strings', hint: 'Long tokens mixing upper case, lower case and digits. Supplementary: warns by default.' },
]

export const allDetectors = [...credentialDetectors, ...personalDetectors, ...heuristicDetectors]

export function detectorLabel(id: string): string {
  const base = id.split('/')[0] ?? id
  const d = allDetectors.find((x) => x.id === base)
  return d ? d.label : id
}

export type PrivacyAction = 'mask' | 'block' | 'warn' | 'allow'

export function actionTone(a: string): 'error' | 'warning' | 'info' | 'success' | 'neutral' {
  switch (a) {
    case 'block':
      return 'error'
    case 'mask':
      return 'info'
    case 'warn':
      return 'warning'
    case 'allow':
      return 'success'
  }
  return 'neutral'
}

/** A new policy: all credential detectors, masking, restoring replies. */
export function defaultPrivacySpec(): PrivacyPolicySpec {
  return {
    description: '',
    required: true,
    allow_unmasked_fallback: false,
    detectors: credentialDetectors.map((d) => d.id),
    personal_data: [],
    supplementary: [],
    detector_actions: {},
    rules: [],
    allow: [],
    restore_in_responses: true,
    tool_arg_restore: { enabled: false, tools: [], fields: [], allowed_hosts: [] },
    secret_values_ref: '',
  }
}

export const traceSections = [
  { value: 'inbound_request', label: 'Client request' },
  { value: 'upstream_request', label: 'Request as sent upstream' },
  { value: 'response', label: 'Response' },
  { value: 'stream_events', label: 'Stream event summary' },
  { value: 'attempts', label: 'Attempts' },
  { value: 'routing', label: 'Routing decision' },
  { value: 'usage', label: 'Usage' },
] as const

export const traceModes: { value: TracePolicySpec['mode']; label: string; short: string; hint: string }[] = [
  { value: 'off', label: 'Nothing (off)', short: 'Off', hint: 'No content is captured. Request details are still recorded in Requests.' },
  { value: 'masked_diagnostic', label: 'Masked excerpts', short: 'Masked excerpts', hint: 'Redacted content, with long texts shortened to their first and last 1,000 characters.' },
  { value: 'masked_full', label: 'Masked full content', short: 'Masked full', hint: 'Complete content with credentials redacted. Recommended with 3-day retention.' },
  { value: 'unredacted', label: 'Unredacted (privileged, time-boxed)', short: 'Unredacted', hint: 'Original content for a limited diagnostic window. Reading it needs the traces.content.unredacted grant.' },
]

export function traceModeLabel(mode: string): string {
  return traceModes.find((m) => m.value === mode)?.short ?? mode
}

export function defaultTraceSpec(): TracePolicySpec {
  return {
    mode: 'masked_full',
    retention: '3d',
    custom_ttl_seconds: 0,
    sections: traceSections.map((s) => s.value),
    max_trace_bytes: 1 << 20,
    max_total_bytes: 512 << 20,
    sample_rate: 0,
    allow_export: false,
    unredacted_until: '',
    redaction_policy_id: '',
  }
}

export const retentionLabel: Record<string, string> = { '24h': '24 hours', '3d': '3 days', '7d': '7 days', custom: 'Custom' }

/** A segment of sample text, highlighted when it carries a finding. */
export interface Segment {
  text: string
  finding?: Finding
}

/**
 * Splits text by findings. The server reports byte offsets into the UTF-8
 * encoding, so offsets are mapped through the encoded bytes.
 */
export function segments(text: string, findings: Finding[]): Segment[] {
  const bytes = new TextEncoder().encode(text)
  const dec = new TextDecoder()
  const sorted = [...findings].filter((f) => f.start >= 0 && f.end <= bytes.length && f.end > f.start).sort((a, b) => a.start - b.start)
  const out: Segment[] = []
  let pos = 0
  for (const f of sorted) {
    if (f.start < pos) continue
    if (f.start > pos) out.push({ text: dec.decode(bytes.slice(pos, f.start)) })
    out.push({ text: dec.decode(bytes.slice(f.start, f.end)), finding: f })
    pos = f.end
  }
  if (pos < bytes.length) out.push({ text: dec.decode(bytes.slice(pos)) })
  return out
}

/** Splits a comma/newline separated list into trimmed items. */
export function splitList(v: string): string[] {
  return v
    .split(/[\n,]/)
    .map((x) => x.trim())
    .filter(Boolean)
}

/** Converts an RFC 3339 instant to a datetime-local input value (local time). */
export function toLocalInput(iso: string | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** Converts a datetime-local value to RFC 3339 (UTC). */
export function fromLocalInput(v: string): string {
  if (!v) return ''
  const d = new Date(v)
  return Number.isNaN(d.getTime()) ? '' : d.toISOString().replace(/\.\d{3}Z$/, 'Z')
}

/** What happens when masking can't run, as one choice instead of two flags. */
export type Enforcement = 'block' | 'flag' | 'send'

export const enforcementOptions: { value: Enforcement; label: string; hint: string; tone: 'success' | 'warning' }[] = [
  { value: 'block', label: 'Block the request', hint: 'Recommended. Nothing leaves Harbor Pilot unmasked.', tone: 'success' },
  { value: 'flag', label: 'Send unmasked and flag it', hint: 'Keeps tools working during incidents; each such request is marked in Requests.', tone: 'warning' },
  { value: 'send', label: 'Send anyway', hint: 'Not recommended. Requests go out unmasked without being flagged.', tone: 'warning' },
]

export function enforcementOf(s: Pick<PrivacyPolicySpec, 'required' | 'allow_unmasked_fallback'>): Enforcement {
  if (!s.required) return 'send'
  return s.allow_unmasked_fallback ? 'flag' : 'block'
}

export function enforcementSpec(e: Enforcement): { required: boolean; allow_unmasked_fallback: boolean } {
  return { required: e !== 'send', allow_unmasked_fallback: e === 'flag' }
}

/** Shortest configured secret value the server masks (and accepts). */
export const MIN_SECRET_VALUE = 6

/** 1-based numbers of non-empty lines too short to be masked. */
export function shortSecretLines(text: string): number[] {
  const out: number[] = []
  text.split('\n').forEach((line, i) => {
    const t = line.trim()
    if (t !== '' && t.length < MIN_SECRET_VALUE) out.push(i + 1)
  })
  return out
}

export function secretLineCount(text: string): number {
  return text.split('\n').filter((l) => l.trim() !== '').length
}

/** "All credential types · Email addresses, Phone numbers · 2 custom rules" */
export function masksSummary(spec: Pick<PrivacyPolicySpec, 'detectors' | 'personal_data' | 'rules'>): string {
  const builtin = spec.detectors ?? []
  const personal = spec.personal_data ?? []
  const parts: string[] = []
  const all = builtin.length === 0 || credentialDetectors.every((d) => builtin.includes(d.id))
  if (all) parts.push('All credential types')
  else if (builtin.length <= 2) parts.push(builtin.map(detectorLabel).join(', '))
  else parts.push(`${builtin.length} of ${credentialDetectors.length} credential types`)
  if (personal.length) parts.push(personal.map(detectorLabel).join(', '))
  const n = spec.rules?.length ?? 0
  if (n) parts.push(`${n} custom ${n === 1 ? 'rule' : 'rules'}`)
  return parts.join(' · ')
}

/** A detected value shown without revealing it: first and last two characters. */
export function hint(value: string): string {
  const v = value.replace(/\s+/g, ' ')
  if (v.length <= 6) return '•'.repeat(Math.max(v.length, 3))
  return `${v.slice(0, 2)}…${v.slice(-2)} (${v.length} characters)`
}

export const MAX_UNREDACTED_MS = 7 * 24 * 3600 * 1000
