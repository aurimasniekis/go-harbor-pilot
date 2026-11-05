import type { EngineSpec, HitPolicy, RuleAction, RuleSpec, RuleStage, TableRow, TableSpec } from '@/lib/api/types'

export const stages: { value: RuleStage; label: string }[] = [
  { value: 'request', label: 'Before choosing an account' },
  { value: 'candidate', label: 'For each account in the fallback group' },
]

export const actionsByStage: Record<RuleStage, { value: RuleAction; label: string }[]> = {
  request: [
    { value: 'block', label: 'Block the request' },
    { value: 'require', label: 'Require a capability' },
    { value: 'set_intent', label: 'Set the intent' },
  ],
  candidate: [
    { value: 'exclude', label: 'Exclude the candidate' },
    { value: 'boost', label: 'Adjust the score' },
  ],
}

export const hitPolicies: { value: HitPolicy; label: string; help: string }[] = [
  { value: 'first', label: 'First matching row wins', help: 'The first matching row applies.' },
  { value: 'unique', label: 'Only one row may match', help: 'At most one row may match; more than one is a configuration error and nothing applies.' },
  { value: 'collect', label: 'Every matching row applies', help: 'Every matching row applies, in order.' },
]

export function emptyEngine(): EngineSpec {
  return { rules: [], tables: [], score: '' }
}

const str = (v: unknown): string => (typeof v === 'string' ? v : '')
const num = (v: unknown): number | undefined => (typeof v === 'number' && Number.isFinite(v) ? v : undefined)
const stage = (v: unknown): RuleStage => (v === 'candidate' ? 'candidate' : 'request')

function action(v: unknown, st: RuleStage): RuleAction {
  const ok = actionsByStage[st].map((a) => a.value)
  return ok.includes(v as RuleAction) ? (v as RuleAction) : (ok[0] as RuleAction)
}

function rowFrom(v: Record<string, unknown>, st: RuleStage, width: number): TableRow {
  const conds = Array.isArray(v.conditions) ? v.conditions.map(str) : []
  while (conds.length < width) conds.push('-')
  return { conditions: conds.slice(0, Math.max(width, conds.length)), action: action(v.action, st), capability: str(v.capability) || undefined,
    intent: str(v.intent) || undefined, boost: num(v.boost), reason: str(v.reason) || undefined }
}

/** Normalizes a plan document's free-form engine section for editing. */
export function parseEngine(raw: unknown): EngineSpec {
  if (!raw || typeof raw !== 'object') return emptyEngine()
  const o = raw as Record<string, unknown>
  const rules: RuleSpec[] = (Array.isArray(o.rules) ? o.rules : []).map((x: Record<string, unknown>) => {
    const st = stage(x.stage)
    return { name: str(x.name), stage: st, when: str(x.when), action: action(x.action, st), capability: str(x.capability) || undefined,
      intent: str(x.intent) || undefined, boost: num(x.boost), reason: str(x.reason) || undefined, enabled: x.enabled !== false }
  })
  const tables: TableSpec[] = (Array.isArray(o.tables) ? o.tables : []).map((x: Record<string, unknown>) => {
    const st = stage(x.stage)
    const inputs = Array.isArray(x.inputs) ? x.inputs.map(str) : []
    const hp = ['first', 'unique', 'collect'].includes(x.hit_policy as string) ? (x.hit_policy as HitPolicy) : 'first'
    return { name: str(x.name), stage: st, hit_policy: hp, inputs, enabled: x.enabled !== false,
      rows: (Array.isArray(x.rows) ? x.rows : []).map((r: Record<string, unknown>) => rowFrom(r, st, inputs.length)) }
  })
  return { rules, tables, score: str(o.score) }
}

export function engineIsEmpty(e: EngineSpec): boolean {
  return e.rules.length === 0 && e.tables.length === 0 && e.score.trim() === ''
}

/** Strips fields that do not apply to an action so saved documents stay minimal. */
function cleanAction<T extends { action: RuleAction; capability?: string; intent?: string; boost?: number; reason?: string }>(x: T): T {
  const out = { ...x }
  if (out.action !== 'require') delete out.capability
  if (out.action !== 'set_intent') delete out.intent
  if (out.action !== 'boost') delete out.boost
  if (!out.reason) delete out.reason
  return out
}

/** Serializes an engine for the plan document (undefined when empty). */
export function serializeEngine(e: EngineSpec): EngineSpec | undefined {
  if (engineIsEmpty(e)) return undefined
  return {
    rules: e.rules.map((r) => cleanAction({ ...r, name: r.name.trim(), when: r.when.trim() })),
    tables: e.tables.map((t) => ({ ...t, name: t.name.trim(), inputs: t.inputs.map((i) => i.trim()),
      rows: t.rows.map((r) => cleanAction({ ...r, conditions: r.conditions.map((c) => c.trim() || '-') })) })),
    score: e.score.trim(),
  }
}

/**
 * A new rule starts turned off with no condition, so adding one never
 * changes traffic until it is configured and switched on.
 */
