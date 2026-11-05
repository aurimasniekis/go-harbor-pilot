import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Badge } from '@/components/ui'
import { ResourceList } from '@/features/config/ResourceList'
import { TracePolicyDialog } from '@/features/privacy/TracePolicyDialog'
import { retentionLabel, traceModes } from '@/features/privacy/privacy'
import { resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { PrivacyPolicyRes, TracePolicyRes } from '@/lib/api/types'
import { fmtDateTime } from '@/lib/format'

export const Route = createFileRoute('/privacy/trace-policies')({
  component: TracePolicies,
})

function TracePolicies() {
  const privacy = useQuery(resourceListQuery<PrivacyPolicyRes>('privacy-policies'))
  const pol = byId(privacy.data?.items)
  return (
    <ResourceList<TracePolicyRes>
      plural="trace-policies"
      title="Content capture rules"
      subtitle="Content is captured only for plans that choose one of these rules. Recommended: masked full content kept for 3 days."
      noun="trace policy"
      empty="Trace policies capture short-lived, encrypted request and response content for troubleshooting."
      columns={[
        { header: 'Keeps', mobile: 'line', cell: (r) => (
          <span style={{ display: 'inline-flex', gap: 4, flexWrap: 'wrap' }}>
            <Badge tone={r.spec.mode === 'unredacted' ? 'error' : r.spec.mode === 'off' ? 'neutral' : 'info'}>
              {traceModes.find((m) => m.value === r.spec.mode)?.label ?? r.spec.mode}
            </Badge>
            {r.spec.mode === 'unredacted' && r.spec.unredacted_until && <span className="muted">until {fmtDateTime(r.spec.unredacted_until)}</span>}
          </span>
        ) },
        { header: 'For', mobile: 'meta', cell: (r) => (r.spec.retention === 'custom' ? `${Math.round((r.spec.custom_ttl_seconds ?? 0) / 3600)} hours` : retentionLabel[r.spec.retention]) },
        { header: 'Masked with', mobile: 'meta', cell: (r) => (r.spec.redaction_policy_id ? (pol[r.spec.redaction_policy_id]?.name ?? r.spec.redaction_policy_id) : 'Built-in detectors') },
        { header: 'Download', mobile: 'meta', cell: (r) => (r.spec.allow_export ? <Badge tone="warning">Allowed</Badge> : 'Not allowed') },
      ]}
      dialog={(r, close) => <TracePolicyDialog policy={r} onClose={close} />}
    />
  )
}
