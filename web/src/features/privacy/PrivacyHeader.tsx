import { useQueries, useQuery } from '@tanstack/react-query'
import { Text } from '@mantine/core'
import { Link } from '@tanstack/react-router'
import { effectiveQuery, planVersionsQuery, resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { PlanRes, TracePolicyRes } from '@/lib/api/types'
import { LinkTabs, PageHeader } from '@/ui/kit'
import { retentionLabel } from './privacy'

/** "Masking is on for 1 of 4 clients. Content capture is on for Private only (kept 3 days, masked)." */
function Coverage() {
  const eff = useQuery(effectiveQuery)
  const traces = useQuery(resourceListQuery<TracePolicyRes>('trace-policies'))
  const planList = useQuery(resourceListQuery<PlanRes>('routing-plans'))
  const plans = (eff.data?.plans ?? []).filter((p) => p.published_version > 0)
  const versions = useQueries({ queries: plans.map((p) => planVersionsQuery(p.id)) })
  if (!eff.data || versions.some((v) => !v.data)) return null
  const tp = byId(traces.data?.items)
  const pl = byId(planList.data?.items)
  let total = 0
  let masked = 0
  const capture: { id: string; name: string; how: string }[] = []
  plans.forEach((p, i) => {
    const live = versions[i]?.data?.items.find((v) => v.status === 'published')
    const priv = live?.document.privacy
    total += p.bindings
    if (priv?.policy_id) masked += p.bindings
    if (priv?.trace_policy_id) {
      const t = tp[priv.trace_policy_id]
      const name = pl[p.id]?.spec.display_name || p.name
      capture.push({ id: p.id, name, how: t ? `kept ${retentionLabel[t.spec.retention] ?? 'for a custom time'}, ${t.spec.mode === 'unredacted' ? 'unredacted' : 'masked'}` : '' })
    }
  })
  return (
    <Text size="md">
      Masking is on for {masked} of {total} {total === 1 ? 'client' : 'clients'}.{' '}
      {capture.length === 0 ? (
        'Content capture is off everywhere.'
      ) : (
        <>
          Content capture is on for{' '}
          {capture.map((c, i) => (
            <span key={c.id}>
              {i > 0 && (i === capture.length - 1 ? ' and ' : ', ')}
              <Link to="/routing/plans/$planId" params={{ planId: c.id }} style={{ color: 'var(--link)' }}>
                {c.name}
              </Link>
              {c.how && ` (${c.how})`}
            </span>
          ))}
          .
        </>
      )}
    </Text>
  )
}

export function PrivacyHeader() {
  return (
    <>
      <PageHeader title="Privacy" info="Masking replaces secrets and personal data with placeholders before a request leaves Harbor Pilot. Content capture keeps short-lived, encrypted copies for troubleshooting when a plan opts in." />
      <Coverage />
      <LinkTabs
        label="Privacy sections"
        items={[
          { to: '/privacy', label: 'Masking', exact: true },
          { to: '/privacy/trace-policies', label: 'Content capture' },
          { to: '/traces', label: 'Captured content', exact: true },
        ]}
      />
    </>
  )
}
