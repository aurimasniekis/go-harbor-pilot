import { withRef } from '@/features/refs/refs'

/** The reference key an orchestrator puts on each run's token. */
export const RUN_KEY = 'spaceman.run'

/** Request-log range covering a spend range. */
function logRange(days: number): 1 | 7 | 30 | 'all' {
  return days <= 1 ? 1 : days <= 7 ? 7 : days <= 30 ? 30 : 'all'
}

/**
 * Where a breakdown row leads: the matching requests, keeping the page's
 * reference filter. A reference group (`ref:<key>`) adds its value to it.
 */
export function rowSearch(groupBy: string, key: string, days: number, refs?: string[]): Record<string, unknown> | null {
  if (!key) return null
  const range = logRange(days)
  const ref = refs?.length ? refs : undefined
  if (groupBy.startsWith('ref:')) return { ref: withRef(refs, { key: groupBy.slice(4), value: key }), range }
  switch (groupBy) {
    case 'binding':
      return { binding_id: key, range, ref }
    case 'account':
      return { account_id: key, range, ref }
    case 'project':
      return { project_id: key, range, ref }
    case 'model':
      return { q: key, range, ref }
    default:
      return null
  }
}

/** Segment for a group_by value: a dimension, "run" or "ref". */
export function groupChoice(groupBy: string): string {
  if (groupBy === `ref:${RUN_KEY}`) return 'run'
  if (groupBy.startsWith('ref:')) return 'ref'
  return groupBy
}
