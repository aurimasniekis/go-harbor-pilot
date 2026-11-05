import type { PlanDocument } from '@/lib/api/types'
import type { Lane, LaneMember } from './graph'
import { parseEngine } from './engine'

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

/** One clause per lane: "claude-* → Team key 1 (then 2 more)". */
function laneClause(l: Lane): string {
  const scope = l.entry.kind === 'default' ? 'anything else' : l.entry.match
  if (l.problem) return `${scope} → rejected (${l.problem})`
  const ms = orderedMembers(l).filter((m) => m.blocked.length === 0)
  if (ms.length === 0) return `${scope} → no usable account`
  const more = ms.length > 1 ? ` (then ${ms.length - 1} more)` : ''
  return `${scope} → ${memberName(ms[0]!)}${more}`
}

/**
 * One plain line for a plan: what is blocked first, then where each kind of
 * request goes (at most three lanes), then paid fallback.
 */
export function planSummary(doc: PlanDocument | undefined, lanes: Lane[]): string {
  if (!doc) return 'Not live yet: requests using this plan are rejected until a version is published.'
  const blocks = engineEffects(doc).filter((e) => e.action === 'block')
  const always = blocks.find((e) => e.always)
  if (always) return `Blocks every request (${always.label}).`
  if (lanes.length === 0) return 'Rejects every request: it maps no models and has no default.'
  const shown = lanes.slice(0, 3).map(laneClause)
  if (lanes.length > 3) shown.push(`+${lanes.length - 3} more`)
  const parts = [`${shown.join(' · ')}.`]
  if (blocks.length > 0) parts.unshift(`Blocks some requests (${blocks.map((b) => b.label).join(', ')}).`)
  if (doc.fallback?.allow_paid_fallback) parts.push('Paid fallback allowed.')
  return parts.join(' ')
}

/** An enabled rule or decision-table row and what it does. */
export interface EngineEffect {
  label: string
  stage: 'request' | 'candidate'
  action: string
  /** Matches every request (no condition), so it always applies. */
  always: boolean
  reason?: string
}

const alwaysTrue = (when: string) => ['true', '1 == 1'].includes(when.replace(/\s+/g, ' ').trim())

/** Enabled rules and table rows of a document, in evaluation order. */
export function engineEffects(doc: PlanDocument | undefined): EngineEffect[] {
  const e = parseEngine(doc?.engine)
  const out: EngineEffect[] = []
  for (const r of e.rules) {
    if (!r.enabled) continue
    out.push({ label: `rule ${r.name}`, stage: r.stage, action: r.action, always: alwaysTrue(r.when), reason: r.reason })
  }
  for (const t of e.tables) {
    if (!t.enabled) continue
    t.rows.forEach((row, i) => {
      const always = row.conditions.every((c) => c.trim() === '' || c.trim() === '-')
      out.push({ label: `table ${t.name} row ${i + 1}`, stage: t.stage, action: row.action, always, reason: row.reason })
      // With hit policy "first", rows after a match-everything row never apply.
      if (always && t.hit_policy === 'first') return
    })
  }
  return out
}

/** Problems a person should see before publishing: rules that block everything. */
export function engineHazards(doc: PlanDocument | undefined): string[] {
  return engineEffects(doc)
    .filter((e) => e.action === 'block' && e.always && e.stage === 'request')
    .map((e) => `${e.label[0]!.toUpperCase()}${e.label.slice(1)} matches every request and blocks it.`)
}

function stable(v: unknown): string {
  return JSON.stringify(v, (_k, x: unknown) =>
    x && typeof x === 'object' && !Array.isArray(x) ? Object.fromEntries(Object.entries(x as Record<string, unknown>).sort(([a], [b]) => a.localeCompare(b))) : x,
  )
}

/** True when two documents route identically (ignoring key order). */
export function sameDocument(a: PlanDocument | undefined, b: PlanDocument | undefined): boolean {
  return stable(a ?? null) === stable(b ?? null)
}

/**
 * Plain-language changes from `from` to `to`: mappings, default, rules and
 * tables, fallback, restrictions, privacy and compatibility.
 */
export function docDiff(from: PlanDocument | undefined, to: PlanDocument, aliasName: (id: string) => string = (id) => id): string[] {
  const out: string[] = []
  const f = from
  const maps = (d?: PlanDocument) => new Map((d?.model_mappings ?? []).map((m) => [m.match, m.alias_id]))
  const a = maps(f)
  const b = maps(to)
  for (const [match, id] of b) {
    if (!a.has(match)) out.push(`Adds ${match} → ${aliasName(id)}`)
    else if (a.get(match) !== id) out.push(`Changes ${match}: ${aliasName(a.get(match) ?? '')} → ${aliasName(id)}`)
  }
  for (const [match, id] of a) if (!b.has(match)) out.push(`Removes ${match} → ${aliasName(id)}`)
  if (f && stable(f.model_mappings.map((m) => m.match)) !== stable(to.model_mappings.map((m) => m.match)) && out.length === 0) out.push('Reorders model mappings')
  if ((f?.default_alias_id ?? '') !== (to.default_alias_id ?? '')) {
    out.push(to.default_alias_id ? `Anything else → ${aliasName(to.default_alias_id)}${f?.default_alias_id ? ` (was ${aliasName(f.default_alias_id)})` : ''}` : 'Unmapped models are now rejected')
  }
  const ea = parseEngine(f?.engine)
  const eb = parseEngine(to.engine)
  const named = <T extends { name: string }>(xs: T[]) => new Map(xs.map((x) => [x.name, x]))
  for (const [kind, xa, xb] of [['rule', named(ea.rules), named(eb.rules)], ['decision table', named(ea.tables), named(eb.tables)]] as const) {
    for (const [n, x] of xb as Map<string, { enabled: boolean }>) {
      const old = (xa as Map<string, { enabled: boolean }>).get(n)
      if (!old) out.push(`Adds ${kind} ${n}${x.enabled ? '' : ' (off)'}`)
      else if (old.enabled !== x.enabled) out.push(`${x.enabled ? 'Turns on' : 'Turns off'} ${kind} ${n}`)
      else if (stable(old) !== stable(x)) out.push(`Changes ${kind} ${n}`)
    }
    for (const n of xa.keys()) if (!xb.has(n)) out.push(`Removes ${kind} ${n}`)
  }
  if (ea.score.trim() !== eb.score.trim()) out.push(eb.score.trim() ? 'Changes the score expression' : 'Removes the score expression')
  if (stable(f?.fallback) !== stable(to.fallback)) out.push(`Fallback: ${fallbackSentence(to)}`)
  if (stable(f?.restrictions) !== stable(to.restrictions)) out.push('Changes hard restrictions')
  if (stable(f?.privacy ?? null) !== stable(to.privacy ?? null)) out.push('Changes masking or content capture')
  if (stable(f?.compatibility ?? null) !== stable(to.compatibility ?? null)) out.push('Changes compatibility settings')
  if (stable(f?.intent ?? null) !== stable(to.intent ?? null)) out.push('Changes intent detection')
  return out
}
