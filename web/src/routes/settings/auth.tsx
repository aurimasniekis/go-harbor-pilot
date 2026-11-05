import { useEffect, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { FlaskConical, Pencil, Plus, Save, ShieldCheck, Trash2, X } from 'lucide-react'
import { z } from 'zod'
import { Badge, Banner, Button, EmptyState, KeyValues, Panel, Skeleton } from '@/components/ui'
import { ActionIcon, Group, SegmentedControl, Select, SimpleGrid, Stack, Switch, Text, TextInput } from '@mantine/core'
import {
  Advanced as AdvancedSection,
  Checkbox,
  ConfirmButton,
  ConfirmDialog,
  Dialog,
  DialogActions,
  ListField,
  MutationError,
  NameField,
  NumberField,
  SelectField,
  TextField,
  slugify,
  useFieldErrors,
} from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { apiGet, apiSend } from '@/lib/api/client'
import { authSettingsQuery, metaQuery, oidcProvidersQuery, qk } from '@/lib/api/queries'
import type { OIDCProvider } from '@/lib/api/types'
import { type Column, CopyText, DataTable } from '@/ui/kit'
import { summarizeError } from '@/lib/api/errors'

const search = z.object({ sso_test: z.string().optional().catch(undefined) })

export const Route = createFileRoute('/settings/auth')({
  validateSearch: (s) => search.parse(s),
  component: AuthSettings,
})

function AuthSettings() {
  const { sso_test } = Route.useSearch()
  return (
    <Stack gap="lg">
      {sso_test && <TestResult rid={sso_test} />}
      <SessionPolicy />
      <Providers />
    </Stack>
  )
}

function TestResult({ rid }: { rid: string }) {
  const r = useQuery({ queryKey: ['oidc-test', rid], queryFn: ({ signal }) => apiGet<{ claims: Record<string, unknown> }>(`/auth/oidc-test-results/${rid}`, undefined, signal) })
  if (r.isError) return <QueryError error={r.error} />
  if (!r.data) return <Skeleton height={60} />
  const c = r.data.claims
  return (
    <Panel title="Test sign-in result" subtitle="No session was created. These are the details Harbor Pilot would use.">
      <KeyValues
        items={[
          ['Issuer', <span className="mono">{String(c.iss)}</span>],
          ['Subject', <span className="mono">{String(c.sub)}</span>],
          ['Email', `${String(c.email ?? '—')} ${c.email_verified ? '(verified)' : '(not verified)'}`],
          ['Groups', Array.isArray(c.groups) && c.groups.length ? (c.groups as string[]).join(', ') : 'none'],
          ['Role', c.mapped_role ? <Badge tone="accent">{String(c.mapped_role)}</Badge> : <Badge tone="error">No access</Badge>],
        ]}
      />
    </Panel>
  )
}

function SessionPolicy() {
  const qc = useQueryClient()
  const s = useQuery(authSettingsQuery)
  const [pw, setPw] = useState(true)
  const [idle, setIdle] = useState<number | null>(30)
  const [abs, setAbs] = useState<number | null>(12)
  useEffect(() => {
    if (s.data) {
      setPw(s.data.password_login)
      setIdle(s.data.idle_timeout_seconds / 60)
      setAbs(s.data.absolute_timeout_seconds / 3600)
    }
  }, [s.data])
  const save = useMutation({
    mutationFn: () => apiSend('PUT', '/auth/settings', { password_login: pw, idle_timeout_seconds: (idle ?? 0) * 60, absolute_timeout_seconds: (abs ?? 0) * 3600 }, { ifMatch: s.data?.revision }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: qk.authSettings }),
  })
  const errs = useFieldErrors(save.error)
  const idleBad = idle !== null && (idle < 5 || idle > 1440) ? 'Between 5 minutes and 24 hours.' : undefined
  const absBad = abs !== null && (abs < 1 || abs > 720) ? 'Between 1 hour and 30 days.' : undefined
  const dirty = !!s.data && (pw !== s.data.password_login || (idle ?? 0) * 60 !== s.data.idle_timeout_seconds || (abs ?? 0) * 3600 !== s.data.absolute_timeout_seconds)
  return (
    <Panel title="Sign-in and sessions">
      {s.isError ? <QueryError error={s.error} /> : !s.data ? <Skeleton height={80} /> : (
        <Stack gap="md">
          <Switch label="Password sign-in" description="People can sign in with email and password. Break-glass recovery always works, even when this is off."
            checked={pw} onChange={(e) => setPw(e.currentTarget.checked)} />
          <Group gap="lg" align="flex-start" wrap="wrap">
            <NumberField label="Sign out after idle" unit="min" value={idle} onChange={setIdle} min={0} max={1440} w={200}
              hint="5 minutes to 24 hours." error={idleBad ?? errs['idle_timeout_seconds']} />
            <NumberField label="Longest session" unit="hours" value={abs} onChange={setAbs} min={0} max={720} w={200}
              hint="Up to 30 days." error={absBad ?? errs['absolute_timeout_seconds']} />
          </Group>
          <MutationError error={save.error} />
          <Group gap="sm">
            <Button variant="primary" icon={Save} busy={save.isPending} disabled={!dirty || !!idleBad || !!absBad || idle === null || abs === null} onClick={() => save.mutate()}>
              Save
            </Button>
            {dirty ? (
              <Text size="sm" c="dimmed">Unsaved changes.</Text>
            ) : save.isSuccess ? (
              <Text size="sm" c="dimmed" role="status">Saved.</Text>
            ) : null}
          </Group>
        </Stack>
      )}
    </Panel>
  )
}

