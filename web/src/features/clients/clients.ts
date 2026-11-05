import { useQuery } from '@tanstack/react-query'
import { statsQuery } from '@/lib/api/queries'
import type { Stats } from '@/lib/api/types'
import { serverNow } from '@/lib/format'

type Group = Stats['groups'][number]

/** Per-client activity over the last 24 hours, keyed by client id. */
export function useClientStats(): { byClient: Record<string, Group>; loaded: boolean } {
  // Round to the minute so the query key is stable between renders.
  const to = Math.ceil(serverNow() / 60_000) * 60_000
  const r = { from: new Date(to - 86_400_000).toISOString(), to: new Date(to).toISOString() }
  const q = useQuery({ ...statsQuery(r, 'client', 'hour'), refetchInterval: 60_000 })
  const byClient: Record<string, Group> = {}
  for (const g of q.data?.groups ?? []) byClient[g.key] = g
  return { byClient, loaded: !!q.data }
}

/** Estimated cost (or the first cost category) of a stats group. */
export function groupCost(g: Group | undefined): { amount: string; currency: string } | null {
  const c = g?.costs.find((x) => x.kind === 'estimated_api_charge') ?? g?.costs[0]
  return c ? { amount: c.amount, currency: c.currency } : null
}

/** "my-laptop-claude" from "My laptop (Claude)". */
export function slugify(s: string): string {
  return s
    .toLowerCase()
    .normalize('NFKD')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 63)
}
