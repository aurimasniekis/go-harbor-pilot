import { describe, expect, it } from 'vitest'
import { detectorLabel, fromLocalInput, segments, splitList, toLocalInput } from './privacy'

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
