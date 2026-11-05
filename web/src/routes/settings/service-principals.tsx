import { useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Bot, KeyRound, Plus, Trash2, XCircle } from 'lucide-react'
import { Badge, Button } from '@/components/ui'
import { Checkbox as MCheckbox, Group, Radio, SimpleGrid, Stack, Switch, Text, Tooltip } from '@mantine/core'
import { type Column, DataTable, Empty, Section } from '@/ui/kit'
import { ConfirmButton, Dialog, DialogActions, MutationError, NameField, RevealDialog, SelectField, TextField, useFieldErrors } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { apiSend } from '@/lib/api/client'
import { principalTokensQuery, principalsQuery, qk } from '@/lib/api/queries'
import type { ApiToken, ServicePrincipal } from '@/lib/api/types'
import { fmtDateTime, fmtRelative } from '@/lib/format'
import { plural, title } from '@/ui/glossary'

export const Route = createFileRoute('/settings/service-principals')({
  component: Principals,
})

/** What each permission allows, in plain words. */
const permissionHelp: Record<string, string> = {
  'config.read': 'See providers, clients, routing and every other setting',
  'usage.read': 'See spend, quotas and budgets',
  'activity.read': 'See requests and their routing explanation',
  'traces.metadata.read': 'List captured content (not the content itself)',
  'audit.read': 'Read the audit log',
  'config.write': 'Create, change and delete settings',
  'policy.publish': 'Publish and roll back routing plans',
  'secrets.write': 'Set API keys and other stored secrets',
  'tokens.manage': 'Issue and revoke access tokens for clients',
  'events.manage': 'Manage event relays and replay deliveries',
  'notifications.manage': 'Manage alert channels and rules, send tests',
  'compat.manage': 'Pause or resume protocol paths',
  'traces.delete': 'Delete captured content',
  'users.manage': 'Invite and manage people',
  'auth.settings': 'Change sign-in and SSO settings',
  'system.manage': 'Take backups and see instance details',
  'traces.content.read': 'Read masked captured content (audited)',
  'traces.content.unredacted': 'Read unredacted captures (audited)',
  'traces.export': 'Export captured content (audited)',
}

/** Permissions grantable to automation, grouped for the editor. */
const permissionGroups: { label: string; perms: string[] }[] = [
  { label: 'Read', perms: ['config.read', 'usage.read', 'activity.read', 'traces.metadata.read', 'audit.read'] },
  { label: 'Change', perms: ['config.write', 'policy.publish', 'secrets.write', 'tokens.manage', 'events.manage', 'notifications.manage', 'compat.manage', 'traces.delete'] },
  { label: 'Administer', perms: ['users.manage', 'auth.settings', 'system.manage'] },
  { label: 'Captured content', perms: ['traces.content.read', 'traces.content.unredacted', 'traces.export'] },
]

const presets: { value: string; label: string; description: string; perms: string[] }[] = [
  {
    value: 'terraform',
    label: 'Terraform',
    description: 'Reads and changes configuration, publishes plans, sets secrets and SSO.',
    perms: ['config.read', 'config.write', 'policy.publish', 'secrets.write', 'tokens.manage', 'events.manage', 'notifications.manage', 'compat.manage', 'auth.settings', 'usage.read'],
  },
  {
    value: 'readonly',
    label: 'Read-only',
    description: 'For CI checks and dashboards: reads settings, usage and activity.',
    perms: ['config.read', 'usage.read', 'activity.read'],
  },
  { value: 'custom', label: 'Custom', description: 'Pick each permission.', perms: [] },
]

function presetOf(perms: string[]): string {
  const same = (a: string[], b: string[]) => a.length === b.length && a.every((x) => b.includes(x))
  return presets.find((p) => p.perms.length && same(p.perms, perms))?.value ?? 'custom'
}

function permLevel(perms: string[]): string {
  const has = (l: string) => permissionGroups.find((g) => g.label === l)?.perms.some((p) => perms.includes(p))
  const parts = [has('Administer') ? 'Administer' : has('Change') ? 'Change' : 'Read']
  if (has('Captured content')) parts.push('captured content')
  return parts.join(' + ')
}

