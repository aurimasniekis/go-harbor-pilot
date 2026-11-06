import { describe, expect, it } from 'vitest'
import { friendlyVersion, paletteEntries, searchPalette, shortcutLabel } from './palette'

const all = () => true
const none = (perm?: string) => !perm

describe('palette', () => {
  const entries = paletteEntries(all, 'auto', true)
  it.each([
    ['statistics', 'Spend breakdown'],
    ['account', 'My account'],
    ['quota tracking', 'Quota tracking'],
    ['tracker', 'Add a quota tracker'],
    ['breakdown', 'Spend breakdown'],
    ['add budget', 'Add a budget'],
    ['sign out', 'Sign out'],
    ['password', 'Change password'],
    ['replay', 'Requests'],
  ])('"%s" finds %s', (q, label) => {
    expect(searchPalette(entries, q).map((e) => e.label)).toContain(label)
  })
  it('lists every action with an icon and pages without descriptions', () => {
    for (const e of entries) expect(e.icon).toBeTruthy()
    expect(entries.filter((e) => e.group === 'Pages').every((e) => !e.description)).toBe(true)
  })
  it('hides actions the principal may not use', () => {
    const labels = paletteEntries(none, 'dark', true).map((e) => e.label)
    expect(labels).not.toContain('People')
    expect(labels).toContain('My account')
    expect(labels).toContain('Match system theme')
    expect(labels).not.toContain('Use dark theme')
  })
  it('omits sign out for service tokens', () => {
    expect(paletteEntries(all, 'auto', false).map((e) => e.id)).not.toContain('signout')
  })
})

describe('shortcutLabel and friendlyVersion', () => {
  it.each([
    ['macos', '⌘ K'],
    ['windows', 'Ctrl K'],
    ['linux', 'Ctrl K'],
  ])('%s → %s', (os, want) => expect(shortcutLabel(os)).toBe(want))
  it.each([
    ['0a10179fd61c', 'build 0a10179'],
    ['0a10179fd61c-dirty', 'build 0a10179 (modified)'],
    ['1.4.0', 'v1.4.0'],
    ['v2.0.1', 'v2.0.1'],
    [undefined, ''],
  ])('%s → %s', (v, want) => expect(friendlyVersion(v)).toBe(want))
})
