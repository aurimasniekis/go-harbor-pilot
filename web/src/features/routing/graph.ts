import type { AccountRes, AliasRes, PlanDocument, PoolRes, ProviderRes } from '@/lib/api/types'

export interface LaneMember {
  accountId: string
  account?: AccountRes
  provider?: ProviderRes
  model: string
  priority: number
  weight: number
  /** Why hard restrictions or configuration rule this member out before ranking. */
  blocked: string[]
}

export interface Lane {
  key: string
  entry: { kind: 'mapping' | 'default'; match: string; note?: string }
  chain: AliasRes[]
  required: string[]
  pool?: PoolRes
  members: LaneMember[]
  problem?: string
}

const MAX_DEPTH = 8

/** Follows an alias chain to its pool, collecting required capabilities. */
export function resolveAlias(aliasId: string, aliases: Record<string, AliasRes>): { chain: AliasRes[]; poolId?: string; required: string[]; problem?: string } {
  const chain: AliasRes[] = []
  const required = new Set<string>()
  let id = aliasId
  for (let depth = 0; depth < MAX_DEPTH; depth++) {
    const a = aliases[id]
    if (!a) return { chain, required: [...required], problem: id ? `alias ${id} does not exist` : 'no alias selected' }
    if (chain.some((c) => c.id === a.id)) return { chain, required: [...required], problem: 'alias cycle' }
    chain.push(a)
    for (const c of a.spec.required_capabilities ?? []) required.add(c)
    if (a.spec.pool_id) return { chain, poolId: a.spec.pool_id, required: [...required] }
    id = a.spec.alias_id
  }
  return { chain, required: [...required], problem: 'alias chain too deep' }
}

/** Builds one lane per model mapping (and the default alias) through to pool members. */
export function planLanes(doc: PlanDocument, maps: {
  aliases: Record<string, AliasRes>
  pools: Record<string, PoolRes>
  accounts: Record<string, AccountRes>
  providers: Record<string, ProviderRes>
}): Lane[] {
  const r = doc.restrictions
  const entries: Lane['entry'][] = doc.model_mappings.map((m) => ({ kind: 'mapping' as const, match: m.match, note: m.note || undefined }))
  const targets = doc.model_mappings.map((m) => m.alias_id)
  if (doc.default_alias_id) {
    entries.push({ kind: 'default', match: 'any other model' })
    targets.push(doc.default_alias_id)
  }
  return entries.map((entry, i) => {
    const res = resolveAlias(targets[i] ?? '', maps.aliases)
    const pool = res.poolId ? maps.pools[res.poolId] : undefined
    const members: LaneMember[] = (pool?.spec.members ?? []).map((m) => {
      const account = maps.accounts[m.account_id]
      const provider = account ? maps.providers[account.spec.provider_id] : undefined
      const blocked: string[] = []
      if (!account) blocked.push('account missing')
      else {
        if (!account.spec.enabled) blocked.push('account disabled')
        if (r.allowed_billing.length > 0 && !r.allowed_billing.includes(account.spec.billing)) blocked.push(`billing ${account.spec.billing} not allowed`)
        if (r.allowed_provider_ids.length > 0 && !r.allowed_provider_ids.includes(account.spec.provider_id)) blocked.push('provider not allowed')
        if (r.denied_provider_ids.includes(account.spec.provider_id)) blocked.push('provider denied')
        // An empty classification means the provider type's default, which only the server resolves.
        if (provider?.spec.classification && r.allowed_classifications.length > 0 && !r.allowed_classifications.includes(provider.spec.classification))
          blocked.push(`${provider.spec.classification} providers not allowed`)
      }
      return { accountId: m.account_id, account, provider, model: m.model, priority: m.priority, weight: m.weight, blocked }
    })
    const problem = res.problem ?? (res.poolId && !pool ? `pool ${res.poolId} does not exist` : undefined)
    return { key: `${entry.kind}-${i}`, entry, chain: res.chain, required: [...new Set([...res.required, ...r.required_capabilities])], pool, members, problem }
  })
}

/** Accounts reachable through any lane (deduplicated, in first-seen order). */
export function laneAccounts(lanes: Lane[]): LaneMember[] {
  const seen = new Set<string>()
  const out: LaneMember[] = []
  for (const l of lanes)
    for (const m of l.members) {
      if (seen.has(m.accountId)) continue
      seen.add(m.accountId)
      out.push(m)
    }
  return out
}
