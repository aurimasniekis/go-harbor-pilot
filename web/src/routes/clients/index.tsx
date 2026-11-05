import { useState } from 'react'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Laptop, Plus } from 'lucide-react'
import { PageHeader } from '@/components/PageHeader'
import { Badge, Button, EmptyState, Panel, TableScroll } from '@/components/ui'
import { Dialog } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { ResourceBadges } from '@/components/ResourceBadges'
import { BindingForm } from '@/features/config/BindingForm'
import { harnesses } from '@/features/config/constants'
import { credentialsQuery, resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { BindingRes, PlanRes, ProjectRes } from '@/lib/api/types'
import { useCan } from '@/lib/session'

export const Route = createFileRoute('/clients/')({
  component: Clients,
})

function CredentialCount({ id }: { id: string }) {
  const q = useQuery(credentialsQuery(id))
  if (!q.data) return <span className="muted">…</span>
  const active = q.data.items.filter((c) => !c.revoked_at).length
  return <span className="num">{active}</span>
}

function Clients() {
  const navigate = useNavigate()
  const list = useQuery(resourceListQuery<BindingRes>('client-bindings'))
  const plans = byId(useQuery(resourceListQuery<PlanRes>('routing-plans')).data?.items)
  const projects = byId(useQuery(resourceListQuery<ProjectRes>('projects')).data?.items)
  const canWrite = useCan('config.write')
  const [creating, setCreating] = useState(false)
  const items = list.data?.items ?? []
  return (
    <>
      <PageHeader title="Clients" subtitle="A client binding connects a harness or application to a routing plan, project, limits and telemetry policy. Clients authenticate with gateway credentials." />
      <Panel title="Client bindings" actions={canWrite && <Button variant="primary" icon={Plus} onClick={() => setCreating(true)}>Add client</Button>}>
        {list.isError ? (
          <QueryError error={list.error} retry={() => void list.refetch()} />
        ) : !list.data ? (
          <TableSkeleton />
        ) : items.length === 0 ? (
          <EmptyState icon={Laptop} title="No clients yet">Add a client, pick its routing plan, then issue a gateway credential.</EmptyState>
        ) : (
          <TableScroll label="Client bindings">
            <table className="data-table" style={{ width: '100%' }}>
              <thead>
                <tr>
                  <th>Client</th>
                  <th>Plan</th>
                  <th>Project</th>
                  <th>Harness</th>
                  <th>Status</th>
                  <th>Active credentials</th>
                  <th>Managed by</th>
                </tr>
              </thead>
              <tbody>
                {items.map((b) => (
                  <tr key={b.id}>
                    <td>
                      <Link to="/clients/$bindingId" params={{ bindingId: b.id }} className="mono">{b.name}</Link>
                      {b.spec.display_name && <div className="muted">{b.spec.display_name}</div>}
                    </td>
                    <td className="mono">{plans[b.spec.plan_id]?.name ?? b.spec.plan_id}</td>
                    <td className="mono">{b.spec.project_id ? projects[b.spec.project_id]?.name ?? b.spec.project_id : <span className="muted">—</span>}</td>
                    <td>{harnesses.find((h) => h.value === b.spec.expected_harness)?.label ?? b.spec.expected_harness}</td>
                    <td>{b.spec.enabled ? <Badge tone="success">enabled</Badge> : <Badge>disabled</Badge>}</td>
                    <td><CredentialCount id={b.id} /></td>
                    <td><ResourceBadges r={b} /></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </TableScroll>
        )}
      </Panel>
      <Dialog open={creating} wide title="Add client" onClose={() => setCreating(false)}>
        {creating && (
          <BindingForm binding={null} onCancel={() => setCreating(false)}
            onSaved={(b) => { setCreating(false); void navigate({ to: '/clients/$bindingId', params: { bindingId: b.id } }) }} />
        )}
      </Dialog>
    </>
  )
}