function Principals() {
  const qc = useQueryClient()
  const list = useQuery(principalsQuery)
  const [editing, setEditing] = useState<ServicePrincipal | 'new' | null>(null)
  const [tokensFor, setTokensFor] = useState<ServicePrincipal | null>(null)
  const del = useMutation({
    mutationFn: (id: string) => apiSend<void>('DELETE', `/service-principals/${id}`),
    onSuccess: () => void qc.invalidateQueries({ queryKey: qk.principals }),
  })
  const cols: Column<ServicePrincipal>[] = [
    {
      id: 'name',
      header: title('automationAccount'),
      mobile: 'title',
      cell: (sp) => (
        <Stack gap={0} miw={0}>
          <Group gap={6} wrap="nowrap">
            <Text size="sm" fw={500} truncate>
              {sp.name}
            </Text>
            {sp.disabled && <Badge tone="error">Off</Badge>}
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
        <Tooltip label={sp.permissions.map((p) => permissionHelp[p] ?? p).join(' · ')} multiline maw={360}>
          <Text size="sm" span>
            {presets.find((p) => p.value === presetOf(sp.permissions) && p.value !== 'custom')?.label ?? permLevel(sp.permissions)}{' '}
            <Text span size="xs" c="dimmed">
              ({plural(sp.permissions.length, 'permission')})
            </Text>
          </Text>
        </Tooltip>
      ),
    },
    { id: 'created', header: 'Created', mobile: 'meta', mobileLabel: 'Created', cell: (sp) => fmtDateTime(sp.created_at) },
    {
      id: 'actions',
      header: <span className="visually-hidden">Actions</span>,
      align: 'right',
      mobile: 'actions',
      cell: (sp) => (
        <Group gap={4} wrap="nowrap" justify="flex-end">
          <Button size="small" variant="ghost" icon={KeyRound} aria-label={`API tokens of ${sp.name}`} onClick={() => setTokensFor(sp)}>
            {title('apiToken', true)}
          </Button>
          <ConfirmButton size="small" icon={Trash2} label={`Delete ${sp.name}`} title={`Delete ${sp.name}?`}
            message="All of its API tokens stop working immediately." confirmLabel="Delete" onConfirm={() => del.mutate(sp.id)} />
        </Group>
      ),
    },
  ]
  return (
    <Section
      title={title('apiToken', true)}
      info="Automation accounts are for Terraform, CI and scripts. Each has only the permissions it needs and signs in with API tokens, never a browser session."
      actions={<Button variant="primary" icon={Plus} onClick={() => setEditing('new')}>New {TERMS_AUTOMATION}</Button>}
    >
      <MutationError error={del.error} />
      {list.isError ? (
        <QueryError error={list.error} retry={() => void list.refetch()} />
      ) : !list.data ? (
        <TableSkeleton />
      ) : (
        <DataTable label={title('automationAccount', true)} rows={list.data.items ?? []} columns={cols} rowKey={(sp) => sp.id} onRow={(sp) => setEditing(sp)}
          empty={<Empty icon={<Bot size={22} />} title="No automation accounts">Create one for Terraform or CI with only the permissions it needs.</Empty>} />
      )}
      {editing && <EditPrincipal sp={editing === 'new' ? null : editing} onClose={() => { setEditing(null); void qc.invalidateQueries({ queryKey: qk.principals }) }} />}
      {tokensFor && <Tokens sp={tokensFor} onClose={() => setTokensFor(null)} />}
    </Section>
  )
}

const TERMS_AUTOMATION = 'automation account'