function roleText(p: OIDCProvider): string {
  if (p.role_mappings.length) return p.role_mappings.map((m) => `${m.group} → ${m.role}`).join(', ')
  return p.default_role ? `Everyone: ${p.default_role}` : 'Local roles'
}

function Providers() {
  const qc = useQueryClient()
  const list = useQuery(oidcProvidersQuery)
  const [editing, setEditing] = useState<OIDCProvider | 'new' | null>(null)
  const [testing, setTesting] = useState<OIDCProvider | null>(null)
  const [testError, setTestError] = useState<{ p: OIDCProvider; error: unknown } | null>(null)
  const del = useMutation({ mutationFn: (id: string) => apiSend<void>('DELETE', `/auth/oidc-providers/${id}`), onSuccess: () => void qc.invalidateQueries({ queryKey: qk.oidc }) })
  const test = useMutation({
    mutationFn: (p: OIDCProvider) => apiSend<{ auth_url: string }>('POST', `/auth/oidc-providers/${p.id}/test`).catch((error: unknown) => {
      setTestError({ p, error })
      throw error
    }),
    onSuccess: (r) => window.location.assign(r.auth_url),
  })
  const cols: Column<OIDCProvider>[] = [
    {
      id: 'name',
      header: 'Provider',
      mobile: 'title',
      cell: (p) => (
        <Stack gap={0} miw={0}>
          <Group gap={6} wrap="nowrap">
            <Text size="sm" fw={500}>
              {p.display_name}
            </Text>
            {!p.enabled && <Badge>Off</Badge>}
          </Group>
          <Text size="xs" c="dimmed" ff="monospace" truncate>
            {p.issuer}
          </Text>
        </Stack>
      ),
    },
    { id: 'prov', header: 'New people', mobile: 'meta', cell: (p) => (p.provisioning === 'jit' ? 'Created on first sign-in' : 'Must be invited first') },
    { id: 'roles', header: 'Role from', mobile: 'meta', cell: (p) => <Text size="sm" c="dimmed">{roleText(p)}</Text> },
    {
      id: 'actions',
      header: <span className="visually-hidden">Actions</span>,
      align: 'right',
      mobile: 'actions',
      cell: (p) => (
        <Group gap={4} wrap="nowrap" justify="flex-end">
          <Button size="small" variant="ghost" icon={FlaskConical} onClick={() => { setTestError(null); setTesting(p) }}>Test</Button>
          <Button size="small" variant="ghost" icon={Pencil} onClick={() => setEditing(p)}>Edit</Button>
          <ConfirmButton size="small" icon={Trash2} label={`Delete ${p.display_name}`} title={`Delete ${p.display_name}?`}
            message="People who signed in with it lose that sign-in method." confirmLabel="Delete" onConfirm={() => del.mutate(p.id)} />
        </Group>
      ),
    },
  ]
  return (
    <Panel
      title="Single sign-on"
      subtitle="Let people sign in with your identity provider. Accounts are matched on the provider's user id, never on email alone."
      actions={<Button variant="primary" icon={Plus} onClick={() => setEditing('new')}>Add provider</Button>}
    >
      <Stack gap="sm">
        <MutationError error={del.error} />
        {testError && (
          <Banner tone="error" role="alert">
            Test of {testError.p.display_name} failed: {summarizeError(testError.error).title}
          </Banner>
        )}
        {list.isError ? <QueryError error={list.error} /> : !list.data ? <TableSkeleton rows={2} /> : (list.data.items ?? []).length === 0 ? (
          <EmptyState icon={ShieldCheck} title="No identity providers">Connect Porch or any OpenID Connect provider.</EmptyState>
        ) : (
          <DataTable label="Identity providers" rows={list.data.items ?? []} columns={cols} rowKey={(p) => p.id} onRow={setEditing} />
        )}
      </Stack>
      {editing && <EditProvider p={editing === 'new' ? null : editing} onClose={() => { setEditing(null); void qc.invalidateQueries({ queryKey: qk.oidc }) }} />}
      <ConfirmDialog
        open={!!testing}
        title={`Test ${testing?.display_name ?? ''}`}
        message={`You will be sent to ${testing?.display_name ?? 'the provider'} to sign in, then back here to see what Harbor Pilot received. No session is created.`}
        confirmLabel="Continue to sign in"
        danger={false}
        onCancel={() => setTesting(null)}
        onConfirm={() => {
          if (testing) test.mutate(testing)
          setTesting(null)
        }}
      />
    </Panel>
  )
}

