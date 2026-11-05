import { describe, expect, it } from 'vitest'
import { globMatches, matchingKinds, patternSuggestions, eventsPhrase, noisePhrase } from './events'
import type { EventKindInfo } from '@/lib/api/types'

const k = (name: string, severity: string): EventKindInfo => ({ name, severity, type: '', subject: '', description: '', subjects: [], data: [] })
const catalog = [k('quota.window.exhausted', 'warning'), k('quota.window.recovered', 'info'), k('routing.account.switched', 'info'), k('budget.threshold.exceeded', 'critical')]

describe('event filters', () => {
  it('matches globs like the server', () => {
    expect(globMatches('quota.*', 'quota.window.exhausted')).toBe(true)
    expect(globMatches('quota.window.*', 'quota.window.recovered')).toBe(true)
    expect(globMatches('*.account.switched', 'routing.account.switched')).toBe(true)
    expect(globMatches('quota.window', 'quota.window.exhausted')).toBe(false)
    expect(globMatches('routing.*.switched', 'routing.account.switched')).toBe(true)
    expect(globMatches('routing.account.switched', 'routing.fallback.selected')).toBe(false)
  })
  it('previews matching kinds with severity', () => {
    expect(matchingKinds({ events: ['quota.*'], min_severity: 'warning' }, catalog).map((x) => x.name)).toEqual(['quota.window.exhausted'])
    expect(matchingKinds({ events: [], min_severity: 'critical' }, catalog).map((x) => x.name)).toEqual(['budget.threshold.exceeded'])
  })
  it('suggests names and area wildcards', () => {
    expect(patternSuggestions(catalog)).toContain('quota.*')
    expect(patternSuggestions(catalog)).toContain('routing.account.switched')
  })
})

describe('eventsPhrase', () => {
  it('turns patterns into plain topics', () => {
    expect(eventsPhrase(['quota.*', 'budget.*', 'provider.account.*'], 'warning')).toBe('Quota, budget and account warnings')
    expect(eventsPhrase([], 'info')).toBe('Every event')
    expect(eventsPhrase(['compat.breaker.*'], 'critical')).toBe('Protocol critical problems')
  })
})

describe('noisePhrase', () => {
  it('reads as one line', () => {
    expect(noisePhrase(1800, 0, true)).toBe('at most once per 30 min · also when resolved')
  })
})