function EditPrincipal({ sp, onClose }: { sp: ServicePrincipal | null; onClose: () => void }) {
  const [name, setName] = useState(sp?.name ?? '')
  const [description, setDescription] = useState(sp?.description ?? '')
  const [perms, setPerms] = useState<string[]>(sp?.permissions ?? presets[1]!.perms)
  const [preset, setPreset] = useState(() => presetOf(sp?.permissions ?? presets[1]!.perms))
  const [active, setActive] = useState(!(sp?.disabled ?? false))
  const save = useMutation({
    mutationFn: () => {
      const body = { name, description, permissions: perms, disabled: !active }
      return sp ? apiSend('PUT', `/service-principals/${sp.id}`, body, { ifMatch: sp.revision }) : apiSend('POST', '/service-principals', body)
    },
    onSuccess: onClose,
  })
  const errs = useFieldErrors(save.error)
  const [submitted, setSubmitted] = useState(false)
  const pick = (v: string) => {
    setPreset(v)
    const p = presets.find((x) => x.value === v)
    if (p && p.perms.length) setPerms(p.perms)
  }
  return (
    <Dialog open wide title={sp ? `Edit ${sp.name}` : `New ${TERMS_AUTOMATION}`} onClose={onClose}>
      {sp?.managed_by === 'terraform' && <Text size="sm" c="dimmed">This account is managed by Terraform; changes made here show up as drift.</Text>}
      <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md" verticalSpacing="md">
        <NameField value={name} onChange={setName} locked={!!sp} error={(submitted && !name ? 'Enter a short name.' : undefined) ?? errs['name']} autoFocus={!sp} />
        <TextField label="Description" value={description} onChange={setDescription} maxLength={500} placeholder="What uses it, e.g. Terraform in CI" error={errs['description']} />
      </SimpleGrid>
      <Radio.Group label="Access" description="Start from a preset; choose Custom to pick individual permissions." value={preset} onChange={pick}>
        <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="sm" mt={8}>
          {presets.map((p) => (
            <Radio.Card key={p.value} value={p.value} radius="md" p="sm" withBorder>
              <Group wrap="nowrap" align="flex-start" gap="sm">
                <Radio.Indicator />
                <Stack gap={2}>
                  <Text size="sm" fw={500}>
                    {p.label}
                  </Text>
                  <Text size="xs" c="dimmed">
                    {p.description}
                  </Text>
                </Stack>
              </Group>
            </Radio.Card>
          ))}
        </SimpleGrid>
      </Radio.Group>
      {preset === 'custom' && (
        <MCheckbox.Group label="Permissions" value={perms} onChange={setPerms} error={(submitted && perms.length === 0 ? 'Choose at least one permission.' : undefined) ?? errs['permissions']}>
          <Stack gap="md" mt={8}>
            {permissionGroups.map((g) => (
              <Stack key={g.label} gap={6}>
                <Text size="xs" fw={600} c="dimmed" tt="uppercase">
                  {g.label}
                </Text>
                <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="xs" verticalSpacing={6}>
                  {g.perms.map((p) => (
                    <MCheckbox key={p} value={p} label={permissionHelp[p] ?? p} description={<span className="mono">{p}</span>} />
                  ))}
                </SimpleGrid>
              </Stack>
            ))}
          </Stack>
        </MCheckbox.Group>
      )}
      {sp && (
        <Switch label="Active" description="When off, every API token of this account stops working." checked={active} onChange={(e) => setActive(e.currentTarget.checked)} />
      )}
      <MutationError error={save.error} />
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} onClick={() => { setSubmitted(true); if (name && perms.length > 0) save.mutate() }}>
          {sp ? 'Save' : `Create ${TERMS_AUTOMATION}`}
        </Button>
      </DialogActions>
    </Dialog>
  )
}

const lifetimes = [
  { value: '30', label: '30 days' },
  { value: '90', label: '90 days' },
  { value: '365', label: '1 year' },
  { value: '0', label: 'No expiry' },
]