type Role = '' | 'viewer' | 'operator' | 'admin'
const roleOptions = [
  { value: 'viewer', label: 'Viewer' },
  { value: 'operator', label: 'Operator' },
  { value: 'admin', label: 'Admin' },
]

function MappingRows({ rows, onChange, error }: { rows: { group: string; role: string }[]; onChange: (r: { group: string; role: string }[]) => void; error?: string }) {
  const set = (i: number, p: Partial<{ group: string; role: string }>) => onChange(rows.map((r, j) => (j === i ? { ...r, ...p } : r)))
  return (
    <Stack gap={6}>
      <Stack gap={0}>
        <Text size="sm" fw={500}>
          Group → role
        </Text>
        <Text size="xs" c="dimmed">
          People in a group get its role; with several groups the highest role wins.
        </Text>
      </Stack>
      {rows.map((r, i) => (
        <Group key={i} gap="xs" wrap="nowrap" align="flex-start">
          <TextInput aria-label={`Group ${i + 1}`} placeholder="platform-admins" value={r.group} style={{ flex: 1 }}
            onChange={(e) => set(i, { group: e.currentTarget.value })} error={!r.group.trim() ? 'Enter a group' : undefined} />
          <Select aria-label={`Role for group ${i + 1}`} data={roleOptions} value={r.role || null} placeholder="Role" w={140}
            onChange={(v) => set(i, { role: v ?? '' })} allowDeselect={false} comboboxProps={{ withinPortal: true }} error={!r.role ? 'Pick a role' : undefined} />
          <ActionIcon variant="subtle" color="gray" size="lg" aria-label={`Remove mapping ${i + 1}`} onClick={() => onChange(rows.filter((_, j) => j !== i))}>
            <X size={16} />
          </ActionIcon>
        </Group>
      ))}
      <div>
        <Button size="small" icon={Plus} onClick={() => onChange([...rows, { group: '', role: 'viewer' }])}>
          Add mapping
        </Button>
      </div>
      {error && (
        <Text size="xs" c="var(--error-text)" role="alert">
          {error}
        </Text>
      )}
    </Stack>
  )
}

