import { useState } from 'react'
import { createFileRoute, Link } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { KeyRound, XCircle } from 'lucide-react'
import { PageHeader } from '@/components/PageHeader'
import { Badge, Button, Panel, Skeleton, TableScroll } from '@/components/ui'
import { MutationError, ConfirmButton } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { ResourceBadges } from '@/components/ResourceBadges'
import { BindingForm } from '@/features/config/BindingForm'
import { IssueCredentialDialog } from '@/features/config/IssueCredentialDialog'
import { apiSend } from '@/lib/api/client'
import { credentialsQuery, resourceQuery } from '@/lib/api/queries'
import type { BindingRes } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { fmtDateTime, fmtRelative } from '@/lib/format'
import { RecentRequestsPanel } from '@/features/activity/ActivityPanels'

export const Route = createFileRoute('/clients/$bindingId')({
  component: ClientDetail,
})

function ClientDetail() {
  const { bindingId } = Route.useParams()
  const qc = useQueryClient()
  const binding = useQuery(resourceQuery<BindingRes>('client-bindings', bindingId))
  const creds = useQuery(credentialsQuery(bindingId))
  const canTokens = useCan('tokens.manage')
  const canActivity = useCan('activity.read')
  const [issuing, setIssuing] = useState(false)
  const revoke = useMutation({
    mutationFn: (id: string) => apiSend<void>('DELETE', `/client-bindings/${encodeURIComponent(bindingId)}/credentials/${encodeURIComponent(id)}`),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['resources'] }),
  })
  if (binding.isError) return <QueryError error={binding.error} retry={() => void binding.refetch()} />
  if (!binding.data) return <Skeleton height={160} />
  const b = binding.data
  return (
    <>
      <p>
        <Link to="/clients">← All clients</Link>
      </p>
      <PageHeader title={<span className="mono">{b.name}</span>} subtitle={b.spec.display_name || undefined} actions={<ResourceBadges r={b} />} />
      <Panel title="Gateway credentials" subtitle="Clients send these as Bearer tokens (or x-api-key). Tokens are shown once at issue time; only a verifier is stored."
        actions={canTokens && <Button variant="primary" icon={KeyRound} onClick={() => setIssuing(true)}>Issue credential</Button>}>
        <MutationError error={revoke.error} />
        {creds.isError ? (
          <QueryError error={creds.error} retry={() => void creds.refetch()} />
        ) : !creds.data ? (
          <TableSkeleton rows={2} />
        ) : creds.data.items.length === 0 ? (
          <p className="secondary">No credentials yet. Issue one and use it with the client's base URL.</p>
        ) : (
          <TableScroll label="Gateway credentials">
            <table className="data-table" style={{ width: '100%' }}>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Token</th>
                  <th>Scopes</th>
                  <th>Expires</th>
                  <th>Last used</th>
                  <th aria-label="Actions" />
                </tr>
              </thead>
              <tbody>
                {creds.data.items.map((cr) => (
                  <tr key={cr.id}>
                    <td>
                      {cr.name} {cr.parent_id && <Badge tone="info">child</Badge>} {cr.revoked_at && <Badge tone="error">revoked</Badge>}
                      {Object.keys(cr.bound_refs).length > 0 && (
                        <div className="muted mono">{Object.entries(cr.bound_refs).map(([k, v]) => `${k}=${v}`).join(', ')}</div>
                      )}
                    </td>
                    <td className="mono">hpk_{cr.prefix}_…</td>
                    <td>{cr.scopes.map((s) => <Badge key={s}>{s}</Badge>)}</td>
                    <td>{cr.expires_at ? fmtDateTime(cr.expires_at) : 'never'}</td>
                    <td>{fmtRelative(cr.last_used_at)}</td>
                    <td>
                      {!cr.revoked_at && canTokens && (
                        <ConfirmButton size="small" icon={XCircle} title={`Revoke ${cr.name}?`}
                          message="Tools using this token stop working immediately. Tokens issued from it are revoked too." confirmLabel="Revoke"
                          onConfirm={() => revoke.mutate(cr.id)}>
                          Revoke
                        </ConfirmButton>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </TableScroll>
        )}
      </Panel>
      {canActivity && <RecentRequestsPanel bindingId={b.id} />}
      <Panel title="Client settings">
        <BindingForm key={b.revision} binding={b} onSaved={() => void qc.invalidateQueries({ queryKey: ['resources'] })} />
      </Panel>
      {issuing && <IssueCredentialDialog bindingID={b.id} onClose={() => setIssuing(false)} />}
    </>
  )
}
