import { useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Bot, KeyRound, Pencil, Plus, Trash2, XCircle } from 'lucide-react'
import { Badge, Button, EmptyState, Panel, TableScroll } from '@/components/ui'
import { Checkbox, Dialog, MutationError, SecretOnce, TextField, formStyles as f, ConfirmButton } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { apiSend } from '@/lib/api/client'
import { principalTokensQuery, principalsQuery, qk } from '@/lib/api/queries'
import type { ServicePrincipal } from '@/lib/api/types'
import { fmtDateTime, fmtRelative } from '@/lib/format'

export const Route = createFileRoute('/settings/service-principals')({
  component: Principals,
})

/** Permissions grantable to automation, grouped for the editor. */
const permissionGroups: { label: string; perms: string[] }[] = [
  { label: 'Read', perms: ['config.read', 'usage.read', 'activity.read', 'traces.metadata.read', 'audit.read'] },
  { label: 'Manage', perms: ['config.write', 'policy.publish', 'secrets.write', 'tokens.manage', 'events.manage', 'notifications.manage', 'compat.manage', 'traces.delete'] },
  { label: 'Administer', perms: ['users.manage', 'auth.settings', 'system.manage'] },
  { label: 'Trace content (audited)', perms: ['traces.content.read', 'traces.content.unredacted', 'traces.export'] },
]

