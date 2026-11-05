import { describe, expect, it } from 'vitest'
import type { StatsGroup } from '@/lib/api/types'
import { buildSeries, errorRate, uniqueShare } from './stats'

const lat = { count: 0, avg_ms: null, p50_ms: null, p95_ms: null }

function group(key: string, over: Partial<StatsGroup>): StatsGroup {
  return {
    key, label: key, requests: 0, ok: 0, errors: 0, blocked: 0, attempts: 0, failovers: 0, passthrough: 0, compat_failures: 0,
    input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0, reasoning_tokens: 0, unknown_usage: 0,
    costs: [], tools_offered: 0, tool_calls: 0, tool_results_unique: 0, tool_result_errors: 0, unique_requests: 0, uncertain_requests: 0,
    new_messages: 0, repeated_messages: 0, ttft: lat, duration: lat, classifier_runs: 0, classifier_tokens: 0, classifier_latency: lat,
    classifier_costs: [], ...over,
  } as StatsGroup
}

describe('buildSeries', () => {
  it('keeps each cost category and currency in its own series', () => {
    const gs = [
      group('2026-11-01T00:00:00Z', { costs: [{ kind: 'estimated_api_charge', currency: 'USD', amount: '1.5' }, { kind: 'list_equivalent', currency: 'USD', amount: '3' }] }),
      group('2026-11-02T00:00:00Z', { costs: [{ kind: 'estimated_api_charge', currency: 'EUR', amount: '2' }] }),
    ]
    const s = buildSeries(gs, 'cost')
    expect(s.map((x) => x.name)).toEqual([
      'Estimated API charge (EUR)',
      'Estimated API charge (USD)',
      'List-price equivalent (not an invoice) (USD)',
    ])
    expect(s[1]!.data).toEqual([
      ['2026-11-01T00:00:00Z', 1.5],
      ['2026-11-02T00:00:00Z', 0],
    ])
  })

  it('stacks outcomes and keeps unknown latency as gaps', () => {
    const gs = [group('a', { ok: 3, errors: 1, blocked: 2 })]
    const req = buildSeries(gs, 'requests')
    expect(req.filter((x) => x.stack === 'r').map((x) => x.data[0]![1])).toEqual([3, 1, 2])
    expect(buildSeries(gs, 'latency')[0]!.data[0]![1]).toBeNull()
  })
})

describe('rates', () => {
  it('returns null without data', () => {
    expect(errorRate({ requests: 0, errors: 0 })).toBeNull()
    expect(uniqueShare({ unique_requests: 0, uncertain_requests: 0 })).toBeNull()
    expect(errorRate({ requests: 4, errors: 1 })).toBe(25)
    expect(uniqueShare({ unique_requests: 3, uncertain_requests: 1 })).toBe(75)
  })
})
