import { useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { KeyRound, LogOut, MoreHorizontal, Pencil, Trash2, UserPlus } from 'lucide-react'
import { Badge, Button } from '@/components/ui'
import { ActionIcon, Group, Menu, Stack, Text } from '@mantine/core'
import { type Column, DataTable, Empty, Section } from '@/ui/kit'
import { Checkbox, ConfirmDialog, Dialog, MutationError, SecretOnce, SelectField, TextField, formStyles as f } from '@/components/form'
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

  const [removing, setRemoving] = useState<User | null>(null)
  const roleName: Record<string, string> = { admin: 'Admin', operator: 'Operator', viewer: 'Viewer' }
  const grantName: Record<string, string> = Object.fromEntries(contentGrants.map((g) => [g.value, g.label]))
  const cols: Column<User>[] = [
    {
      id: 'user',
      header: 'Person',
      mobile: 'title',
      cell: (u) => (
        <Stack gap={0} miw={0}>
          <Group gap={6} wrap="nowrap">
            <Text size="sm" fw={500} truncate>
              {u.display_name || u.email}
            </Text>
            {u.id === me?.id && <Badge>You</Badge>}
            {u.disabled && <Badge tone="error">Disabled</Badge>}
          </Group>
          <Text size="xs" c="dimmed" truncate>
            {u.email}
          </Text>
        </Stack>
      ),
    },
    {
      id: 'role',
      header: 'Role',
      mobile: 'aside',
      cell: (u) => (
        <Group gap={4} wrap="nowrap">
          <Text size="sm">{roleName[u.role] ?? u.role}</Text>
          {u.role_source !== 'local' && <Badge tone="info" title="Set by single sign-on group mapping">From SSO</Badge>}
        </Group>
      ),
    },
    {
      id: 'access',
      header: 'Can also',
      mobile: 'meta',
      cell: (u) => (u.grants.length ? u.grants.map((g) => grantName[g] ?? g).join(', ') : <Text size="sm" c="dimmed" span>—</Text>),
    },
    {
      id: 'signin',
      header: 'Last sign-in',
      mobile: 'meta',
      cell: (u) => (u.last_login_at ? fmtRelative(u.last_login_at) : u.has_password || u.identities.length ? 'Never' : 'Invitation pending'),
    },
    {
      id: 'actions',
      header: <span className="visually-hidden">Actions</span>,
      align: 'right',
      width: 48,
      mobile: 'hidden',
      cell: (u) => (
        <Menu position="bottom-end" withinPortal>
          <Menu.Target>
            <ActionIcon variant="subtle" color="gray" aria-label={`Actions for ${u.email}`}>
              <MoreHorizontal size={16} />
            </ActionIcon>
          </Menu.Target>
          <Menu.Dropdown>
            <Menu.Item leftSection={<Pencil size={14} />} onClick={() => setEditing(u)}>
              Edit role and access
            </Menu.Item>
            <Menu.Item leftSection={<KeyRound size={14} />} onClick={() => reset.mutate(u.id)}>
              Create a password reset link
            </Menu.Item>
            <Menu.Item leftSection={<LogOut size={14} />} onClick={() => revoke.mutate(u.id)}>
              Sign out everywhere
            </Menu.Item>
            {u.id !== me?.id && (
              <Menu.Item color="red" leftSection={<Trash2 size={14} />} onClick={() => setRemoving(u)}>
                Remove
              </Menu.Item>
            )}
          </Menu.Dropdown>
        </Menu>
      ),
    },
  ]

  return (
    <Section
      title="People"
      info="People who can sign in. Roles: Viewer reads, Operator changes configuration, Admin also manages people and sign-in. Reading captured content always needs an extra grant."
      actions={<Button variant="primary" icon={UserPlus} onClick={() => setCreating(true)}>Invite user</Button>}
    >
      <MutationError error={reset.error ?? revoke.error ?? del.error} />
      {revoke.isSuccess && <Text size="sm" c="dimmed" role="status">Signed out everywhere.</Text>}
      {users.isError ? (
        <QueryError error={users.error} retry={() => void users.refetch()} />
      ) : !users.data ? (
        <TableSkeleton />
      ) : (
        <DataTable label="People list" rows={users.data.items ?? []} columns={cols} rowKey={(u) => u.id} onRow={setEditing}
          empty={<Empty title="No one yet">Invite the first operator.</Empty>} />
      )}
      <CreateUser open={creating} onClose={() => setCreating(false)} onCreated={(url) => { invalidate(); setCreating(false); if (url) setSecret({ label: 'invitation link', value: url }) }} />
      {editing && <EditUser user={editing} onClose={() => { setEditing(null); invalidate() }} />}
      <Dialog open={!!secret} title={secret?.label === 'invitation link' ? 'Invitation created' : 'Password reset link'} onClose={() => setSecret(null)}>
        {secret && <SecretOnce label={secret.label} value={secret.value} />}
      </Dialog>
      <ConfirmDialog
        open={!!removing}
        title={`Remove ${removing?.email ?? ''}?`}
        message="They are signed out everywhere and can no longer sign in. This cannot be undone."
        confirmLabel="Remove"
        onCancel={() => setRemoving(null)}
        onConfirm={() => {
          if (removing) del.mutate(removing.id)
          setRemoving(null)
        }}
      />
    </Section>
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
