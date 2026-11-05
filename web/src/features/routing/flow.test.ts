import { describe, expect, it } from 'vitest'
import type { AccountRes, PlanDocument } from '@/lib/api/types'
import type { Lane } from './graph'
import { docDiff, engineHazards, fallbackSentence, planSummary, sameDocument } from './flow'

const doc = (p: Partial<PlanDocument['fallback']> = {}): PlanDocument =>
  ({ fallback: { steps: ['alternate_account', 'alternate_model', 'stop'], max_attempts: 3, allow_paid_fallback: false, wait_max_seconds: 0, ...p } }) as unknown as PlanDocument

const acct = (name: string) => ({ name, spec: { display_name: '' } }) as unknown as AccountRes
const lane = (kind: 'mapping' | 'default', match: string, members: [string, number][]): Lane => ({
  key: kind + match,
  entry: { kind, match },
  chain: [],
  required: [],
  members: members.map(([n, p]) => ({ accountId: n, account: acct(n), model: 'm', priority: p, weight: 0, blocked: [] })),
})

describe('fallbackSentence', () => {
  it('describes steps in order and the attempt limit', () => {
    expect(fallbackSentence(doc())).toBe('If an attempt fails: try another account with the same model, then try another model in the group. Give up after 3 attempts.')
  })
  it('says when there is no fallback', () => {
    expect(fallbackSentence(doc({ steps: ['stop'] }))).toBe('If an attempt fails, the request fails (no fallback).')
  })
  it('mentions paid fallback', () => {
    expect(fallbackSentence(doc({ steps: ['alternate_provider'], allow_paid_fallback: true }))).toContain('Paid accounts may be used as a fallback.')
  })
})

const withEngine = (engine: unknown, extra: Partial<PlanDocument> = {}): PlanDocument =>
  ({ ...doc(), model_mappings: [], default_alias_id: '', restrictions: {}, engine, ...extra }) as unknown as PlanDocument
const blockAllTable = { tables: [{ name: 'table-1', stage: 'request', hit_policy: 'first', inputs: ['request.harness'], enabled: true, rows: [{ conditions: ['-'], action: 'block' }] }] }

describe('planSummary', () => {
  it('covers every lane, first account and how many more', () => {
    const lanes = [lane('mapping', 'claude-*', [['a', 0]]), lane('default', 'any other model', [['team-2', 1], ['team-1', 0], ['openai', 2]])]
    expect(planSummary(doc(), lanes)).toBe('claude-* → a · anything else → team-1 (then 2 more).')
  })
  it('leads with a rule that blocks every request', () => {
    const lanes = [lane('default', 'any other model', [['team-1', 0]])]
    expect(planSummary(withEngine(blockAllTable), lanes)).toBe('Blocks every request (table table-1 row 1).')
  })
  it('mentions conditional blocking rules and paid fallback', () => {
    const lanes = [lane('default', 'any other model', [['team-1', 0]])]
    const d = withEngine({ rules: [{ name: 'no-codex', stage: 'request', when: 'request.harness == "codex"', action: 'block', enabled: true }] })
    d.fallback.allow_paid_fallback = true
    expect(planSummary(d, lanes)).toBe('Blocks some requests (rule no-codex). anything else → team-1. Paid fallback allowed.')
  })
  it('ignores rules that are turned off', () => {
    const lanes = [lane('default', 'any other model', [['team-1', 0]])]
    const off = { tables: [{ ...blockAllTable.tables[0], enabled: false }] }
    expect(planSummary(withEngine(off), lanes)).toBe('anything else → team-1.')
  })
  it('explains a plan without a live version', () => {
    expect(planSummary(undefined, [])).toMatch(/^Not live yet/)
  })
})

describe('engineHazards', () => {
  it('flags a match-everything block row', () => {
    expect(engineHazards(withEngine(blockAllTable))).toEqual(['Table table-1 row 1 matches every request and blocks it.'])
  })
  it('is quiet for conditional rows and other actions', () => {
    const t = { tables: [{ ...blockAllTable.tables[0], rows: [{ conditions: ['value == "codex"'], action: 'block' }, { conditions: ['-'], action: 'set_intent', intent: 'x' }] }] }
    expect(engineHazards(withEngine(t))).toEqual([])
  })
})

describe('docDiff and sameDocument', () => {
  const base = withEngine(undefined, { model_mappings: [{ match: 'claude-*', alias_id: 'a1', note: '' }], default_alias_id: 'a1' } as Partial<PlanDocument>)
  const name = (id: string) => ({ a1: 'coding/default', a2: 'coding/fast' })[id] ?? id
  it('reports nothing for identical documents', () => {
    expect(docDiff(base, structuredClone(base), name)).toEqual([])
    expect(sameDocument(base, JSON.parse(JSON.stringify(base)) as PlanDocument)).toBe(true)
  })
  it('lists mapping, default and rule changes in words', () => {
    const next = withEngine(blockAllTable, { model_mappings: [{ match: 'claude-*', alias_id: 'a2', note: '' }, { match: 'gpt-*', alias_id: 'a1', note: '' }], default_alias_id: '' } as Partial<PlanDocument>)
    expect(docDiff(base, next, name)).toEqual([
      'Changes claude-*: coding/default → coding/fast',
      'Adds gpt-* → coding/default',
      'Unmapped models are now rejected',
      'Adds decision table table-1',
    ])
    expect(sameDocument(base, next)).toBe(false)
  })
})
