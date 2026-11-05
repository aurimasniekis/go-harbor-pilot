import { useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { KeyRound, LogOut, MailPlus, MoreHorizontal, Pencil, Trash2, Unlink, UserPlus } from 'lucide-react'
import { Badge, Button } from '@/components/ui'
import { ActionIcon, Checkbox as MCheckbox, Group, Menu, Stack, Switch, Text } from '@mantine/core'
import { type Column, DataTable, Empty, Section } from '@/ui/kit'
import { ConfirmDialog, Dialog, DialogActions, MutationError, RevealDialog, SelectField, TextField, useFieldErrors } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { apiSend } from '@/lib/api/client'
import { qk, sessionQuery, usersQuery, oidcProvidersQuery } from '@/lib/api/queries'
import type { OIDCProvider, User } from '@/lib/api/types'
import { fmtDateTime, fmtRelative } from '@/lib/format'

export const Route = createFileRoute('/settings/users')({
  component: Users,
})

type Role = 'admin' | 'operator' | 'viewer'
const roles: { value: Role; label: string }[] = [
  { value: 'viewer', label: 'Viewer — read configuration, usage and activity' },
  { value: 'operator', label: 'Operator — manage configuration, policies and tokens' },
  { value: 'admin', label: 'Admin — everything, including users and sign-in' },
]
const roleName: Record<string, string> = { admin: 'Admin', operator: 'Operator', viewer: 'Viewer' }
const contentGrants = [
  { value: 'traces.content.read', label: 'Read masked captured content' },
  { value: 'traces.content.unredacted', label: 'Read unredacted diagnostic captures' },
  { value: 'traces.export', label: 'Export captured content' },
]
const grantName: Record<string, string> = Object.fromEntries(contentGrants.map((g) => [g.value, g.label]))

/** Simple shape check; the server has the final say. */
const emailRE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

/** An account that was invited but never set a password or signed in. */
function invitationPending(u: User): boolean {
  return !u.last_login_at && !u.has_password && u.identities.length === 0
}

function signInState(u: User): string {
  if (u.last_login_at) return fmtRelative(u.last_login_at)
  if (invitationPending(u)) return u.sso_link_provider ? 'Waiting for first SSO sign-in' : 'Invitation pending'
  return 'Never'
}

type Reveal = { title: string; label: string; value: string }

function Users() {
  const qc = useQueryClient()
  const users = useQuery(usersQuery)
  const providers = useQuery(oidcProvidersQuery)
  const me = useQuery(sessionQuery).data?.user
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<User | null>(null)
  const [reveal, setReveal] = useState<Reveal | null>(null)
  const [removing, setRemoving] = useState<User | null>(null)
  const [signingOut, setSigningOut] = useState<User | null>(null)
  const [notice, setNotice] = useState('')
  const invalidate = () => void qc.invalidateQueries({ queryKey: qk.users })

  const link = useMutation({
    mutationFn: (u: User) => apiSend<{ reset_url: string }>('POST', `/users/${u.id}/password-reset`).then((r) => ({ u, url: r.reset_url })),
    onSuccess: ({ u, url }) =>
      setReveal(
        invitationPending(u)
          ? { title: `New invitation link for ${u.email}`, label: 'invitation link', value: url }
          : { title: `Password reset link for ${u.email}`, label: 'password reset link', value: url },
      ),
  })
  const revoke = useMutation({
    mutationFn: (u: User) => apiSend<void>('POST', `/users/${u.id}/revoke-sessions`).then(() => u),
    onSuccess: (u) => setNotice(`${u.display_name || u.email} was signed out everywhere.`),
  })
  const del = useMutation({
    mutationFn: (u: User) => apiSend<void>('DELETE', `/users/${u.id}`).then(() => u),
    onSuccess: (u) => {
      setNotice(`${u.email} was removed.`)
      invalidate()
    },
  })

  const list = users.data?.items ?? []
  const activeAdmins = list.filter((u) => u.role === 'admin' && !u.disabled).length
  const ssoName = (id?: string) => (providers.data?.items ?? []).find((p) => p.id === id)?.display_name

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
            {u.disabled && <Badge tone="error">Cannot sign in</Badge>}
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
      only: 'desktop',
      cell: (u) =>
        u.grants.length ? (
          <Text size="sm">{u.grants.map((g) => grantName[g] ?? g).join(', ')}</Text>
        ) : (
          <Text size="sm" c="dimmed" span>
            —
          </Text>
        ),
    },
    {
      id: 'signin',
      header: 'Last sign-in',
      mobile: 'meta',
      mobileLabel: 'Last sign-in',
      cell: (u) => signInState(u),
    },
    {
      id: 'actions',
      header: <span className="visually-hidden">Actions</span>,
      align: 'right',
      width: 48,
      mobile: 'actions',
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
            {invitationPending(u) && !u.sso_link_provider ? (
              <Menu.Item leftSection={<MailPlus size={14} />} onClick={() => link.mutate(u)}>
                New invitation link
              </Menu.Item>
            ) : (
              <Menu.Item leftSection={<KeyRound size={14} />} onClick={() => link.mutate(u)}>
                Create a password reset link
              </Menu.Item>
            )}
            <Menu.Item leftSection={<LogOut size={14} />} onClick={() => setSigningOut(u)}>
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
      <MutationError error={link.error ?? revoke.error ?? del.error} />
      {notice && (
        <Text size="sm" c="dimmed" role="status">
          {notice}
        </Text>
      )}
      {users.isError ? (
        <QueryError error={users.error} retry={() => void users.refetch()} />
      ) : !users.data ? (
        <TableSkeleton />
      ) : (
        <DataTable label="People list" rows={list} columns={cols} rowKey={(u) => u.id} onRow={setEditing}
          empty={<Empty title="No one yet">Invite the first operator.</Empty>} />
      )}
      <CreateUser
        open={creating}
        providers={providers.data?.items ?? []}
        onClose={() => setCreating(false)}
        onCreated={(email, url) => {
          invalidate()
          setCreating(false)
          setNotice('')
          if (url) setReveal({ title: `Invitation for ${email}`, label: 'invitation link', value: url })
          else setNotice(`${email} can now sign in with single sign-on.`)
        }}
      />
      {editing && (
        <EditUser
          user={editing}
          self={editing.id === me?.id}
          lastAdmin={editing.role === 'admin' && !editing.disabled && activeAdmins <= 1}
          ssoName={ssoName}
          onClose={() => {
            setEditing(null)
            invalidate()
          }}
        />
      )}
      <RevealDialog open={!!reveal} title={reveal?.title ?? ''} label={reveal?.label ?? ''} value={reveal?.value ?? ''} onDone={() => setReveal(null)}>
        <Text size="sm" c="dimmed">
          {reveal?.label === 'invitation link'
            ? 'Send this link to the person. It lets them choose a password and expires after 72 hours.'
            : 'Send this link to the person. It lets them choose a new password and expires after one hour.'}
        </Text>
      </RevealDialog>
      <ConfirmDialog
        open={!!signingOut}
        title={`Sign ${signingOut?.display_name || signingOut?.email || ''} out everywhere?`}
        message="Every browser session of this person ends now. They can sign in again."
        confirmLabel="Sign out everywhere"
        onCancel={() => setSigningOut(null)}
        onConfirm={() => {
          if (signingOut) revoke.mutate(signingOut)
          setSigningOut(null)
        }}
      />
      <ConfirmDialog
        open={!!removing}
        title={`Remove ${removing?.email ?? ''}?`}
        message="They are signed out everywhere and can no longer sign in. Their audit history stays."
        confirmLabel="Remove"
        onCancel={() => setRemoving(null)}
        onConfirm={() => {
          if (removing) del.mutate(removing)
          setRemoving(null)
        }}
      />
    </Section>
  )
}