function Tokens({ sp, onClose }: { sp: ServicePrincipal; onClose: () => void }) {
  const qc = useQueryClient()
  const tokens = useQuery(principalTokensQuery(sp.id))
  const [name, setName] = useState('')
  const [ttlDays, setTtlDays] = useState('90')
  const [plain, setPlain] = useState<string | null>(null)
  const [tried, setTried] = useState(false)
  const refresh = () => void qc.invalidateQueries({ queryKey: principalTokensQuery(sp.id).queryKey })
  const issue = useMutation({
    mutationFn: () => apiSend<{ plaintext: string }>('POST', `/service-principals/${sp.id}/tokens`, { name, ttl_seconds: Number(ttlDays) * 86400 }),
    onSuccess: (r) => {
      setPlain(r.plaintext)
      setName('')
      setTried(false)
      refresh()
    },
  })
  const revoke = useMutation({
    mutationFn: (id: string) => apiSend<void>('DELETE', `/service-principals/${sp.id}/tokens/${id}`),
    onSuccess: () => {
      setPlain(null)
      refresh()
    },
  })
  const errs = useFieldErrors(issue.error)
  const items = tokens.data?.items ?? []
  const cols: Column<ApiToken>[] = [
    {
      id: 'name',
      header: 'Name',
      mobile: 'title',
      cell: (t) => (
        <Group gap={6} wrap="nowrap">
          <Text size="sm" fw={500}>
            {t.name}
          </Text>
          {t.revoked_at && <Badge tone="error">Revoked</Badge>}
        </Group>
      ),
    },
    { id: 'prefix', header: 'Starts with', mobile: 'meta', cell: (t) => <span className="mono">hpa_{t.prefix}_…</span> },
    { id: 'expires', header: 'Expires', mobile: 'meta', mobileLabel: 'Expires', cell: (t) => (t.expires_at ? fmtDateTime(t.expires_at) : 'Never') },
    { id: 'used', header: 'Last used', mobile: 'meta', mobileLabel: 'Last used', cell: (t) => (t.last_used_at ? fmtRelative(t.last_used_at) : 'Never') },
    {
      id: 'actions',
      header: <span className="visually-hidden">Actions</span>,
      align: 'right',
      mobile: 'actions',
      cell: (t) =>
        !t.revoked_at && (
          <ConfirmButton size="small" icon={XCircle} title={`Revoke ${t.name}?`} confirmLabel="Revoke" busy={revoke.isPending}
            message="Anything using this token is refused from now on. This cannot be undone." onConfirm={() => revoke.mutate(t.id)}>
            Revoke
          </ConfirmButton>
        ),
    },
  ]
  return (
    <Dialog open wide title={`${title('apiToken', true)} · ${sp.name}`} onClose={onClose}>
      <Group align="flex-end" gap="sm" wrap="wrap">
        <div style={{ flex: '1 1 240px' }}>
          <TextField label="Token name" value={name} onChange={setName} maxLength={80} placeholder="e.g. terraform-ci" error={(tried && !name.trim() ? 'Name the token after where it is used.' : undefined) ?? errs['name']} />
        </div>
        <SelectField label="Expires after" value={ttlDays} onChange={setTtlDays} options={lifetimes} error={errs['ttl_seconds']} />
        <Button variant="primary" icon={KeyRound} busy={issue.isPending} onClick={() => { setTried(true); if (name.trim()) issue.mutate() }}>
          Issue token
        </Button>
      </Group>
      <MutationError error={issue.error ?? revoke.error} />
      {tokens.isError ? (
        <QueryError error={tokens.error} />
      ) : (
        <DataTable label="Tokens" rows={items} columns={cols} rowKey={(t) => t.id}
          empty={<Empty icon={<KeyRound size={22} />} title="No tokens yet">Issue one above; it is shown only once.</Empty>} />
      )}
      <RevealDialog open={!!plain} title="API token issued" label="API token" value={plain ?? ''} onDone={() => setPlain(null)}>
        <Text size="sm" c="dimmed">
          Send it as <span className="mono">Authorization: Bearer …</span>. Store it in your CI secrets or the Terraform provider's <span className="mono">HP_API_TOKEN</span>.
        </Text>
      </RevealDialog>
    </Dialog>
  )
}
