import { describe, expect, it } from 'vitest'
import type { AccountRes, AliasRes, PlanDocument, PoolRes, ProviderRes } from '@/lib/api/types'
import { scopeProblem } from './ExprEditor'
import { describeLocation, emptyEngine, newRule, newTable, parseEngine, serializeEngine, structuralProblems } from './engine'
import { laneAccounts, planLanes, resolveAlias } from './graph'
import { buildSimulateRequest } from './Simulator'

describe('engine document round trip', () => {
  it('normalizes missing or malformed engine sections', () => {
    expect(parseEngine(undefined)).toEqual(emptyEngine())
    expect(parseEngine('nope')).toEqual(emptyEngine())
    const e = parseEngine({ rules: [{ name: 'x', stage: 'candidate', when: 'true', action: 'block' }], tables: [{ name: 't', inputs: ['a', 'b'], rows: [{ conditions: ['1'] }] }] })
    // block is not a candidate action: falls back to the first valid one
    expect(e.rules[0]?.action).toBe('exclude')
    expect(e.rules[0]?.enabled).toBe(true)
    // rows are padded to one condition per input with "-"
    expect(e.tables[0]?.rows[0]?.conditions).toEqual(['1', '-'])
    expect(e.tables[0]?.hit_policy).toBe('first')
  })

  it('serializes empty engines as absent and drops fields that do not apply', () => {
    expect(serializeEngine(emptyEngine())).toBeUndefined()
    const out = serializeEngine({
      rules: [{ name: ' a ', stage: 'request', when: ' request.stream ', action: 'set_intent', intent: 'review', boost: 5, capability: 'tools', reason: '', enabled: true }],
      tables: [{ name: 't', stage: 'request', hit_policy: 'first', inputs: [' request.harness '], rows: [{ conditions: [' '], action: 'block', reason: 'no' }], enabled: true }],
      score: '',
    })
    expect(out?.rules[0]).toEqual({ name: 'a', stage: 'request', when: 'request.stream', action: 'set_intent', intent: 'review', enabled: true })
    expect(out?.tables[0]?.inputs).toEqual(['request.harness'])
    expect(out?.tables[0]?.rows[0]).toEqual({ conditions: ['-'], action: 'block', reason: 'no' })
  })

  it('generates unique default names', () => {
    const r1 = newRule([])
    const r2 = newRule([r1])
    expect(r1.name).not.toBe(r2.name)
    expect(newTable(['table-1']).name).toBe('table-2')
  })
})

describe('structural problems', () => {
  it('reports duplicate names, missing action fields and unreachable rows', () => {
    const e = emptyEngine()
    e.rules = [
      { name: 'dup', stage: 'request', when: 'true', action: 'require', enabled: true },
      { name: 'x', stage: 'request', when: 'true', action: 'set_intent', intent: ' ', enabled: true },
    ]
    e.tables = [{ name: 'dup', stage: 'request', hit_policy: 'unique', inputs: ['request.harness'], enabled: true,
      rows: [{ conditions: ['-'], action: 'block' }, { conditions: [''], action: 'block' }] }]
    const p = structuralProblems(e).join('\n')
    expect(p).toContain('"dup" is used more than once')
    expect(p).toContain('choose the capability')
    expect(p).toContain('enter the intent')
    expect(p).toContain('row 2 repeats row 1')
  })

  it('allows repeated rows with hit policy collect', () => {
    const e = emptyEngine()
    e.tables = [{ name: 't', stage: 'request', hit_policy: 'collect', inputs: ['request.harness'], enabled: true,
      rows: [{ conditions: ['-'], action: 'block' }, { conditions: ['-'], action: 'block' }] }]
    expect(structuralProblems(e)).toEqual([])
  })
})

describe('locations and scope', () => {
  const e = parseEngine({ rules: [{ name: 'no-codex', stage: 'request', when: 'x', action: 'block' }], tables: [{ name: 'tbl', inputs: ['a'], rows: [] }] })
  it('turns server locations into readable labels', () => {
    expect(describeLocation('body.document.engine.rules[0].when', e)).toBe('Rule 1 (no-codex) · when')
    expect(describeLocation('body.document.engine.tables[0].rows[2].conditions[0]', e)).toBe('Table 1 (tbl) · row 3 · column 1')
    expect(describeLocation('body.document.engine.score', e)).toBe('Score expression')
    expect(describeLocation('body.document.fallback.steps', e)).toBe('fallback.steps')
  })
  it('flags stage misuse before the server does', () => {
    expect(scopeProblem("candidate.native", 'request')).toMatch(/cannot reference candidate/)
    expect(scopeProblem("candidate.native", 'candidate')).toBeUndefined()
    expect(scopeProblem('value == 1', 'request')).toMatch(/only defined inside decision-table cells/)
    expect(scopeProblem("request.model == 'value'", 'request')).toBeUndefined()
    expect(scopeProblem('value == 1', 'cell')).toBeUndefined()
  })
})

