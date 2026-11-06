import { describe, expect, it } from 'vitest'
// Static: importing the dialog inside the test can exceed the 5s test budget on a loaded machine.
import { parseOptions } from './MonitorDialog'
import { presetRange, stepFor } from '@/features/usage/range'
import type { BucketView } from '@/lib/api/types'
import { bucketPercent, bucketValueText, e10, forecastHeadline, historyValue } from './quota'

const base: BucketView = {
  key: 'k', label: 'K', scope: 'account', kind: 'fixed_window', unit: 'requests', reset_semantics: 'unknown', global: true, official: true,
  source: 'adapter', limit: 100, used: 40, remaining: 60, remaining_fraction: 0.6, reset_at: null, source_time: null,
  observed_at: '2026-11-05T12:00:00Z', quality: 'provider_reported', stale: false, exhausted: false, reset_passed: false,
}

describe('quota helpers', () => {
  it('reports unknown remaining as null, never 0', () => {
    expect(bucketPercent({ remaining_fraction: null, reset_passed: false })).toBeNull()
    expect(bucketPercent({ remaining_fraction: 0, reset_passed: false })).toBe(0)
    expect(bucketPercent({ remaining_fraction: 0, reset_passed: true })).toBeNull()
  })
  it('describes bucket values', () => {
    expect(bucketValueText(base)).toBe('60 of 100 requests left')
    expect(bucketValueText({ ...base, remaining: null, limit: null, used: null })).toBe('Remaining unknown')
    expect(bucketValueText({ ...base, unit: 'percent', remaining: 37.5 })).toBe('37.5% left')
    expect(bucketValueText({ ...base, unit: 'currency', currency: 'USD', remaining_amount: '12.50', limit_amount: '50.00' })).toBe('USD 12.50 of 50.00 left')
  })
  it('derives history values', () => {
    expect(historyValue({ t: '', remaining: null, used: 30, limit: 100, reset_at: null })).toBe(70)
    expect(historyValue({ t: '', remaining: null, used: null, limit: 100, reset_at: null })).toBeNull()
  })
  it('formats fixed-point amounts', () => {
    expect(e10(10_500_000)).toBe('0.00105')
    expect(e10(1_119_600_000)).toBe('0.11196')
    expect(e10(25_000_000_000)).toBe('2.50')
    expect(e10(null)).toBeNull()
  })
  it('headlines forecasts', () => {
    const f = { unavailable_reason: 'insufficient data', exhaustion_at: null, exhausts_before_reset: false } as never
    expect(forecastHeadline(f)).toBe('No forecast: insufficient data')
  })
})

describe('monitor options and ranges', () => {
  it('parses adapter options strictly', async () => {
    expect(parseOptions('')).toEqual({ value: {} })
    expect(parseOptions('{"token_source":"login"}').value).toEqual({ token_source: 'login' })
    expect(parseOptions('[1]').error).toMatch(/JSON object/)
    expect(parseOptions('{bad').error).toMatch(/Invalid JSON/)
  })
  it('builds usage ranges on the server clock', async () => {
    const r = presetRange(7, Date.parse('2026-11-05T12:00:30Z'))
    expect(r).toEqual({ from: '2026-10-29T12:01:00.000Z', to: '2026-11-05T12:01:00.000Z' })
    expect(stepFor(7)).toBe('hour')
    expect(stepFor(30)).toBe('day')
  })
})
