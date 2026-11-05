import { createFileRoute, Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Badge } from '@/components/ui'
import { ResourceList } from '@/features/config/ResourceList'
import { PlanDialog } from '@/features/config/PlanDialog'
import { effectiveQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { PlanRes } from '@/lib/api/types'

export const Route = createFileRoute('/routing/plans/')({
  component: Plans,
})

function Plans() {
  const eff = useQuery(effectiveQuery)
  const plans = byId(eff.data?.plans)
  return (
    <ResourceList<PlanRes>
      plural="routing-plans"
      title="Routing plans"
      subtitle="Plans hold versioned routing logic. Drafts never affect traffic until published."
      noun="plan"
      empty="Create a plan, then draft and publish its first version."
      nameCell={(r) => (
        <Link to="/routing/plans/$planId" params={{ planId: r.id }} className="mono">
          {r.name}
        </Link>
      )}
      columns={[
        { header: 'Display name', cell: (r) => r.spec.display_name || <span className="muted">—</span> },
        {
          header: 'Published',
          cell: (r) => {
            const p = plans[r.id]
            return p?.published_version ? <Badge tone="success">v{p.published_version}</Badge> : <Badge tone="warning">none</Badge>
          },
        },
        { header: 'Clients', cell: (r) => plans[r.id]?.bindings ?? 0 },
      ]}
      dialog={(r, close) => <PlanDialog plan={r} onClose={close} />}
    />
  )
}