const res = <S,>(id: string, name: string, spec: S) => ({ id, name, spec }) as unknown

describe('decision graph lanes', () => {
  const providers = { p1: res('p1', 'anthropic', { classification: 'official' }), p2: res('p2', 'local', { classification: 'local' }) } as Record<string, ProviderRes>
  const accounts = {
    a1: res('a1', 'sub', { provider_id: 'p1', billing: 'subscription', enabled: true }),
    a2: res('a2', 'api', { provider_id: 'p1', billing: 'metered_api', enabled: true }),
    a3: res('a3', 'box', { provider_id: 'p2', billing: 'local', enabled: false }),
  } as Record<string, AccountRes>
  const pools = { po: res('po', 'main', { strategy: 'ordered', members: [{ account_id: 'a1', model: 'm', priority: 0, weight: 1 }, { account_id: 'a2', model: 'm', priority: 1, weight: 1 }, { account_id: 'a3', model: 'l', priority: 2, weight: 1 }] }) } as Record<string, PoolRes>
  const aliases = {
    base: res('base', 'coding/default', { pool_id: 'po', alias_id: '', required_capabilities: [] }),
    deep: res('deep', 'coding/deep', { pool_id: '', alias_id: 'base', required_capabilities: ['reasoning'] }),
    loop1: res('loop1', 'l1', { pool_id: '', alias_id: 'loop2', required_capabilities: [] }),
    loop2: res('loop2', 'l2', { pool_id: '', alias_id: 'loop1', required_capabilities: [] }),
  } as Record<string, AliasRes>
  const doc: PlanDocument = {
    schema_version: 1,
    model_mappings: [{ match: 'claude-*', alias_id: 'deep', note: '' }, { match: 'x', alias_id: 'loop1', note: '' }],
    default_alias_id: 'base',
    restrictions: { allowed_provider_ids: [], denied_provider_ids: [], allowed_billing: ['subscription', 'local'], allowed_classifications: [], required_capabilities: [] },
    fallback: { steps: ['stop'], max_attempts: 1, allow_paid_fallback: false, wait_max_seconds: 0 },
  }

  it('follows alias chains, collects capabilities and detects cycles', () => {
    expect(resolveAlias('deep', aliases)).toMatchObject({ poolId: 'po', required: ['reasoning'] })
    expect(resolveAlias('loop1', aliases).problem).toBe('alias cycle')
    expect(resolveAlias('nope', aliases).problem).toContain('does not exist')
  })

  it('marks members removed by hard restrictions and configuration', () => {
    const lanes = planLanes(doc, { aliases, pools, accounts, providers })
    expect(lanes.map((l) => l.entry.match)).toEqual(['claude-*', 'x', 'any other model'])
    expect(lanes[0]?.chain.map((a) => a.name)).toEqual(['coding/deep', 'coding/default'])
    const blocked = Object.fromEntries((lanes[0]?.members ?? []).map((m) => [m.accountId, m.blocked]))
    expect(blocked).toEqual({ a1: [], a2: ['billing metered_api not allowed'], a3: ['account disabled'] })
    expect(lanes[1]?.problem).toBe('alias cycle')
    expect(laneAccounts(lanes).map((m) => m.accountId)).toEqual(['a1', 'a2', 'a3'])
  })
})

describe('simulate request', () => {
  const base = { versionId: '', model: ' coding/default ', protocol: 'anthropic_messages' as const, harness: '', stream: true, tools: 'Read, Edit  Bash',
    images: false, intent: '', sessionConfidence: '', healthMode: 'current' as const, at: '', health: {} }
  it('builds a minimal request', () => {
    expect(buildSimulateRequest(base)).toEqual({ model: 'coding/default', protocol: 'anthropic_messages', stream: true, health_mode: 'current', tool_names: ['Read', 'Edit', 'Bash'] })
  })
  it('includes only explicit health overrides and converts percent to a fraction', () => {
    const req = buildSimulateRequest({ ...base, versionId: 'pvr_1', health: {
      a1: { choice: 'unavailable', remaining: '', stale: false },
      a2: { choice: 'available', remaining: '25', stale: true },
      a3: { choice: 'current', remaining: '50', stale: false },
    } })
    expect(req.version_id).toBe('pvr_1')
    expect(req.health_overrides).toEqual({ a1: { available: false, reason: 'simulated unavailable' }, a2: { available: true, remaining: 0.25, stale: true } })
  })
})
