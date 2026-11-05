import { useQueries, useQuery } from '@tanstack/react-query'
import { planVersionsQuery, resourceListQuery } from '@/lib/api/queries'
import type { AliasRes, PlanDocument, PlanRes } from '@/lib/api/types'

export interface PlanDocs {
  plan: PlanRes
  /** Live and draft documents (what can affect traffic now or next). */
  docs: PlanDocument[]
}

/** Every plan with its live and draft documents. */
export function usePlanDocuments(): PlanDocs[] {
  const plans = useQuery(resourceListQuery<PlanRes>('routing-plans'))
  const items = plans.data?.items ?? []
  const versions = useQueries({ queries: items.map((p) => planVersionsQuery(p.id)) })
  return items.map((plan, i) => ({
    plan,
    docs: (versions[i]?.data?.items ?? []).filter((v) => v.status === 'published' || v.status === 'draft').map((v) => v.document),
  }))
}

const planName = (p: PlanRes) => `plan ${p.spec.display_name || p.name}`

/** What refers to each model name: plans (mappings or default) and other model names. */
export function aliasUsers(plans: PlanDocs[], aliases: AliasRes[]): Record<string, string[]> {
  const out: Record<string, string[]> = {}
  const add = (id: string, who: string) => {
    if (!id) return
    const l = (out[id] ??= [])
    if (!l.includes(who)) l.push(who)
  }
  for (const { plan, docs } of plans)
    for (const d of docs) {
      for (const m of d.model_mappings) add(m.alias_id, planName(plan))
      add(d.default_alias_id, planName(plan))
    }
  for (const a of aliases) if (a.spec.alias_id) add(a.spec.alias_id, `model name ${a.name}`)
  return out
}

/** Model names that point at each fallback group. */
export function poolUsers(aliases: AliasRes[]): Record<string, string[]> {
  const out: Record<string, string[]> = {}
  for (const a of aliases) if (a.spec.pool_id) (out[a.spec.pool_id] ??= []).push(`model name ${a.name}`)
  return out
}

/** Plans that use each masking policy or content capture rule. */
export function privacyUsers(plans: PlanDocs[], field: 'policy_id' | 'trace_policy_id'): Record<string, string[]> {
  const out: Record<string, string[]> = {}
  for (const { plan, docs } of plans)
    for (const d of docs) {
      const id = d.privacy?.[field]
      if (!id) continue
      const l = (out[id] ??= [])
      if (!l.includes(planName(plan))) l.push(planName(plan))
    }
  return out
}