function Principals() {
  const qc = useQueryClient()
  const list = useQuery(principalsQuery)
  const [editing, setEditing] = useState<ServicePrincipal | 'new' | null>(null)
  const [tokensFor, setTokensFor] = useState<ServicePrincipal | null>(null)
  const del = useMutation({
    mutationFn: (id: string) => apiSend<void>('DELETE', `/service-principals/${id}`),
    onSuccess: () => void qc.invalidateQueries({ queryKey: qk.principals }),
  })
  return (
    <Panel
      title="Service principals"
      subtitle="Automation identities for Terraform, CI and integrations. They authenticate with hpa_ API tokens and never use browser sessions."
      actions={<Button variant="primary" icon={Plus} onClick={() => setEditing('new')}>New service principal</Button>}
    >
      <MutationError error={del.error} />
      {list.isError ? (
        <QueryError error={list.error} retry={() => void list.refetch()} />
      ) : !list.data ? (
        <TableSkeleton />
      ) : (list.data.items ?? []).length === 0 ? (
        <EmptyState icon={Bot} title="No service principals">Create one for Terraform or CI with only the permissions it needs.</EmptyState>
      ) : (
        <TableScroll label="Service principals">
          <table className="data-table" style={{ width: '100%' }}>
            <thead>
              <tr>
                <th>Name</th>
                <th>Permissions</th>
                <th>Managed by</th>
                <th>Created</th>
                <th aria-label="Actions" />
              </tr>
            </thead>
            <tbody>
              {(list.data.items ?? []).map((sp) => (
                <tr key={sp.id}>
                  <td>
                    <div className="mono">{sp.name}</div>
                    <div className="muted">{sp.description}</div>
                    {sp.disabled && <Badge tone="error">disabled</Badge>}
                  </td>
                  <td style={{ maxWidth: 420 }}>{sp.permissions.map((p) => <Badge key={p}>{p}</Badge>)}</td>
                  <td><Badge tone={sp.managed_by === 'terraform' ? 'purple' : 'neutral'}>{sp.managed_by}</Badge></td>
                  <td>{fmtDateTime(sp.created_at)}</td>
                  <td style={{ whiteSpace: 'nowrap' }}>
                    <Button size="small" variant="ghost" icon={KeyRound} onClick={() => setTokensFor(sp)}>Tokens</Button>
                    <Button size="small" variant="ghost" icon={Pencil} aria-label={`Edit ${sp.name}`} onClick={() => setEditing(sp)} />
                    <ConfirmButton size="small" icon={Trash2} label={`Delete ${sp.name}`} title={`Delete ${sp.name}?`}
                      message="All of its API tokens are revoked immediately." confirmLabel="Delete" onConfirm={() => del.mutate(sp.id)} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableScroll>
      )}
      {editing && <EditPrincipal sp={editing === 'new' ? null : editing} onClose={() => { setEditing(null); void qc.invalidateQueries({ queryKey: qk.principals }) }} />}
      {tokensFor && <Tokens sp={tokensFor} onClose={() => setTokensFor(null)} />}
    </Panel>
  )
}

function EditPrincipal({ sp, onClose }: { sp: ServicePrincipal | null; onClose: () => void }) {
  const [name, setName] = useState(sp?.name ?? '')
  const [description, setDescription] = useState(sp?.description ?? '')
  const [perms, setPerms] = useState<string[]>(sp?.permissions ?? ['config.read'])
  const [disabled, setDisabled] = useState(sp?.disabled ?? false)
  const save = useMutation({
    mutationFn: () => {
      const body = { name, description, permissions: perms, disabled }
      return sp ? apiSend('PUT', `/service-principals/${sp.id}`, body, { ifMatch: sp.revision }) : apiSend('POST', '/service-principals', body)
    },
    onSuccess: onClose,
  })
  return (
    <Dialog open title={sp ? `Edit ${sp.name}` : 'New service principal'} onClose={onClose}>
      {sp?.managed_by === 'terraform' && <p className="secondary">This principal is managed by Terraform; changes here will appear as drift.</p>}
      <TextField label="Name" value={name} onChange={setName} disabled={!!sp} hint="Lowercase letters, digits, dot, dash and underscore." required />
      <TextField label="Description" value={description} onChange={setDescription} maxLength={500} />
      {permissionGroups.map((g) => (
        <fieldset key={g.label} style={{ border: 'none', padding: 0, margin: 0, display: 'flex', flexWrap: 'wrap', gap: '6px 16px' }}>
          <legend className={f.label} style={{ marginBottom: 6 }}>{g.label}</legend>
          {g.perms.map((p) => (
            <Checkbox key={p} label={<span className="mono">{p}</span>} checked={perms.includes(p)}
              onChange={(v) => setPerms(v ? [...perms, p] : perms.filter((x) => x !== p))} />
          ))}
        </fieldset>
      ))}
      {sp && <Checkbox label="Disabled (all tokens stop working)" checked={disabled} onChange={setDisabled} />}
      <MutationError error={save.error} />
      <div className={f.actions}>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} disabled={!name} onClick={() => save.mutate()}>Save</Button>
      </div>
    </Dialog>
  )
}

function Tokens({ sp, onClose }: { sp: ServicePrincipal; onClose: () => void }) {
  const qc = useQueryClient()
  const tokens = useQuery(principalTokensQuery(sp.id))
  const [name, setName] = useState('')
  const [ttlDays, setTtlDays] = useState('90')
  const [plain, setPlain] = useState<string | null>(null)
  const refresh = () => void qc.invalidateQueries({ queryKey: principalTokensQuery(sp.id).queryKey })
  const issue = useMutation({
    mutationFn: () => apiSend<{ plaintext: string }>('POST', `/service-principals/${sp.id}/tokens`, { name, ttl_seconds: Number(ttlDays || 0) * 86400 }),
    onSuccess: (r) => { setPlain(r.plaintext); setName(''); refresh() },
  })
  const revoke = useMutation({ mutationFn: (id: string) => apiSend<void>('DELETE', `/service-principals/${sp.id}/tokens/${id}`), onSuccess: refresh })
  return (
    <Dialog open title={`API tokens · ${sp.name}`} onClose={onClose}>
      {plain && <SecretOnce label="API token" value={plain} />}
      <div className={f.grid}>
        <TextField label="Token name" value={name} onChange={setName} maxLength={80} />
        <TextField label="Lifetime (days, 0 = no expiry)" type="number" value={ttlDays} onChange={setTtlDays} />
      </div>
      <MutationError error={issue.error ?? revoke.error} />
      <div className={f.actions}>
        <Button variant="primary" icon={KeyRound} busy={issue.isPending} disabled={!name} onClick={() => issue.mutate()}>Issue token</Button>
      </div>
      {tokens.isError ? (
        <QueryError error={tokens.error} />
      ) : (
        <TableScroll label="Tokens">
          <table className="data-table" style={{ width: '100%' }}>
            <thead><tr><th>Name</th><th>Prefix</th><th>Expires</th><th>Last used</th><th aria-label="Actions" /></tr></thead>
            <tbody>
              {(tokens.data?.items ?? []).map((t) => (
                <tr key={t.id}>
                  <td>{t.name} {t.revoked_at && <Badge tone="error">revoked</Badge>}</td>
                  <td className="mono">hpa_{t.prefix}_…</td>
                  <td>{t.expires_at ? fmtDateTime(t.expires_at) : 'never'}</td>
                  <td>{fmtRelative(t.last_used_at)}</td>
                  <td>{!t.revoked_at && <Button size="small" variant="ghost" icon={XCircle} onClick={() => revoke.mutate(t.id)}>Revoke</Button>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableScroll>
      )}
    </Dialog>
  )
}
