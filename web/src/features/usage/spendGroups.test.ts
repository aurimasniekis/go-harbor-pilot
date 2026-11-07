import { describe, expect, it } from 'vitest'
import { groupChoice, rowSearch } from './spendGroups'

describe('groupChoice', () => {
  it.each([
    ['account', 'account'],
    ['ref:spaceman.run', 'run'],
    ['ref:spaceman.task', 'ref'],
  ])('%s', (groupBy, want) => {
    expect(groupChoice(groupBy)).toBe(want)
  })
})

describe('rowSearch', () => {
  it('opens a run’s requests, keeping the page filter', () => {
    expect(rowSearch('ref:spaceman.run', 'run_a', 30, ['spaceman.space:s1'])).toEqual({ ref: ['spaceman.space:s1', 'spaceman.run:run_a'], range: 30 })
  })
  it('keeps the reference filter on other groupings', () => {
    expect(rowSearch('binding', 'cbn_1', 7, ['spaceman.run:run_a'])).toEqual({ binding_id: 'cbn_1', range: 7, ref: ['spaceman.run:run_a'] })
    expect(rowSearch('model', 'm', 90)).toEqual({ q: 'm', range: 'all', ref: undefined })
  })
  it('has no link for the group without a value', () => {
    expect(rowSearch('ref:spaceman.run', '', 30)).toBeNull()
  })
})
