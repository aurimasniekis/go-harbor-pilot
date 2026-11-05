import type { StatsBy, StatsCost, StatsGroup } from '@/lib/api/types'
import { costKindName } from '@/ui/fmt'

export const dimensions: { value: Exclude<StatsBy, 'period'>; label: string }[] = [
  { value: 'client', label: 'Client' },
  { value: 'harness', label: 'Tool' },
  { value: 'model', label: 'Model' },
  { value: 'account', label: 'Account' },
  { value: 'provider', label: 'Provider' },
  { value: 'plan', label: 'Plan' },
  { value: 'project', label: 'Project' },
  { value: 'intent', label: 'Purpose' },
  { value: 'session', label: 'Session' },
]

export type Metric = 'requests' | 'tokens' | 'latency' | 'cost'

export const metrics: { value: Metric; label: string }[] = [
  { value: 'requests', label: 'Requests' },
  { value: 'tokens', label: 'Tokens' },
  { value: 'latency', label: 'Latency' },
  { value: 'cost', label: 'Cost' },
]

export { costKindName as costKindLabel } from '@/ui/fmt'

export interface Series {
  name: string
  kind: 'bar' | 'line'
  stack?: string
  /** Right axis (second unit). */
  secondary?: boolean
  tone: 'success' | 'error' | 'warning' | 'info' | 'purple' | 'accent' | 'muted'
  data: [string, number | null][]
  /** Currency of a cost series. */
  currency?: string
}

/**
 * Chart series for period groups. Costs get one series per category and
 * currency: categories are never added together.
 */
export function buildSeries(groups: StatsGroup[], metric: Metric): Series[] {
  const pts = <T,>(f: (g: StatsGroup) => T) => groups.map((g) => [g.key, f(g)] as [string, T])
  switch (metric) {
    case 'requests':
      return [
        { name: 'OK', kind: 'bar', stack: 'r', tone: 'success', data: pts((g) => g.ok) },
        { name: 'Errors', kind: 'bar', stack: 'r', tone: 'error', data: pts((g) => g.errors) },
        { name: 'Blocked', kind: 'bar', stack: 'r', tone: 'purple', data: pts((g) => g.blocked) },
        { name: 'Fallbacks', kind: 'line', tone: 'warning', data: pts((g) => g.failovers) },
      ]
    case 'tokens':
      return [
        { name: 'Input', kind: 'bar', stack: 't', tone: 'info', data: pts((g) => g.input_tokens) },
        { name: 'Output', kind: 'bar', stack: 't', tone: 'purple', data: pts((g) => g.output_tokens) },
        { name: 'Cache read', kind: 'line', tone: 'success', data: pts((g) => g.cache_read_tokens) },
        { name: 'Requests with unknown usage', kind: 'line', secondary: true, tone: 'warning', data: pts((g) => g.unknown_usage) },
      ]
    case 'latency':
      return [
        { name: 'Duration p50', kind: 'line', tone: 'info', data: pts((g) => g.duration.p50_ms ?? null) },
        { name: 'Duration p95', kind: 'line', tone: 'warning', data: pts((g) => g.duration.p95_ms ?? null) },
        { name: 'TTFT p50', kind: 'line', tone: 'success', data: pts((g) => g.ttft.p50_ms ?? null) },
        { name: 'TTFT p95', kind: 'line', tone: 'purple', data: pts((g) => g.ttft.p95_ms ?? null) },
      ]
    case 'cost': {
      const keys = new Set<string>()
      for (const g of groups) for (const c of g.costs ?? []) keys.add(`${c.kind}|${c.currency}`)
      const tones: Series['tone'][] = ['accent', 'warning', 'success', 'info', 'purple']
      const currencies = new Set([...keys].map((k) => k.split('|')[1]))
      return [...keys].sort().map((k, i) => {
        const [kind, cur] = k.split('|') as [string, string]
        return {
          name: currencies.size > 1 ? `${costKindName[kind] ?? kind} (${cur})` : (costKindName[kind] ?? kind),
          currency: cur,
          kind: 'line',
          tone: tones[i % tones.length]!,
          data: pts((g) => Number(findCost(g.costs, kind, cur)?.amount ?? 0)),
        }
      })
    }
  }
}

export function findCost(costs: StatsCost[] | null | undefined, kind: string, currency: string): StatsCost | undefined {
  return (costs ?? []).find((c) => c.kind === kind && c.currency === currency)
}

/** Percentage of requests that failed (errors, not blocks), or null without requests. */
export function errorRate(g: Pick<StatsGroup, 'requests' | 'errors'>): number | null {
  return g.requests > 0 ? (100 * g.errors) / g.requests : null
}

/** Percentage of requests with a known session (history deduplicated). */
export function uniqueShare(g: Pick<StatsGroup, 'unique_requests' | 'uncertain_requests'>): number | null {
  const n = g.unique_requests + g.uncertain_requests
  return n > 0 ? (100 * g.unique_requests) / n : null
}

