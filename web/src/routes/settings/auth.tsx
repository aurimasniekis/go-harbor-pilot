import { useEffect, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { FlaskConical, Pencil, Plus, Save, Trash2 } from 'lucide-react'
import { z } from 'zod'
import { Badge, Banner, Button, EmptyState, KeyValues, Panel, Skeleton, TableScroll } from '@/components/ui'
import { Group, Stack, Switch, Text } from '@mantine/core'
import { Checkbox, Dialog, MutationError, SelectField, TextField, formStyles as f, ConfirmButton } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { apiGet, apiSend } from '@/lib/api/client'
import { authSettingsQuery, oidcProvidersQuery, qk } from '@/lib/api/queries'
import type { OIDCProvider } from '@/lib/api/types'

const search = z.object({ sso_test: z.string().optional().catch(undefined) })

export const Route = createFileRoute('/settings/auth')({
  validateSearch: (s) => search.parse(s),
  component: AuthSettings,
})

function AuthSettings() {
  const { sso_test } = Route.useSearch()
  return (
    <>
      {sso_test && <TestResult rid={sso_test} />}
      <SessionPolicy />
      <Providers />
    </>
  )
}

function TestResult({ rid }: { rid: string }) {
  const r = useQuery({ queryKey: ['oidc-test', rid], queryFn: ({ signal }) => apiGet<{ claims: Record<string, unknown> }>(`/auth/oidc-test-results/${rid}`, undefined, signal) })
  if (r.isError) return <QueryError error={r.error} />
  if (!r.data) return <Skeleton height={60} />
  const c = r.data.claims
  return (
    <Panel title="Test sign-in result" subtitle="No session was created. These are the claims Harbor Pilot would use.">
      <KeyValues
        items={[
          ['Issuer', <span className="mono">{String(c.iss)}</span>],
          ['Subject', <span className="mono">{String(c.sub)}</span>],
          ['Email', `${String(c.email ?? '—')} ${c.email_verified ? '(verified)' : '(not verified)'}`],
          ['Groups', Array.isArray(c.groups) && c.groups.length ? (c.groups as string[]).join(', ') : 'none'],
          ['Mapped role', c.mapped_role ? <Badge tone="accent">{String(c.mapped_role)}</Badge> : <Badge tone="error">no access</Badge>],
        ]}
      />
    </Panel>
  )
}

function SessionPolicy() {
  const qc = useQueryClient()
  const s = useQuery(authSettingsQuery)
  const [pw, setPw] = useState(true)
  const [idle, setIdle] = useState('30')
  const [abs, setAbs] = useState('12')
  useEffect(() => {
    if (s.data) {
      setPw(s.data.password_login)
      setIdle(String(s.data.idle_timeout_seconds / 60))
      setAbs(String(s.data.absolute_timeout_seconds / 3600))
    }
  }, [s.data])
  const save = useMutation({
    mutationFn: () => apiSend('PUT', '/auth/settings', { password_login: pw, idle_timeout_seconds: Number(idle) * 60, absolute_timeout_seconds: Number(abs) * 3600 }, { ifMatch: s.data?.revision }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: qk.authSettings }),
  })
  const dirty = !!s.data && (pw !== s.data.password_login || Number(idle) * 60 !== s.data.idle_timeout_seconds || Number(abs) * 3600 !== s.data.absolute_timeout_seconds)
  return (
    <Panel title="Sign-in and sessions">
      {s.isError ? <QueryError error={s.error} /> : !s.data ? <Skeleton height={80} /> : (
        <Stack gap="md">
          <Switch label="Password sign-in" description="People can sign in with email and password. Break-glass recovery always works, even when this is off."
            checked={pw} onChange={(e) => setPw(e.currentTarget.checked)} />
          <div className={f.grid}>
            <TextField label="Sign out after idle" type="number" suffix="min" value={idle} onChange={setIdle} hint="5 minutes to 24 hours." />
            <TextField label="Longest session" type="number" suffix="hours" value={abs} onChange={setAbs} hint="Up to 30 days." />
          </div>
          <MutationError error={save.error} />
          <Group gap="sm">
            <Button variant="primary" icon={Save} busy={save.isPending} disabled={!dirty} onClick={() => save.mutate()}>Save</Button>
            {save.isSuccess && !dirty && <Text size="sm" c="dimmed" role="status">Saved.</Text>}
          </Group>
        </Stack>
      )}
    </Panel>
  )
}