export function newRule(existing: RuleSpec[]): RuleSpec {
  return { name: uniqueName('rule', existing.map((r) => r.name)), stage: 'request', when: '', action: 'block', reason: '', enabled: false }
}

/** A new decision table starts turned off and without rows (it never applies). */
export function newTable(existing: string[]): TableSpec {
  return { name: uniqueName('table', existing), stage: 'request', hit_policy: 'first', inputs: ['request.harness'], rows: [], enabled: false }
}

/** True when a table row has no condition (every cell is empty or "-"). */
export function rowMatchesAll(row: TableRow): boolean {
  return row.conditions.every((c) => c.trim() === '' || c.trim() === '-')
}

/** True when an enabled rule or row would block every request. */
export function blocksEverything(x: { stage: RuleStage; action: RuleAction; enabled?: boolean }, matchesAll: boolean): boolean {
  return x.enabled !== false && x.stage === 'request' && x.action === 'block' && matchesAll
}

function uniqueName(prefix: string, taken: string[]): string {
  for (let i = taken.filter((n) => n.startsWith(`${prefix}-`)).length + 1; ; i++) {
    const n = `${prefix}-${i}`
    if (!taken.includes(n)) return n
  }
}

export function move<T>(list: T[], i: number, d: -1 | 1): T[] {
  const j = i + d
  if (j < 0 || j >= list.length) return list
  const out = [...list]
  ;[out[i], out[j]] = [out[j] as T, out[i] as T]
  return out
}

/** Problems the server would report on save/validate that can be shown while editing. */
export function structuralProblems(e: EngineSpec): string[] {
  const out: string[] = []
  const seen = new Set<string>()
  const named = [...e.rules.map((r, i) => ({ n: r.name.trim(), what: `Rule ${i + 1}` })), ...e.tables.map((t, i) => ({ n: t.name.trim(), what: `Table ${i + 1}` }))]
  for (const { n, what } of named) {
    if (!n) out.push(`${what} needs a name.`)
    else if (seen.has(n)) out.push(`The name "${n}" is used more than once; rule and table names must be unique.`)
    seen.add(n)
  }
  const checkAction = (what: string, a: { action: RuleAction; capability?: string; intent?: string }) => {
    if (a.action === 'require' && !a.capability) out.push(`${what}: choose the capability to require.`)
    if (a.action === 'set_intent' && !(a.intent ?? '').trim()) out.push(`${what}: enter the intent to set.`)
  }
  e.rules.forEach((r, i) => {
    checkAction(`Rule ${r.name || i + 1}`, r)
    if (r.enabled && r.stage === 'request' && r.action === 'block' && r.when.trim() === 'true') out.push(`Rule ${r.name || i + 1} blocks every request.`)
  })
  e.tables.forEach((t, i) => {
    if (t.enabled && t.stage === 'request')
      t.rows.forEach((r, j) => {
        if (r.action === 'block' && rowMatchesAll(r)) out.push(`Table ${t.name || i + 1}, row ${j + 1} has no condition, so it blocks every request.`)
      })
    if (t.inputs.length === 0) out.push(`Table ${t.name || i + 1} needs at least one input column.`)
    t.rows.forEach((r, j) => checkAction(`Table ${t.name || i + 1}, row ${j + 1}`, r))
    if (t.hit_policy !== 'collect') {
      const keys = t.rows.map((r) => r.conditions.map((c) => c.trim() || '-').join('\u0000'))
      keys.forEach((k, j) => {
        const first = keys.indexOf(k)
        if (first !== j) out.push(`Table ${t.name || i + 1}: row ${j + 1} repeats row ${first + 1} and can never apply with hit policy ${t.hit_policy}.`)
      })
    }
  })
  return out
}

/** Turns a server location such as body.document.engine.rules[2].when into a readable label. */
export function describeLocation(loc: string, e: EngineSpec): string {
  const m = /^body\.document\.engine\.?(.*)$/.exec(loc)
  if (!m) return loc.replace(/^body\.document\./, '')
  const rest = m[1] ?? ''
  const rule = /^rules\[(\d+)\]\.?(.*)$/.exec(rest)
  if (rule) {
    const i = Number(rule[1])
    return `Rule ${i + 1}${e.rules[i]?.name ? ` (${e.rules[i]?.name})` : ''}${rule[2] ? ` · ${rule[2]}` : ''}`
  }
  const tbl = /^tables\[(\d+)\]\.?(.*)$/.exec(rest)
  if (tbl) {
    const i = Number(tbl[1])
    const sub = (tbl[2] ?? '').replace(/rows\[(\d+)\]/, (_, n: string) => `row ${Number(n) + 1}`).replace(/conditions\[(\d+)\]/, (_, n: string) => `column ${Number(n) + 1}`)
      .replace(/inputs\[(\d+)\]/, (_, n: string) => `input ${Number(n) + 1}`)
      .replace(/\./g, ' · ')
    return `Table ${i + 1}${e.tables[i]?.name ? ` (${e.tables[i]?.name})` : ''}${sub ? ` · ${sub}` : ''}`
  }
  if (rest === 'score') return 'Score expression'
  return rest ? `Engine · ${rest}` : 'Engine'
}
