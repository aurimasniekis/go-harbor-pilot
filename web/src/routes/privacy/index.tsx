import { createFileRoute } from '@tanstack/react-router'
import { Badge, Banner } from '@/components/ui'
import { ResourceList } from '@/features/config/ResourceList'
import { PrivacyPolicyDialog } from '@/features/privacy/PrivacyPolicyDialog'
import type { PrivacyPolicyRes } from '@/lib/api/types'

export const Route = createFileRoute('/privacy/')({
  component: PrivacyPolicies,
})

function PrivacyPolicies() {
  return (
    <ResourceList<PrivacyPolicyRes>
      plural="privacy-policies"
      title="Masking policies"
      subtitle="Select a policy in a routing plan's Privacy section to mask that plan's requests."
      noun="privacy policy"
      empty="Masking replaces credentials and other sensitive values with placeholders before requests leave the gateway."
      extra={
        <Banner tone="info">
          Masking is defense in depth, not a guarantee: tools running on the user&apos;s machine are outside the gateway&apos;s outbound boundary.
        </Banner>
      }
      columns={[
        { header: 'Enforcement', cell: (r) => (r.spec.required ? (r.spec.allow_unmasked_fallback ? <Badge tone="warning">Required, availability first</Badge> : <Badge tone="success">Required</Badge>) : <Badge>Best effort</Badge>) },
        { header: 'Detectors', cell: (r) => <span className="num">{(r.spec.detectors?.length ?? 0) + (r.spec.personal_data?.length ?? 0) + (r.spec.supplementary?.length ?? 0)}</span> },
        { header: 'Custom rules', cell: (r) => <span className="num">{r.spec.rules?.length ?? 0}</span> },
        { header: 'Restoration', cell: (r) => (
          <span style={{ display: 'inline-flex', gap: 4, flexWrap: 'wrap' }}>
            {r.spec.restore_in_responses ? <Badge tone="info">Replies</Badge> : <Badge>Off</Badge>}
            {r.spec.tool_arg_restore?.enabled && <Badge tone="warning">Tool arguments</Badge>}
          </span>
        ) },
      ]}
      dialog={(r, close) => <PrivacyPolicyDialog policy={r} onClose={close} />}
    />
  )
}