function Providers() {
  const qc = useQueryClient()
  const list = useQuery(oidcProvidersQuery)
  const [editing, setEditing] = useState<OIDCProvider | 'new' | null>(null)
  const del = useMutation({ mutationFn: (id: string) => apiSend<void>('DELETE', `/auth/oidc-providers/${id}`), onSuccess: () => void qc.invalidateQueries({ queryKey: qk.oidc }) })
  const test = useMutation({
    mutationFn: (id: string) => apiSend<{ auth_url: string }>('POST', `/auth/oidc-providers/${id}/test`),
    onSuccess: (r) => window.location.assign(r.auth_url),
  })
  return (
    <Panel
      title="Single sign-on (OIDC)"
      subtitle="Let people sign in with your identity provider (OIDC: authorization code with PKCE, nonce and issuer and audience checks). Accounts are matched on issuer and subject, never on email alone."
      actions={<Button variant="primary" icon={Plus} onClick={() => setEditing('new')}>Add provider</Button>}
    >
      <MutationError error={del.error ?? test.error} />
      {list.isError ? <QueryError error={list.error} /> : !list.data ? <TableSkeleton rows={2} /> : (list.data.items ?? []).length === 0 ? (
        <EmptyState title="No identity providers">Connect Porch or any OpenID Connect provider.</EmptyState>
      ) : (
        <TableScroll label="Identity providers">
          <table className="data-table" style={{ width: '100%' }}>
            <thead><tr><th>Provider</th><th>Issuer</th><th>Provisioning</th><th>Role mapping</th><th aria-label="Actions" /></tr></thead>
            <tbody>
              {(list.data.items ?? []).map((p) => (
                <tr key={p.id}>
                  <td>{p.display_name} <span className="muted mono">{p.slug}</span> {!p.enabled && <Badge>disabled</Badge>}</td>
                  <td className="mono" style={{ overflowWrap: 'anywhere' }}>{p.issuer}</td>
                  <td>{p.provisioning === 'jit' ? 'Just-in-time' : 'Pre-provisioned'}</td>
                  <td className="secondary">{p.role_mappings.length ? p.role_mappings.map((m) => `${m.group} → ${m.role}`).join(', ') : p.default_role ? `default: ${p.default_role}` : 'local roles'}</td>
                  <td style={{ whiteSpace: 'nowrap' }}>
                    <Button size="small" variant="ghost" icon={FlaskConical} onClick={() => test.mutate(p.id)}>Test</Button>
                    <Button size="small" variant="ghost" icon={Pencil} aria-label={`Edit ${p.slug}`} onClick={() => setEditing(p)} />
                    <ConfirmButton size="small" icon={Trash2} label={`Delete ${p.slug}`} title={`Delete ${p.display_name}?`}
                      message="People who signed in with it lose that sign-in method." confirmLabel="Delete" onConfirm={() => del.mutate(p.id)} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableScroll>
      )}
      {editing && <EditProvider p={editing === 'new' ? null : editing} onClose={() => { setEditing(null); void qc.invalidateQueries({ queryKey: qk.oidc }) }} />}
    </Panel>
  )
}

function EditProvider({ p, onClose }: { p: OIDCProvider | null; onClose: () => void }) {
  const [slug, setSlug] = useState(p?.slug ?? 'porch')
  const [name, setName] = useState(p?.display_name ?? 'Porch')
  const [issuer, setIssuer] = useState(p?.issuer ?? '')
  const [clientId, setClientId] = useState(p?.client_id ?? '')
  const [secret, setSecret] = useState('')
  const [scopes, setScopes] = useState((p?.scopes ?? ['openid', 'profile', 'email', 'groups']).join(' '))
  const [groupsClaim, setGroupsClaim] = useState(p?.groups_claim ?? 'porch.groups')
  const [mappings, setMappings] = useState((p?.role_mappings ?? []).map((m) => `${m.group}=${m.role}`).join('\n'))
  const [defaultRole, setDefaultRole] = useState<'' | 'viewer' | 'operator' | 'admin'>((p?.default_role as '' | 'viewer') ?? '')
  const [prov, setProv] = useState<'preprovisioned' | 'jit'>((p?.provisioning as 'jit') ?? 'preprovisioned')
  const [domains, setDomains] = useState((p?.allowed_domains ?? []).join(', '))
  const [priv, setPriv] = useState(p?.allow_private_network ?? false)
  const [enabled, setEnabled] = useState(p?.enabled ?? true)
  const save = useMutation({
    mutationFn: () => {
      const body = {
        slug, display_name: name, issuer, client_id: clientId,
        ...(secret ? { client_secret: secret } : {}),
        scopes: scopes.split(/\s+/).filter(Boolean),
        groups_claim: groupsClaim,
        role_mappings: mappings.split('\n').map((l) => l.trim()).filter(Boolean).map((l) => { const [group = '', role = ''] = l.split('='); return { group: group.trim(), role: role.trim() } }),
        default_role: defaultRole,
        provisioning: prov,
        allowed_domains: domains.split(',').map((d) => d.trim().toLowerCase()).filter(Boolean),
        allow_private_network: priv,
        enabled,
      }
      return p ? apiSend('PUT', `/auth/oidc-providers/${p.id}`, body, { ifMatch: p.revision }) : apiSend('POST', '/auth/oidc-providers', body)
    },
    onSuccess: onClose,
  })
  return (
    <Dialog open title={p ? `Edit ${p.display_name}` : 'Add identity provider'} onClose={onClose}>
      <Banner tone="info">
        Register the redirect URI <code>{window.location.origin}/api/v1/auth/oidc/callback</code> with the provider. For Porch, enable the
        application's groups disclosure and request the <code>groups</code> scope; groups arrive in <code>porch.groups</code>.
      </Banner>
      <div className={f.grid}>
        <TextField label="Slug" value={slug} onChange={setSlug} required />
        <TextField label="Display name" value={name} onChange={setName} required />
      </div>
      <TextField label="Issuer URL" value={issuer} onChange={setIssuer} placeholder="https://id.example.com" required />
      <div className={f.grid}>
        <TextField label="Client ID" value={clientId} onChange={setClientId} required />
        <TextField label="Client secret" type="password" value={secret} onChange={setSecret} autoComplete="off"
          hint={p?.client_secret_set ? 'Set. Leave empty to keep it.' : 'Write-only; leave empty for public clients.'} />
      </div>
      <TextField label="Scopes" value={scopes} onChange={setScopes} hint="Space separated; must include openid." />
      <TextField label="Groups claim" value={groupsClaim} onChange={setGroupsClaim} hint="Dotted path, e.g. porch.groups or groups." />
      <label className={f.field}>
        <span className={f.label}>Group → role mappings (one per line, group=role; highest role wins)</span>
        <textarea className={f.textarea} value={mappings} onChange={(e) => setMappings(e.target.value)} placeholder="platform-admins=admin" />
      </label>
      <div className={f.grid}>
        <SelectField label="Default role when no group matches" value={defaultRole} onChange={setDefaultRole}
          options={[{ value: '', label: 'Deny access' }, { value: 'viewer', label: 'Viewer' }, { value: 'operator', label: 'Operator' }, { value: 'admin', label: 'Admin' }]} />
        <SelectField label="Provisioning" value={prov} onChange={setProv}
          options={[{ value: 'preprovisioned', label: 'Pre-provisioned (link by verified email)' }, { value: 'jit', label: 'Just-in-time (create users)' }]} />
      </div>
      <TextField label="Allowed email domains (JIT)" value={domains} onChange={setDomains} hint="Comma separated." />
      <Checkbox label="Issuer is on a private or local network" checked={priv} onChange={setPriv} />
      <Checkbox label="Enabled on the sign-in page" checked={enabled} onChange={setEnabled} />
      <MutationError error={save.error} />
      <div className={f.actions}>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} disabled={!issuer || !clientId} onClick={() => save.mutate()}>Save</Button>
      </div>
    </Dialog>
  )
}
