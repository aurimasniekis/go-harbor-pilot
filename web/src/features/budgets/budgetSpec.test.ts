import { describe, expect, it } from 'vitest'
import { budgetBadge, buildBudgetSpec, canStartOver, type BudgetForm, periodLabel, windowEnd, windowOnPeriodChange } from './budgetSpec'

const form: BudgetForm = {
  scope: 'project',
  scopeID: 'prj_1',
  period: 'month',
  limit: 10,
  currency: 'USD',
  kinds: ['estimated_api_charge'],
  enforcement: 'block',
  warnAt: 80,
  tz: 'Europe/Vilnius',
  startsAt: '2026-11-01T00:00:00.000Z',
  endsAt: '',
  enabled: true,
}

describe('buildBudgetSpec', () => {
  it('sends no window for a recurring budget', () => {
    const s = buildBudgetSpec(form)
    expect(s.timezone).toBe('Europe/Vilnius')
    expect(s).not.toHaveProperty('starts_at')
    expect(s).not.toHaveProperty('ends_at')
  })
  it('sends the window and no time zone for a one-time budget', () => {
    const s = buildBudgetSpec({ ...form, period: 'once', endsAt: '2027-01-01T00:00:00.000Z' })
    expect(s.timezone).toBe('')
    expect(s.starts_at).toBe('2026-11-01T00:00:00.000Z')
    expect(s.ends_at).toBe('2027-01-01T00:00:00.000Z')
  })
  it('maps UTC to the empty time zone', () => {
    expect(buildBudgetSpec({ ...form, tz: 'UTC' }).timezone).toBe('')
  })
})

describe('windowOnPeriodChange', () => {
  const now = new Date('2026-11-08T09:12:44Z')
  it('pre-fills now when an existing recurring budget becomes one time', () => {
    expect(windowOnPeriodChange('once', { startsAt: '', endsAt: '' }, { period: 'month' }, now)).toEqual({ startsAt: '2026-11-08T09:12:44.000Z', endsAt: '' })
  })
  it('keeps an empty start for a new budget (counts from creation)', () => {
    expect(windowOnPeriodChange('once', { startsAt: '', endsAt: '' }, undefined, now)).toEqual({ startsAt: '', endsAt: '' })
  })
  it('keeps a start already chosen', () => {
    expect(windowOnPeriodChange('once', { startsAt: 'x', endsAt: 'y' }, { period: 'month' }, now)).toEqual({ startsAt: 'x', endsAt: 'y' })
  })
  it('clears the window for a recurring period', () => {
    expect(windowOnPeriodChange('week', { startsAt: 'x', endsAt: 'y' }, { period: 'once' }, now)).toEqual({ startsAt: '', endsAt: '' })
  })
  it('restores the saved window when switching back to a saved one-time budget', () => {
    const saved = { period: 'once' as const, starts_at: '2026-10-01T00:00:00Z', ends_at: '2027-01-01T00:00:00Z' }
    const cleared = windowOnPeriodChange('month', { startsAt: saved.starts_at, endsAt: saved.ends_at }, saved, now)
    expect(cleared).toEqual({ startsAt: '', endsAt: '' })
    expect(windowOnPeriodChange('once', cleared, saved, now)).toEqual({ startsAt: saved.starts_at, endsAt: saved.ends_at })
    expect(windowOnPeriodChange('once', cleared, { period: 'once' }, now)).toEqual({ startsAt: '', endsAt: '' })
  })
})

describe('budget rows', () => {
  it('offers start over only on one-time budgets that have not ended', () => {
    expect(canStartOver({ period: 'once', state: 'ok' })).toBe(true)
    expect(canStartOver({ period: 'once', state: 'scheduled' })).toBe(true)
    expect(canStartOver({ period: 'once', state: 'expired' })).toBe(false)
    expect(canStartOver({ period: 'month', state: 'exceeded' })).toBe(false)
  })
  it('labels the period', () => {
    expect(periodLabel('once')).toBe('one time')
    expect(periodLabel('month')).toBe('per month')
  })
  it('maps states to badges', () => {
    expect(budgetBadge({ state: 'scheduled', enforcement: 'block' })).toBe('scheduled')
    expect(budgetBadge({ state: 'expired', enforcement: 'block' })).toBe('ended')
    expect(budgetBadge({ state: 'exceeded', enforcement: 'block' })).toBe('blocking')
    expect(budgetBadge({ state: 'exceeded', enforcement: 'warn' })).toBe('over')
    expect(budgetBadge({ state: 'ok', enforcement: 'block' })).toBe('ok')
  })
  it('describes where the window ends', () => {
    expect(windowEnd({ period: 'month', period_end: '2026-12-01T00:00:00Z', state: 'ok' })).toEqual({ kind: 'resets', iso: '2026-12-01T00:00:00Z' })
    expect(windowEnd({ period: 'once', period_end: null, state: 'ok' })).toEqual({ kind: 'never' })
    expect(windowEnd({ period: 'once', period_end: '2027-01-01T00:00:00Z', state: 'warning' })).toEqual({ kind: 'ends', iso: '2027-01-01T00:00:00Z' })
    expect(windowEnd({ period: 'once', period_end: '2026-01-01T00:00:00Z', state: 'expired' })).toEqual({ kind: 'ended' })
  })
})
