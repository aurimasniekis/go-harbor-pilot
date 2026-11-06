import { describe, expect, it } from 'vitest'
import { buildUrl } from '@/lib/api/client'
import { parseRef, refKeyLabel, refKeyOptions, refLabel, refProblem, refsSearch, withRef } from './refs'

describe('parseRef', () => {
  it.each([
    ['spaceman.run:run_1', { key: 'spaceman.run', value: 'run_1' }],
    ['git.ref:refs/heads/main:x', { key: 'git.ref', value: 'refs/heads/main:x' }],
    ['session:abc', { key: 'session', value: 'abc' }],
  ])('%s', (raw, want) => {
    expect(parseRef(raw)).toEqual(want)
  })

  it.each(['spaceman.run', 'spaceman.run:', ':value', 'Spaceman.run:x', `k:${'x'.repeat(257)}`])('rejects %s', (raw) => {
    expect(parseRef(raw)).toBeNull()
  })
})

describe('refKeyLabel', () => {
  it.each([
    ['spaceman.run', 'Run'],
    ['spaceman.space', 'Space'],
    ['session', 'Session'],
    ['project_id', 'Project'],
    ['ci.build_number', 'Build number'],
  ])('%s', (key, want) => {
    expect(refKeyLabel(key)).toBe(want)
  })

  it('labels a filter in plain words', () => {
    expect(refLabel('spaceman.run:run_1')).toBe('Run: run_1')
  })
})

describe('refProblem', () => {
  it('accepts a new reference', () => {
    expect(refProblem('spaceman.run', 'run_1')).toBeNull()
  })
  it('explains what is wrong', () => {
    expect(refProblem('Run', 'x')).toMatch(/lowercase key/)
    expect(refProblem('spaceman.run', ' ')).toMatch(/Enter a value/)
    expect(refProblem('spaceman.run', 'a', ['spaceman.run:a'])).toMatch(/Already/)
    expect(refProblem('e', 'x', ['a:1', 'b:1', 'c:1', 'd:1'])).toMatch(/Up to 4/)
  })
  it('allows replacing a key when the list is full', () => {
    expect(refProblem('a', '2', ['a:1', 'b:1', 'c:1', 'd:1'])).toBeNull()
  })
})

describe('withRef', () => {
  it('appends, and replaces the same key', () => {
    expect(withRef(undefined, { key: 'a', value: '1' })).toEqual(['a:1'])
    expect(withRef(['a:1', 'b:1'], { key: 'a', value: '2' })).toEqual(['b:1', 'a:2'])
  })
})

describe('refsSearch', () => {
  it.each([
    [undefined, undefined],
    ['spaceman.run:run_1', ['spaceman.run:run_1']],
    [['a:1', 'b:2'], ['a:1', 'b:2']],
    [['a:1', 'bad', 'a:2'], ['a:2']],
    [['a:1', 'b:1', 'c:1', 'd:1', 'e:1'], ['a:1', 'b:1', 'c:1', 'd:1']],
    [[], undefined],
    [42, undefined],
  ])('%j', (input, want) => {
    expect(refsSearch.parse(input)).toEqual(want)
  })
})

describe('refKeyOptions', () => {
  it('lists common keys first, then client keys once', () => {
    const keys = refKeyOptions([{ 'spaceman.space': 'x', 'ci.provider': 'y' }, undefined]).map((o) => o.value)
    expect(keys.slice(0, 4)).toEqual(['spaceman.run', 'spaceman.task', 'spaceman.space', 'session'])
    expect(keys.filter((k) => k === 'spaceman.space')).toHaveLength(1)
    expect(keys).toContain('ci.provider')
  })
})

describe('buildUrl', () => {
  it('repeats a parameter for each item of an array', () => {
    expect(buildUrl('/usage/summary', { group_by: 'ref:spaceman.run', ref: ['spaceman.space:s1', 'spaceman.task:t:1'], cursor: undefined })).toBe(
      '/api/v1/usage/summary?group_by=ref%3Aspaceman.run&ref=spaceman.space%3As1&ref=spaceman.task%3At%3A1',
    )
  })
})
