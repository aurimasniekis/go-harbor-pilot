import { AlertTriangle, Boxes } from 'lucide-react'
import { Badge, Banner, type Tone } from './ui'
import type { ResourceMeta } from '@/lib/api/types'
import { fmtDateTime } from '@/lib/format'

/** Who manages a resource; Terraform-managed objects are highlighted. */
export function ManagedByBadge({ managedBy }: { managedBy: string }) {
  if (managedBy === 'terraform') {
    return (
      <Badge tone="purple" icon={Boxes} title="Managed by Terraform">
        Terraform
      </Badge>
    )
  }
  return <Badge title={`Last changed via ${managedBy}`}>{managedBy}</Badge>
}

export function DriftBadge({ r }: { r: Pick<ResourceMeta, 'drift_state' | 'drifted_at' | 'drifted_by'> }) {
  if (r.drift_state !== 'drifted') return null
  return (
    <Badge
      tone="warning"
      icon={AlertTriangle}
      title={`Changed outside Terraform${r.drifted_by ? ` by ${r.drifted_by}` : ''}${r.drifted_at ? ` at ${fmtDateTime(r.drifted_at)}` : ''}. The next Terraform plan will show the difference.`}
    >
      Drifted
    </Badge>
  )
}

/** Managed-by + drift badges for a list row. */
export function ResourceBadges({ r }: { r: ResourceMeta }) {
  return (
    <span style={{ display: 'inline-flex', gap: 4, flexWrap: 'wrap' }}>
      <ManagedByBadge managedBy={r.managed_by} />
      <DriftBadge r={r} />
    </span>
  )
}

/** Warning shown in edit dialogs of Terraform-managed objects. */
export function TerraformEditWarning({ r }: { r?: ResourceMeta | null }) {
  if (!r || r.managed_by !== 'terraform') return null
  return (
    <Banner tone="warning" icon={Boxes}>
      This object is managed by Terraform. Changes made here are recorded as drift and the next Terraform apply will revert
      them.
    </Banner>
  )
}

const classificationTone: Record<string, Tone> = { official: 'neutral', partial: 'info', unofficial: 'warning', local: 'purple' }

export function ClassificationBadge({ value }: { value: string }) {
  const title: Record<string, string> = {
    official: 'Documented provider API or vendor-supplied binary',
    partial: 'Compatible API with partial feature coverage',
    unofficial: 'Undocumented integration; may break or conflict with provider terms',
    local: 'Local model endpoint',
  }
  return (
    <Badge tone={classificationTone[value] ?? 'neutral'} title={title[value]}>
      {value || 'default'}
    </Badge>
  )
}

const billingTone: Record<string, Tone> = { metered_api: 'warning', subscription: 'info', free: 'success', local: 'purple' }
const billingLabel: Record<string, string> = { metered_api: 'Metered API', subscription: 'Subscription', free: 'Free', local: 'Local' }

export function BillingBadge({ value }: { value: string }) {
  return <Badge tone={billingTone[value] ?? 'neutral'}>{billingLabel[value] ?? value}</Badge>
}