function EditProvider({ p, onClose }: { p: OIDCProvider | null; onClose: () => void }) {
  const meta = useQuery(metaQuery)
  const [kind, setKind] = useState<'porch' | 'other'>(p ? (p.groups_claim === 'porch.groups' ? 'porch' : 'other') : 'other')
  const [name, setName] = useState(p?.display_name ?? '')
  const [slug, setSlug] = useState(p?.slug ?? '')
  const [slugTouched, setSlugTouched] = useState(!!p)
  const [issuer, setIssuer] = useState(p?.issuer ?? '')
  const [clientId, setClientId] = useState(p?.client_id ?? '')
  const [secret, setSecret] = useState('')
  const [scopes, setScopes] = useState<string[]>(p?.scopes ?? ['openid', 'profile', 'email', 'groups'])
  const [groupsClaim, setGroupsClaim] = useState(p?.groups_claim ?? 'groups')
  const [mappings, setMappings] = useState<{ group: string; role: string }[]>(p?.role_mappings ?? [])
  const [defaultRole, setDefaultRole] = useState<Role>((p?.default_role as Role) ?? '')
  const [prov, setProv] = useState<'preprovisioned' | 'jit'>((p?.provisioning as 'jit') ?? 'preprovisioned')
  const [domains, setDomains] = useState<string[]>(p?.allowed_domains ?? [])
  const [priv, setPriv] = useState(p?.allow_private_network ?? false)
  const [enabled, setEnabled] = useState(p?.enabled ?? true)

  const pickKind = (k: 'porch' | 'other') => {
    setKind(k)
    if (p) return
    if (k === 'porch') {
      if (!name) setName('Porch')
      if (!slugTouched) setSlug('porch')
      setGroupsClaim('porch.groups')
    } else {
      if (name === 'Porch') setName('')
      if (!slugTouched && slug === 'porch') setSlug('')
      if (groupsClaim === 'porch.groups') setGroupsClaim('groups')
    }
  }

  const save = useMutation({
    mutationFn: () => {
      const body = {
        slug, display_name: name, issuer: issuer.trim(), client_id: clientId.trim(),
        ...(secret ? { client_secret: secret } : {}),
        scopes,
        groups_claim: groupsClaim,
        role_mappings: mappings.map((m) => ({ group: m.group.trim(), role: m.role })),
        default_role: defaultRole,
        provisioning: prov,
        allowed_domains: domains.map((d) => d.trim().toLowerCase()).filter(Boolean),
        allow_private_network: priv,
        enabled,
      }
      return p ? apiSend('PUT', `/auth/oidc-providers/${p.id}`, body, { ifMatch: p.revision }) : apiSend('POST', '/auth/oidc-providers', body)
    },
    onSuccess: onClose,
  })
  const errs = useFieldErrors(save.error)
  const issuerBad = (() => {
    if (!issuer) return undefined
    try {
      const u = new URL(issuer.trim())
      if (u.protocol === 'https:' || (u.protocol === 'http:' && priv)) return undefined
      return u.protocol === 'http:' ? 'Plain http is allowed only for issuers on a private network (see below).' : 'Use an https:// URL.'
    } catch {
      return 'Enter a full URL such as https://id.example.com.'
    }
  })()
  const mappingsOK = mappings.every((m) => m.group.trim() && m.role)
  const scopesOK = scopes.includes('openid')
  const jitOK = prov !== 'jit' || domains.length > 0 || mappings.length > 0
  const valid = !!name && !!slug && !!issuer && !issuerBad && !!clientId && mappingsOK && scopesOK && jitOK
  const [submitted, setSubmitted] = useState(false)
  const need = (v: string, msg: string) => (submitted && !v.trim() ? msg : undefined)

  const publicURL = meta.data?.public_url ?? ''
  const redirect = publicURL ? `${publicURL.replace(/\/$/, '')}/api/v1/auth/oidc/callback` : ''

  return (
    <Dialog open wide title={p ? `Edit ${p.display_name}` : 'Add identity provider'} onClose={onClose}>
      {!p && (
        <SegmentedControl
          fullWidth
          value={kind}
          onChange={(v) => pickKind(v as 'porch' | 'other')}
          data={[
            { value: 'other', label: 'OpenID Connect provider' },
            { value: 'porch', label: 'Porch' },
          ]}
        />
      )}
      <Stack gap={4}>
        <Text size="sm" fw={500}>
          1. Register this redirect URI with the provider
        </Text>
        {redirect ? <CopyText value={redirect} maw="100%" /> : <Skeleton height={20} />}
        {publicURL && !publicURL.startsWith('https://') && (
          <Text size="xs" c="var(--warning)">
            The server's public URL is plain http. Providers usually require https; set public_url to the address people use.
          </Text>
        )}
        {kind === 'porch' && (
          <Text size="xs" c="dimmed">
            For Porch, also enable the application's groups disclosure and request the groups scope; groups arrive in porch.groups.
          </Text>
        )}
      </Stack>
      <Text size="sm" fw={500}>
        2. Enter the details it gives you
      </Text>
      <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md" verticalSpacing="md">
        <TextField label="Display name" value={name} required hint="Shown on the sign-in button." error={need(name, 'Enter the name people see on the sign-in button.') ?? errs['display_name']}
          onChange={(v) => {
            setName(v)
            if (!slugTouched) setSlug(slugify(v))
          }} />
        <NameField value={slug} locked={!!p} error={need(slug, 'Enter a short name.') ?? errs['slug']} onChange={(v) => { setSlug(v); setSlugTouched(true) }} />
        <TextField label="Issuer URL" value={issuer} onChange={setIssuer} placeholder="https://id.example.com" required error={issuerBad ?? need(issuer, 'Enter the issuer URL from your provider.') ?? errs['issuer']} />
        <TextField label="Client ID" value={clientId} onChange={setClientId} required error={need(clientId, 'Enter the client ID from your provider.') ?? errs['client_id']} />
        <TextField label="Client secret" type="password" value={secret} onChange={setSecret} autoComplete="off" error={errs['client_secret']}
          hint={p?.client_secret_set ? 'Stored. Leave empty to keep it.' : 'Leave empty for public clients.'} />
        <TextField label="Groups claim" value={groupsClaim} onChange={setGroupsClaim} hint="Where groups are in the ID token, e.g. groups or porch.groups." error={errs['groups_claim']} />
      </SimpleGrid>
      <Text size="sm" fw={500}>
        3. Decide who gets in
      </Text>
      <MappingRows rows={mappings} onChange={setMappings} error={errs['role_mappings']} />
      <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md" verticalSpacing="md">
        <SelectField label="Everyone else" value={defaultRole} onChange={setDefaultRole} error={errs['default_role']}
          hint="People whose groups match no mapping."
          options={[{ value: '', label: 'No access' }, { value: 'viewer', label: 'Viewer' }, { value: 'operator', label: 'Operator' }, { value: 'admin', label: 'Admin' }]} />
        <SelectField label="New people" value={prov} onChange={setProv} error={errs['provisioning']}
          options={[{ value: 'preprovisioned', label: 'Must be invited first' }, { value: 'jit', label: 'Created on first sign-in' }]}
          hint={prov === 'jit' ? 'Anyone the provider lets in who matches a domain or group below.' : 'Invite people in People and choose this provider.'} />
      </SimpleGrid>
      {prov === 'jit' && (
        <ListField label="Allowed email domains" value={domains} onChange={setDomains} placeholder="example.com" splitChars={[',', ' ', '\n']}
          hint="Only people with these email domains are created." error={!jitOK ? 'Add an allowed domain or a group mapping, so not everyone at the provider gets in.' : errs['allowed_domains']} />
      )}
      <Switch label="Show on the sign-in page" checked={enabled} onChange={(e) => setEnabled(e.currentTarget.checked)} />
      <AdvancedProvider scopes={scopes} setScopes={setScopes} scopesOK={scopesOK} scopesError={errs['scopes']} priv={priv} setPriv={setPriv} />
      <MutationError error={save.error} />
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} onClick={() => { setSubmitted(true); if (valid) save.mutate() }}>
          {p ? 'Save' : 'Add provider'}
        </Button>
      </DialogActions>
    </Dialog>
  )
}

function AdvancedProvider({ scopes, setScopes, scopesOK, scopesError, priv, setPriv }: {
  scopes: string[]
  setScopes: (v: string[]) => void
  scopesOK: boolean
  scopesError?: string
  priv: boolean
  setPriv: (v: boolean) => void
}) {
  return (
    <AdvancedSection>
      <ListField label="Scopes" value={scopes} onChange={setScopes} suggestions={['openid', 'profile', 'email', 'groups']} splitChars={[' ', ',']}
        error={!scopesOK ? 'Include the openid scope.' : scopesError} />
      <Checkbox label="Issuer is on a private or local network" checked={priv} onChange={setPriv}
        description="Allows plain http and private addresses for the issuer. Cloud metadata addresses stay blocked." />
    </AdvancedSection>
  )
}
