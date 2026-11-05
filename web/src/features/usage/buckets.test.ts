import { describe, expect, it } from 'vitest'
import { bucketFor, bucketize } from './buckets'
import type { UsagePoint } from '@/lib/api/types'

const point = (t: string, input: number, cost?: string): UsagePoint => ({
  t,
  attempts: 1,
  tokens: { input, output: 1, cache_read: 0, cache_write: 0, reasoning: 0, unknown_usage_attempts: 0 },
  costs: cost ? [{ kind: 'estimated_api_charge', currency: 'USD', amount: cost }] : [],
})

describe('bucketFor', () => {
  it.each([
    [1, 'hour'],
    [7, 'day'],
    [30, 'day'],
    [90, 'week'],
  ] as const)('%i days uses %s buckets', (days, want) => {
    expect(bucketFor(days)).toBe(want)
  })
})

describe('bucketize', () => {
  it('fills every bucket, including empty ones', () => {
    const rows = bucketize([point('2026-11-03T00:00:00Z', 5)], '2026-11-01T00:00:00Z', '2026-11-05T00:00:00Z', 'day')
    expect(rows.map((r) => r.input)).toEqual([0, 0, 5, 0])
  })

  it('sums days into Monday-based weeks and keeps cost categories apart', () => {
    // 2026-11-02 is a Monday.
    const rows = bucketize(
      [point('2026-11-02T00:00:00Z', 1, '0.5'), point('2026-11-08T00:00:00Z', 2, '0.25'), point('2026-11-09T00:00:00Z', 4)],
      '2026-11-02T00:00:00Z',
      '2026-11-16T00:00:00Z',
      'week',
    )
    expect(rows).toHaveLength(2)
    expect(rows[0]!.input).toBe(3)
    expect(rows[0]!.costs['estimated_api_charge|USD']).toBeCloseTo(0.75)
    expect(rows[1]!.input).toBe(4)
    expect(rows[1]!.costs).toEqual({})
  })
})
