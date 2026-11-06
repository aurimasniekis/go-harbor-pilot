import { describe, expect, it } from 'vitest'
import { resolveTheme } from './theme'

describe('resolveTheme', () => {
  it.each([
    ['auto', true, 'light'],
    ['auto', false, 'dark'],
    ['light', false, 'light'],
    ['dark', true, 'dark'],
  ] as const)('%s with OS light=%s is %s', (pref, osLight, want) => {
    expect(resolveTheme(pref, osLight)).toBe(want)
  })
})