function CreateUser({ open, providers, onClose, onCreated }: {
  open: boolean
  providers: OIDCProvider[]
  onClose: () => void
  onCreated: (email: string, inviteUrl?: string) => void
}) {
  const [email, setEmail] = useState('')
  const [name, setName] = useState('')
  const [role, setRole] = useState<Role>('viewer')
  const [sso, setSso] = useState('')
  const [tried, setTried] = useState(false)
  const ssoOptions = providers.filter((p) => p.provisioning === 'preprovisioned' && p.enabled)
  const resetForm = () => {
    setEmail('')
    setName('')
    setRole('viewer')
    setSso('')
    setTried(false)
  }
  const create = useMutation({
    mutationFn: () =>
      apiSend<{ invite_url?: string }>('POST', '/users', sso ? { email, display_name: name, role, invite: false, sso_provider: sso } : { email, display_name: name, role, invite: true }),
    onSuccess: (r) => {
      const e = email
      resetForm()
      create.reset()
      onCreated(e, r.invite_url)
    },
  })
  const errs = useFieldErrors(create.error)
  const emailBad = email !== '' && !emailRE.test(email.trim())
  const close = () => {
    resetForm()
    create.reset()
    onClose()
  }
  return (
    <Dialog open={open} title="Invite user" onClose={close}>
      <TextField label="Email" type="email" value={email} onChange={setEmail} required autoFocus autoComplete="off"
        error={emailBad ? 'Enter an email address such as name@example.com.' : tried && !email ? 'Enter the email address of the person.' : errs['email']} />
      <TextField label="Display name" value={name} onChange={setName} maxLength={120} error={errs['display_name']} />
      <SelectField label="Role" value={role} onChange={setRole} options={roles} hint="People signing in through SSO with group mappings get their role from groups." />
      {ssoOptions.length > 0 && (
        <SelectField
          label="Signs in with"
          value={sso}
          onChange={setSso}
          options={[{ value: '', label: 'Password (invitation link)' }, ...ssoOptions.map((p) => ({ value: p.id, label: p.display_name }))]}
          hint="With SSO, the first sign-in from that provider with this email links the account. Existing accounts are never linked by email alone."
        />
      )}
      <MutationError error={create.error} />
      <DialogActions>
        <Button onClick={close}>Cancel</Button>
        <Button variant="primary" icon={UserPlus} busy={create.isPending} onClick={() => { setTried(true); if (email && !emailBad) create.mutate() }}>
          {sso ? 'Create user' : 'Create invitation'}
        </Button>
      </DialogActions>
    </Dialog>
  )
}

