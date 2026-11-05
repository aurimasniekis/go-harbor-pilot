import { describe, expect, it } from 'vitest'
import { detectorLabel, enforcementOf, enforcementSpec, fromLocalInput, hint, masksSummary, segments, shortSecretLines, splitList, toLocalInput, credentialDetectors } from './privacy'

describe('segments', () => {
  it('splits by byte offsets, including multi-byte text before a finding', () => {
    const text = 'naïve key=sk-ant-x end'
    const bytesBefore = new TextEncoder().encode('naïve key=').length
    const s = segments(text, [{ rule: 'anthropic_key', action: 'mask', start: bytesBefore, end: bytesBefore + 8 }])
    expect(s.map((x) => x.text)).toEqual(['naïve key=', 'sk-ant-x', ' end'])
    expect(s[1]?.finding?.rule).toBe('anthropic_key')
  })
  it('ignores out-of-range and overlapping findings', () => {
    const s = segments('abcdef', [
      { rule: 'a', action: 'mask', start: 1, end: 3 },
      { rule: 'b', action: 'mask', start: 2, end: 4 },
      { rule: 'c', action: 'mask', start: 4, end: 99 },
    ])
    expect(s.map((x) => x.text)).toEqual(['a', 'bc', 'def'])
  })
  it('returns the whole text without findings', () => {
    expect(segments('plain', [])).toEqual([{ text: 'plain' }])
  })
})

describe('helpers', () => {
  it('labels detectors and allow-rule suffixes', () => {
    expect(detectorLabel('github_token')).toBe('GitHub tokens')
    expect(detectorLabel('anthropic_key/test-keys')).toBe('Anthropic API keys')
    expect(detectorLabel('my_rule')).toBe('my_rule')
  })
  it('splits lists', () => {
    expect(splitList('a, b\nc,,')).toEqual(['a', 'b', 'c'])
  })
  it('round-trips local date-time inputs', () => {
    const iso = '2026-11-06T10:30:00Z'
    expect(fromLocalInput(toLocalInput(iso))).toBe(iso)
    expect(fromLocalInput('')).toBe('')
  })
})

describe('enforcement', () => {
  it('maps the two flags to one choice and back', () => {
    for (const e of ['block', 'flag', 'send'] as const) expect(enforcementOf(enforcementSpec(e))).toBe(e)
    expect(enforcementOf({ required: false, allow_unmasked_fallback: true })).toBe('send')
  })
})

describe('secret values', () => {
  it('names short non-empty lines', () => {
    expect(shortSecretLines('abc\n\nsupersecretvalue\n  xy  ')).toEqual([1, 4])
    expect(shortSecretLines('')).toEqual([])
  })
})

describe('masksSummary', () => {
  it('says "all" instead of listing every credential type', () => {
    expect(masksSummary({ detectors: credentialDetectors.map((d) => d.id), personal_data: ['email'], rules: [] })).toBe('All credential types · Email addresses')
    expect(masksSummary({ detectors: ['private_key', 'jwt', 'openai_key'], personal_data: [], rules: [{ name: 'x', pattern: 'x', action: 'mask', group: 0 }] }))
      .toBe('3 of 14 credential types · 1 custom rule')
  })
})

describe('hint', () => {
  it('never shows a whole value', () => {
    expect(hint('hunter2hunter2')).toBe('hu…r2 (14 characters)')
    expect(hint('abc')).toBe('•••')
  })
})
