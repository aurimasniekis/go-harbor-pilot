import { useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Bot, KeyRound, Plus, Trash2, XCircle } from 'lucide-react'
import { Badge, Button, TableScroll } from '@/components/ui'
import { Group, Stack, Text, Tooltip } from '@mantine/core'
import { type Column, DataTable, Empty, Section } from '@/ui/kit'
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
  const permLevel = (perms: string[]) => {
    const has = (l: string) => permissionGroups.find((g) => g.label === l)?.perms.some((p) => perms.includes(p))
    const parts = [has('Administer') ? 'Administer' : has('Manage') ? 'Manage' : 'Read']
    if (has('Trace content (audited)')) parts.push('captured content')
    return parts.join(' + ')
  }
  const cols: Column<ServicePrincipal>[] = [
    {
      id: 'name',
      header: 'Automation account',
      mobile: 'title',
      cell: (sp) => (
        <Stack gap={0} miw={0}>
          <Group gap={6} wrap="nowrap">
            <Text size="sm" fw={500} truncate>
              {sp.name}
            </Text>
            {sp.disabled && <Badge tone="error">Disabled</Badge>}
            {sp.managed_by === 'terraform' && <Badge tone="purple" title="Managed by Terraform">Terraform</Badge>}
          </Group>
          {sp.description && (
            <Text size="xs" c="dimmed" truncate>
              {sp.description}
            </Text>
          )}
        </Stack>
      ),
    },
    {
      id: 'perms',
      header: 'Can',
      mobile: 'meta',
      cell: (sp) => (
        <Tooltip label={sp.permissions.join(', ')}>
          <Text size="sm" span>
            {permLevel(sp.permissions)} <Text span size="xs" c="dimmed">({sp.permissions.length})</Text>
          </Text>
        </Tooltip>
      ),
    },
    { id: 'created', header: 'Created', mobile: 'meta', cell: (sp) => fmtDateTime(sp.created_at) },
    {
      id: 'actions',
      header: <span className="visually-hidden">Actions</span>,
      align: 'right',
      mobile: 'aside',
      cell: (sp) => (
        <Group gap={4} wrap="nowrap" justify="flex-end" onClick={(e) => e.stopPropagation()}>
          <Button size="small" variant="ghost" icon={KeyRound} onClick={() => setTokensFor(sp)}>
            Tokens
          </Button>
          <ConfirmButton size="small" icon={Trash2} label={`Delete ${sp.name}`} title={`Delete ${sp.name}?`}
            message="All of its API tokens are revoked immediately." confirmLabel="Delete" onConfirm={() => del.mutate(sp.id)} />
        </Group>
      ),
    },
  ]
  return (
    <Section
      title="API tokens"
      info="Automation accounts for Terraform, CI and scripts. Each has only the permissions it needs and signs in with API tokens, never a browser session."
      actions={<Button variant="primary" icon={Plus} onClick={() => setEditing('new')}>New automation account</Button>}
    >
      <MutationError error={del.error} />
      {list.isError ? (
        <QueryError error={list.error} retry={() => void list.refetch()} />
      ) : !list.data ? (
        <TableSkeleton />
      ) : (
        <DataTable label="Automation accounts" rows={list.data.items ?? []} columns={cols} rowKey={(sp) => sp.id} onRow={(sp) => setEditing(sp)}
          empty={<Empty icon={<Bot size={22} />} title="No automation accounts">Create one for Terraform or CI with only the permissions it needs.</Empty>} />
      )}
      {editing && <EditPrincipal sp={editing === 'new' ? null : editing} onClose={() => { setEditing(null); void qc.invalidateQueries({ queryKey: qk.principals }) }} />}
      {tokensFor && <Tokens sp={tokensFor} onClose={() => setTokensFor(null)} />}
    </Section>
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
