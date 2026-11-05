import { createFileRoute } from '@tanstack/react-router'
import { ClassificationBadge } from '@/components/ResourceBadges'
import { ResourceList } from '@/features/config/ResourceList'
import { ProviderDialog } from '@/features/config/ProviderDialog'
import { providerTypes } from '@/features/config/constants'
import type { ProviderRes } from '@/lib/api/types'

export const Route = createFileRoute('/providers/')({
  component: Providers,
})

function Providers() {
  return (
    <ResourceList<ProviderRes>
      plural="providers"
      title="Providers"
      subtitle="Each provider is one upstream endpoint type. Classification is shown wherever the provider is used."
      noun="provider"
      empty="Add an API provider, a local model endpoint or a CLI bridge."
      columns={[
        { header: 'Type', cell: (r) => providerTypes.find((t) => t.value === r.spec.type)?.label ?? r.spec.type },
        { header: 'Endpoint', cell: (r) => <span className="mono" style={{ overflowWrap: 'anywhere' }}>{r.spec.base_url || '— (CLI bridge)'}</span> },
        { header: 'Classification', cell: (r) => <ClassificationBadge value={r.spec.classification} /> },
        { header: 'Models', cell: (r) => r.spec.models.length || <span className="muted">any</span> },
      ]}
      dialog={(r, close) => <ProviderDialog provider={r} onClose={close} />}
    />
  )
}
