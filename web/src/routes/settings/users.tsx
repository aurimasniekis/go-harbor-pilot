import { useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { KeyRound, LogOut, Pencil, Trash2, UserPlus } from 'lucide-react'
import { Badge, Button, EmptyState, Panel, TableScroll } from '@/components/ui'
import { Checkbox, Dialog, MutationError, SecretOnce, SelectField, TextField, formStyles as f } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { apiSend } from '@/lib/api/client'
import { qk, sessionQuery, usersQuery } from '@/lib/api/queries'
import type { User } from '@/lib/api/types'
import { fmtRelative } from '@/lib/format'

export const Route = createFileRoute('/settings/users')({
  component: Users,
})

type Role = 'admin' | 'operator' | 'viewer'
const roles: { value: Role; label: string }[] = [
  { value: 'viewer', label: 'Viewer — read configuration, usage and activity' },
  { value: 'operator', label: 'Operator — manage configuration, policies and tokens' },
  { value: 'admin', label: 'Admin — everything, including users and sign-in' },
]
const contentGrants = [
  { value: 'traces.content.read', label: 'Read masked trace content' },
  { value: 'traces.content.unredacted', label: 'Read unredacted diagnostic captures' },
  { value: 'traces.export', label: 'Export trace content' },
]

function Users() {
  const qc = useQueryClient()
  const users = useQuery(usersQuery)
  const me = useQuery(sessionQuery).data?.user
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<User | null>(null)
  const [secret, setSecret] = useState<{ label: string; value: string } | null>(null)
  const invalidate = () => void qc.invalidateQueries({ queryKey: qk.users })

  const reset = useMutation({
    mutationFn: (id: string) => apiSend<{ reset_url: string }>('POST', `/users/${id}/password-reset`),
    onSuccess: (r) => setSecret({ label: 'password reset link', value: r.reset_url }),
  })
  const revoke = useMutation({ mutationFn: (id: string) => apiSend<void>('POST', `/users/${id}/revoke-sessions`) })
  const del = useMutation({ mutationFn: (id: string) => apiSend<void>('DELETE', `/users/${id}`), onSuccess: invalidate })

  return (
    <Panel
      title="Users"
      subtitle="Human operators. Trace content access is never implied by a role and must be granted explicitly."
      actions={<Button variant="primary" icon={UserPlus} onClick={() => setCreating(true)}>Invite user</Button>}
    >
      <MutationError error={reset.error ?? revoke.error ?? del.error} />
      {users.isError ? (
        <QueryError error={users.error} retry={() => void users.refetch()} />
      ) : !users.data ? (
        <TableSkeleton />
      ) : (users.data.items ?? []).length === 0 ? (
        <EmptyState title="No users">Invite the first operator.</EmptyState>
      ) : (
        <TableScroll label="Users">
          <table className="data-table" style={{ width: '100%' }}>
            <thead>
              <tr>
                <th>User</th>
                <th>Role</th>
                <th>Sign-in</th>
                <th>Grants</th>
                <th>Last sign-in</th>
                <th aria-label="Actions" />
              </tr>
            </thead>
            <tbody>
              {(users.data.items ?? []).map((u) => (
                <tr key={u.id}>
                  <td>
                    <div>{u.display_name}</div>
                    <div className="muted">{u.email}</div>
                  </td>
                  <td>
                    <Badge tone={u.role === 'admin' ? 'accent' : 'neutral'}>{u.role}</Badge>{' '}
                    {u.disabled && <Badge tone="error">disabled</Badge>}
                    {u.role_source !== 'local' && <Badge tone="info">SSO-managed</Badge>}
                  </td>
                  <td className="secondary">
                    {[u.has_password && 'password', u.identities.length > 0 && `SSO (${u.identities.length})`].filter(Boolean).join(', ') || 'invite pending'}
                  </td>
                  <td>{u.grants.length ? u.grants.map((g) => <Badge key={g} tone="warning">{g}</Badge>) : <span className="muted">—</span>}</td>
                  <td>{fmtRelative(u.last_login_at)}</td>
                  <td style={{ whiteSpace: 'nowrap' }}>
                    <Button size="small" variant="ghost" icon={Pencil} aria-label={`Edit ${u.email}`} onClick={() => setEditing(u)} />
                    <Button size="small" variant="ghost" icon={KeyRound} aria-label={`Issue password reset for ${u.email}`} onClick={() => reset.mutate(u.id)} />
                    <Button size="small" variant="ghost" icon={LogOut} aria-label={`Sign out ${u.email} everywhere`} onClick={() => revoke.mutate(u.id)} />
                    {u.id !== me?.id && (
                      <Button size="small" variant="ghost" icon={Trash2} aria-label={`Delete ${u.email}`}
                        onClick={() => window.confirm(`Delete ${u.email}? This cannot be undone.`) && del.mutate(u.id)} />
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableScroll>
      )}
      <CreateUser open={creating} onClose={() => setCreating(false)} onCreated={(url) => { invalidate(); setCreating(false); if (url) setSecret({ label: 'invitation link', value: url }) }} />
      {editing && <EditUser user={editing} onClose={() => { setEditing(null); invalidate() }} />}
      <Dialog open={!!secret} title={secret?.label === 'invitation link' ? 'Invitation created' : 'Password reset link'} onClose={() => setSecret(null)}>
        {secret && <SecretOnce label={secret.label} value={secret.value} />}
      </Dialog>
    </Panel>
  )
}

function CreateUser({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: (inviteUrl?: string) => void }) {
  const [email, setEmail] = useState('')
  const [name, setName] = useState('')
  const [role, setRole] = useState<Role>('viewer')
  const create = useMutation({
    mutationFn: () => apiSend<{ invite_url?: string }>('POST', '/users', { email, display_name: name, role, invite: true }),
    onSuccess: (r) => {
      setEmail('')
      setName('')
      onCreated(r.invite_url)
    },
  })
  return (
    <Dialog open={open} title="Invite user" onClose={onClose}>
      <TextField label="Email" type="email" value={email} onChange={setEmail} required autoFocus />
      <TextField label="Display name" value={name} onChange={setName} maxLength={120} />
      <SelectField label="Role" value={role} onChange={setRole} options={roles} hint="Users signing in through SSO with group mappings get their role from groups." />
      <MutationError error={create.error} />
      <div className={f.actions}>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" icon={UserPlus} busy={create.isPending} disabled={!email} onClick={() => create.mutate()}>
          Create invitation
        </Button>
      </div>
    </Dialog>
  )
}

function EditUser({ user, onClose }: { user: User; onClose: () => void }) {
  const [name, setName] = useState(user.display_name)
  const [role, setRole] = useState<Role>(user.role as Role)
  const [disabled, setDisabled] = useState(user.disabled)
  const [grants, setGrants] = useState<string[]>(user.grants)
  const save = useMutation({
    mutationFn: async () => {
      const updated = await apiSend<User>('PATCH', `/users/${user.id}`, { display_name: name, role, disabled }, { ifMatch: user.revision })
      const same = grants.length === user.grants.length && grants.every((g) => user.grants.includes(g))
      if (!same) await apiSend<User>('PUT', `/users/${user.id}/grants`, { grants }, { ifMatch: updated.revision })
    },
    onSuccess: onClose,
  })
  return (
    <Dialog open title={`Edit ${user.email}`} onClose={onClose}>
      <TextField label="Display name" value={name} onChange={setName} maxLength={120} />
      <SelectField label="Role" value={role} onChange={setRole} options={roles}
        hint={user.role_source !== 'local' ? 'This role is currently managed by SSO groups; changing it makes it local.' : undefined} />
      <Checkbox label="Disabled (signs the user out everywhere)" checked={disabled} onChange={setDisabled} />
      <fieldset style={{ border: 'none', padding: 0, margin: 0, display: 'flex', flexDirection: 'column', gap: 6 }}>
        <legend className={f.label}>Trace content grants (audited)</legend>
        {contentGrants.map((g) => (
          <Checkbox key={g.value} label={g.label} checked={grants.includes(g.value)}
            onChange={(v) => setGrants(v ? [...grants, g.value] : grants.filter((x) => x !== g.value))} />
        ))}
      </fieldset>
      <MutationError error={save.error} />
      <div className={f.actions}>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} onClick={() => save.mutate()}>Save</Button>
      </div>
    </Dialog>
  )
}
