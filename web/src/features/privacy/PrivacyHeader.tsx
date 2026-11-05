import { useQueries, useQuery } from '@tanstack/react-query'
import { Text } from '@mantine/core'
import { effectiveQuery, planVersionsQuery, resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { TracePolicyRes } from '@/lib/api/types'
import { LinkTabs, PageHeader } from '@/ui/kit'
import { retentionLabel } from './privacy'

/** "Masking is on for 1 of 4 clients. Content capture is on for 1 plan (kept 3 days, masked)." */
function Coverage() {
  const eff = useQuery(effectiveQuery)
  const traces = useQuery(resourceListQuery<TracePolicyRes>('trace-policies'))
  const plans = (eff.data?.plans ?? []).filter((p) => p.published_version > 0)
  const versions = useQueries({ queries: plans.map((p) => planVersionsQuery(p.id)) })
  if (!eff.data || versions.some((v) => !v.data)) return null
  const tp = byId(traces.data?.items)
  let total = 0
  let masked = 0
  const capture: string[] = []
  plans.forEach((p, i) => {
    const live = versions[i]?.data?.items.find((v) => v.status === 'published')
    const priv = live?.document.privacy
    total += p.bindings
    if (priv?.policy_id) masked += p.bindings
    if (priv?.trace_policy_id) {
      const t = tp[priv.trace_policy_id]
      capture.push(t ? `${p.name}: kept ${retentionLabel[t.spec.retention] ?? 'custom'}, ${t.spec.mode === 'unredacted' ? 'unredacted' : 'masked'}` : p.name)
    }
  })
  const maskText = `Masking is on for ${masked} of ${total} ${total === 1 ? 'client' : 'clients'}.`
  const captureText = capture.length ? ` Content capture is on for ${capture.length} ${capture.length === 1 ? 'plan' : 'plans'} (${capture.join('; ')}).` : ' Content capture is off everywhere.'
  return <Text size="md">{maskText + captureText}</Text>
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
