import { describe, expect, it } from 'vitest'
import { UNKNOWN, fmtCompact, fmtDuration, fmtMoney, fmtNumber, fmtPercent, fmtRate, fmtRelative, fmtWindow, setServerTime, serverNow } from './format'

describe('unknown vs zero', () => {
  it('renders null/undefined/NaN as Unknown, never as 0', () => {
    for (const v of [null, undefined, Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(fmtNumber(v)).toBe(UNKNOWN)
      expect(fmtPercent(v)).toBe(UNKNOWN)
      expect(fmtCompact(v)).toBe(UNKNOWN)
      expect(fmtRate(v, 'pp/h')).toBe(UNKNOWN)
      expect(fmtDuration(v)).toBe(UNKNOWN)
    }
    expect(fmtMoney(null, 'USD')).toBe(UNKNOWN)
    expect(fmtMoney('', 'USD')).toBe(UNKNOWN)
  })

  it('renders real zeros as zero', () => {
    expect(fmtNumber(0)).toBe('0')
    expect(fmtPercent(0)).toBe('0%')
    expect(fmtRate(0, 'pp/h')).toBe('0.0 pp/h')
    expect(fmtMoney('0', 'EUR')).toBe('EUR 0.00')
  })
})

describe('formatting', () => {
  it('formats compact numbers and money', () => {
    expect(fmtCompact(5_226_266)).toBe('5.2M')
    expect(fmtCompact(251_000)).toBe('251.0K')
    expect(fmtMoney('1234.5', 'USD')).toBe('USD 1,234.50')
    expect(fmtMoney('0.0123', 'USD')).toBe('USD 0.0123')
  })

  it('formats durations and windows', () => {
    expect(fmtDuration(2 * 3600_000 + 11 * 60_000)).toBe('2h 11m')
    expect(fmtDuration(3 * 86400_000 + 4 * 3600_000)).toBe('3d 4h')
    expect(fmtDuration(-90_000)).toBe('-1m')
    expect(fmtDuration(91)).toBe('91 ms')
    expect(fmtDuration(423.4)).toBe('423 ms')
    expect(fmtDuration(0)).toBe('0 ms')
    expect(fmtDuration(4_230)).toBe('4.2 s')
    expect(fmtDuration(42_000)).toBe('42s')
    expect(fmtWindow(5 * 3600)).toBe('5 hours')
    expect(fmtWindow(7 * 86400)).toBe('7 days')
    expect(fmtWindow(null, 'balance')).toBe('Balance')
  })

  it('computes relative times against the server clock', () => {
    setServerTime('2026-10-17T12:00:00Z')
    expect(Math.abs(serverNow() - Date.parse('2026-10-17T12:00:00Z'))).toBeLessThan(1000)
    expect(fmtRelative('2026-10-17T11:20:00Z')).toBe('40m ago')
    expect(fmtRelative('2026-10-17T15:30:00Z')).toBe('in 3h 30m')
    expect(fmtRelative('2026-10-17T12:00:10Z')).toBe('just now')
    expect(fmtRelative(null)).toBe(UNKNOWN)
  })
})
