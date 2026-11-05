import { Badge, type Tone } from '@/components/ui'
import { isKnown } from '@/lib/format'

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

export function Ms({ v }: { v: number | null | undefined }) {
  if (!isKnown(v)) return <span className="muted">—</span>
  if (v < 1000) return <span className="num">{v} ms</span>
  return <span className="num">{(v / 1000).toFixed(v < 10000 ? 2 : 1)} s</span>
}

