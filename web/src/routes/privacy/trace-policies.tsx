import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Badge } from '@/components/ui'
import { ResourceList } from '@/features/config/ResourceList'
import { TracePolicyDialog } from '@/features/privacy/TracePolicyDialog'
import { retentionLabel, traceModeLabel } from '@/features/privacy/privacy'
import { privacyUsers, usePlanDocuments } from '@/features/routing/usage'
import { resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { PrivacyPolicyRes, TracePolicyRes } from '@/lib/api/types'
import { fmtDateTime, getDisplayTimeZone } from '@/lib/format'

export const Route = createFileRoute('/privacy/trace-policies')({
  component: TracePolicies,
})

function TracePolicies() {
  const privacy = useQuery(resourceListQuery<PrivacyPolicyRes>('privacy-policies'))
  const pol = byId(privacy.data?.items)
  const plans = usePlanDocuments()
  const users = privacyUsers(plans, 'trace_policy_id')
  return (
    <ResourceList<TracePolicyRes>
      plural="trace-policies"
      title="Content capture rules"
      hideTitle
      explain="Content is captured only for plans that choose one of these rules. Recommended: masked full content kept for 3 days."
      noun="capture rule"
      empty="Capture rules keep short-lived, encrypted copies of requests and responses for troubleshooting."
      usedBy={(r) => users[r.id] ?? []}
      columns={[
        { header: 'Keeps', mobile: 'line', cell: (r) => (
          <span style={{ display: 'inline-flex', gap: 4, flexWrap: 'wrap', alignItems: 'center' }}>
            <Badge tone={r.spec.mode === 'unredacted' ? 'error' : r.spec.mode === 'off' ? 'neutral' : 'info'}>{traceModeLabel(r.spec.mode)}</Badge>
            {r.spec.mode === 'unredacted' && r.spec.unredacted_until && (
              <span className="muted">until {fmtDateTime(r.spec.unredacted_until)} {getDisplayTimeZone()}</span>
            )}
          </span>
        ) },
        { header: 'For', mobile: 'meta', cell: (r) => (r.spec.retention === 'custom' ? `${Math.round((r.spec.custom_ttl_seconds ?? 0) / 3600)} hours` : retentionLabel[r.spec.retention]) },
        { header: 'Masked with', mobile: 'meta', cell: (r) => (r.spec.redaction_policy_id ? (pol[r.spec.redaction_policy_id]?.name ?? r.spec.redaction_policy_id) : 'Built-in detectors') },
        { header: 'Downloads', mobile: 'meta', cell: (r) => (r.spec.allow_export ? <Badge tone="warning">Allowed</Badge> : 'Not allowed') },
      ]}
      dialog={(r, close) => <TracePolicyDialog policy={r} onClose={close} />}
    />
  )
}
