import { describe, expect, it } from 'vitest'
import { money, ms, short } from './fmt'

describe('short', () => {
  it.each([
    [null, '—'],
    [0, '0'],
    [2860, '2,860'],
    [22700, '22.7k'],
    [114000, '114k'],
    [18_500_000, '18.5M'],
  ])('%s → %s', (v, want) => expect(short(v)).toBe(want))
})

describe('money', () => {
  it.each([
    ['0.11196', 'USD', '$0.11'],
    ['0.00258', 'USD', '$0.0026'],
    ['0', 'USD', '$0.00'],
    ['1234.5', 'USD', '$1,234.50'],
    ['0.5', 'EUR', '€0.50'],
    ['3', 'JPY', 'JPY 3.00'],
    [null, 'USD', '—'],
  ])('%s %s → %s', (a, c, want) => expect(money(a, c)).toBe(want))
})

describe('ms', () => {
  it.each([
    [null, '—'],
    [91, '91 ms'],
    [1240, '1.24 s'],
    [12_500, '12.5 s'],
  ])('%s → %s', (v, want) => expect(ms(v)).toBe(want))
})
