import { describe, expect, it } from 'vitest'
import type { AccountRes, PlanDocument } from '@/lib/api/types'
import type { Lane } from './graph'
import { fallbackSentence, planSummary } from './flow'

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

describe('planSummary', () => {
  it('names the accounts in priority order', () => {
    const lanes = [lane('mapping', 'claude-*', [['a', 0]]), lane('default', 'any other model', [['team-2', 1], ['team-1', 0], ['openai', 2]])]
    expect(planSummary(doc(), lanes)).toBe('Other requests go to team-1 first, then team-2, openai.')
  })
  it('explains a plan without a live version', () => {
    expect(planSummary(undefined, [])).toMatch(/^Not live yet/)
  })
})
