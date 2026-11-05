import type { PlanDocument } from '@/lib/api/types'
import type { Lane, LaneMember } from './graph'

const stepPhrase: Record<string, string> = {
  retry: 'retry the same account',
  alternate_account: 'try another account with the same model',
  alternate_model: 'try another model in the group',
  alternate_provider: 'try another provider',
  wait: 'wait and retry',
  stop: 'stop',
}

/** "If an attempt fails: … Give up after 3 attempts." */
export function fallbackSentence(doc: PlanDocument): string {
  const steps = (doc.fallback?.steps ?? []).filter((s) => s !== 'stop')
  const attempts = doc.fallback?.max_attempts || 3
  if (steps.length === 0) return 'If an attempt fails, the request fails (no fallback).'
  const list = steps.map((s) => stepPhrase[s] ?? s.replaceAll('_', ' '))
  const joined = list.length === 1 ? list[0] : `${list.slice(0, -1).join(', then ')}, then ${list[list.length - 1]}`
  const paid = doc.fallback?.allow_paid_fallback ? ' Paid accounts may be used as a fallback.' : ''
  return `If an attempt fails: ${joined}. Give up after ${attempts} attempts.${paid}`
}

function memberName(m: LaneMember): string {
  return m.account?.spec.display_name || m.account?.name || m.accountId
}

/** Members in the order they are tried (by priority for ordered groups). */
export function orderedMembers(lane: Lane): LaneMember[] {
  return [...lane.members].sort((a, b) => a.priority - b.priority)
}

/** One plain sentence for a plan: where requests go first and what happens next. */
export function planSummary(doc: PlanDocument | undefined, lanes: Lane[]): string {
  if (!doc) return 'Not live yet: requests using this plan are rejected until a version is published.'
  if (lanes.length === 0) return 'Rejects every request: it maps no models and has no default.'
  const main = lanes.find((l) => l.entry.kind === 'default') ?? lanes[0]!
  const ms = orderedMembers(main).filter((m) => m.blocked.length === 0)
  const who = ms.length === 0 ? 'no usable account' : ms.length === 1 ? memberName(ms[0]!) : `${memberName(ms[0]!)} first, then ${ms.slice(1).map(memberName).join(', ')}`
  const scope = main.entry.kind === 'default' ? (lanes.length > 1 ? 'Other requests' : 'All requests') : `Requests for ${main.entry.match}`
  const paid = doc.fallback?.allow_paid_fallback ? '; paid fallback allowed' : ''
  return `${scope} go to ${who}${paid}.`
}