function EditUser({ user, self, lastAdmin, ssoName, onClose }: {
  user: User
  self: boolean
  lastAdmin: boolean
  ssoName: (id?: string) => string | undefined
  onClose: () => void
}) {
  const qc = useQueryClient()
  const [name, setName] = useState(user.display_name)
  const [role, setRole] = useState<Role>(user.role as Role)
  const [canSignIn, setCanSignIn] = useState(!user.disabled)
  const [grants, setGrants] = useState<string[]>(user.grants)
  const [unlinking, setUnlinking] = useState<User['identities'][number] | null>(null)
  const [identities, setIdentities] = useState(user.identities)
  const save = useMutation({
    mutationFn: async () => {
      const updated = await apiSend<User>('PATCH', `/users/${user.id}`, { display_name: name, role, disabled: !canSignIn }, { ifMatch: user.revision })
      const same = grants.length === user.grants.length && grants.every((g) => user.grants.includes(g))
      if (!same) await apiSend<User>('PUT', `/users/${user.id}/grants`, { grants }, { ifMatch: updated.revision })
    },
    onSuccess: onClose,
  })
  const unlink = useMutation({
    mutationFn: (i: User['identities'][number]) => apiSend<void>('POST', `/users/${user.id}/unlink-identity`, { issuer: i.issuer, subject: i.subject }).then(() => i),
    onSuccess: (i) => {
      setIdentities((xs) => xs.filter((x) => !(x.issuer === i.issuer && x.subject === i.subject)))
      void qc.invalidateQueries({ queryKey: qk.users })
    },
  })
  const errs = useFieldErrors(save.error)
  const dirty = name !== user.display_name || role !== user.role || canSignIn === user.disabled || grants.join() !== user.grants.join()
  const lockReason = self ? 'You cannot change your own role or sign-in access.' : lastAdmin ? 'This is the last active admin; add another admin first.' : undefined
  return (
    <Dialog open title={`Edit ${user.display_name || user.email}`} onClose={onClose} dirty={dirty}>
      <Text size="sm" c="dimmed">
        {user.email}
      </Text>
      <TextField label="Display name" value={name} onChange={setName} maxLength={120} error={errs['display_name']} />
      <SelectField label="Role" value={role} onChange={setRole} options={roles} disabled={!!lockReason}
        hint={lockReason ?? (user.role_source !== 'local' ? 'This role is currently set by SSO groups; changing it here makes it local.' : undefined)} />
      <Switch
        label="Can sign in"
        description={lockReason ?? 'Turning this off signs the person out everywhere and blocks new sign-ins.'}
        checked={canSignIn}
        disabled={!!lockReason}
        onChange={(e) => setCanSignIn(e.currentTarget.checked)}
      />
      <MCheckbox.Group label="Captured content access" description="Separate from the role and audited on every use." value={grants} onChange={setGrants}>
        <Stack gap={6} mt={6}>
          {contentGrants.map((g) => (
            <MCheckbox key={g.value} value={g.value} label={g.label} />
          ))}
        </Stack>
      </MCheckbox.Group>
      <Stack gap={6}>
        <Text size="sm" fw={500}>
          Sign-in methods
        </Text>
        <Stack gap={4}>
          <Text size="sm">{user.has_password ? 'Password is set.' : invitationPending(user) ? 'Invitation pending: no password yet.' : 'No password.'}</Text>
          {user.sso_link_provider && (
            <Text size="sm">Will link to {ssoName(user.sso_link_provider) ?? 'single sign-on'} on the first verified sign-in.</Text>
          )}
          {identities.map((i) => (
            <Group key={`${i.issuer}|${i.subject}`} justify="space-between" wrap="nowrap" gap="xs">
              <Stack gap={0} miw={0}>
                <Text size="sm" truncate>
                  {ssoName(i.provider_id) ?? 'Single sign-on'} · <span className="mono">{i.subject}</span>
                </Text>
                <Text size="xs" c="dimmed">
                  Linked {fmtDateTime(i.linked_at)}
                  {i.last_seen_at ? ` · last used ${fmtRelative(i.last_seen_at)}` : ''}
                </Text>
              </Stack>
              <Button size="small" variant="ghost" icon={Unlink} busy={unlink.isPending} onClick={() => setUnlinking(i)}>
                Unlink
              </Button>
            </Group>
          ))}
        </Stack>
      </Stack>
      <MutationError error={save.error ?? unlink.error} />
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} disabled={!dirty} onClick={() => save.mutate()}>
          Save
        </Button>
      </DialogActions>
      <ConfirmDialog
        open={!!unlinking}
        title="Unlink this sign-in?"
        message="SSO sessions from this identity end now, and the next sign-in from it will not reach this account."
        confirmLabel="Unlink"
        onCancel={() => setUnlinking(null)}
        onConfirm={() => {
          if (unlinking) unlink.mutate(unlinking)
          setUnlinking(null)
        }}
      />
    </Dialog>
  )
}
