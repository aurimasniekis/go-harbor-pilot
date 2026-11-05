import type { ReactNode } from 'react'
import { Badge, type Tone } from '@/components/ui'
import type { Cost, Tokens } from '@/lib/api/types'
import { fmtMoney, isKnown } from '@/lib/format'

const NOT_REPORTED = 'not reported by upstream'

/** A token count that renders "—" (never 0) when the upstream did not report it. */
export function TokenValue({ v }: { v: number | null | undefined }) {
  if (!isKnown(v)) {
    return (
      <span className="muted" title={NOT_REPORTED} aria-label={NOT_REPORTED}>
        —
      </span>
    )
  }
  return <span className="num">{v.toLocaleString('en-US')}</span>
}

/** Compact in / out / cache-read token triple. */
export function TokenTriple({ t }: { t: Tokens }) {
  return (
    <span style={{ whiteSpace: 'nowrap' }}>
      <TokenValue v={t.input} /> <span className="muted">/</span> <TokenValue v={t.output} />
      {isKnown(t.cache_read) && (
        <>
          {' '}
          <span className="muted">· cache</span> <TokenValue v={t.cache_read} />
        </>
      )}
    </span>
  )
}

const costKindLabel: Record<string, string> = {
  estimated_api_charge: 'est. API charge',
  list_equivalent: 'list-price equivalent — not an invoice',
  actual_api_charge: 'actual API charge',
  subscription_fee: 'subscription fee',
  allocation: 'internal allocation of subscription fees',
  none: 'no cost',
}

export function CostKindLabel({ kind }: { kind: string }) {
  return <span className="muted">{costKindLabel[kind] ?? kind}</span>
}

export function CostValue({ cost }: { cost?: Cost | null }) {
  if (!cost) {
    return (
      <span className="muted" title="No price configured or usage not reported">
        —
      </span>
    )
  }
  return (
    <span>
      <span className="num">{fmtMoney(cost.amount, cost.currency)}</span> <CostKindLabel kind={cost.kind} />
    </span>
  )
}

const statusTone: Record<string, Tone> = { ok: 'success', error: 'error', blocked: 'warning', canceled: 'neutral', in_progress: 'info' }

export function StatusBadge({ status }: { status: string }) {
  return <Badge tone={statusTone[status] ?? 'neutral'}>{status.replace('_', ' ')}</Badge>
}

const protoLabel: Record<string, string> = {
  anthropic_messages: 'Anthropic',
  openai_chat: 'Chat',
  openai_responses: 'Responses',
  gemini: 'Gemini',
  claude_code_bridge: 'Claude Code bridge',
  codex_bridge: 'Codex bridge',
}

export function ProtocolBadge({ p }: { p: string }) {
  if (!p) return <span className="muted">—</span>
  return <Badge title={p}>{protoLabel[p] ?? p}</Badge>
}

const harnessLabel: Record<string, string> = { claude_code: 'Claude Code', codex: 'Codex', opencode: 'OpenCode', pi: 'Pi', generic: 'Generic' }

export function HarnessBadge({ h }: { h: string }) {
  if (!h) return <span className="muted">—</span>
  return (
    <Badge tone={h === 'generic' ? 'neutral' : 'info'} title="Detected from request headers (heuristic, not proof of identity)">
      {harnessLabel[h] ?? h}
    </Badge>
  )
}

const modeTone: Record<string, Tone> = { native: 'success', translated: 'info', passthrough: 'warning' }

export function ModeBadge({ mode }: { mode: string }) {
  if (!mode) return <span className="muted">—</span>
  return <Badge tone={modeTone[mode] ?? 'neutral'}>{mode}</Badge>
}

const confTone: Record<string, Tone> = { high: 'success', medium: 'info', low: 'warning', unknown: 'neutral' }

export function ConfidenceBadge({ c }: { c: string }) {
  return <Badge tone={confTone[c] ?? 'neutral'}>{c || 'unknown'}</Badge>
}

export function Ms({ v }: { v: number | null | undefined }) {
  if (!isKnown(v)) return <span className="muted">—</span>
  if (v < 1000) return <span className="num">{v} ms</span>
  return <span className="num">{(v / 1000).toFixed(v < 10000 ? 2 : 1)} s</span>
}

/** Key/value rows for a string map; renders a dash when empty. */
export function MapTable({ m, label, empty }: { m: Record<string, string> | null | undefined; label: string; empty: ReactNode }) {
  const entries = Object.entries(m ?? {})
  if (entries.length === 0) return <p className="secondary">{empty}</p>
  return (
    <table className="data-table" aria-label={label} style={{ width: '100%' }}>
      <tbody>
        {entries.map(([k, v]) => (
          <tr key={k}>
            <td className="mono muted" style={{ width: '35%' }}>
              {k}
            </td>
            <td className="mono" style={{ overflowWrap: 'anywhere' }}>
              {v}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
