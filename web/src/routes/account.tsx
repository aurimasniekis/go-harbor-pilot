import { type FormEvent, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { KeyRound, Link2, LogOut } from 'lucide-react'
import { PageHeader } from '@/components/PageHeader'
import { Badge, Banner, Button, KeyValues, Panel } from '@/components/ui'
import { DataTable } from '@/ui/kit'
import { MutationError, TextField } from '@/components/form'
import { TableSkeleton, QueryError } from '@/components/states'
import { apiSend } from '@/lib/api/client'
import { mySessionsQuery, qk, sessionQuery } from '@/lib/api/queries'
import { fmtDateTime, fmtRelative } from '@/lib/format'

export const Route = createFileRoute('/account')({
  component: Account,
})

const methodName: Record<string, string> = { password: 'Password', oidc: 'Single sign-on', breakglass: 'Recovery link' }

/** "Chrome on macOS" from a user agent string. */
export function uaSummary(ua: string | undefined | null): string {
  if (!ua) return 'Unknown device'
  const browser = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : /curl\//.test(ua) ? 'curl' : 'Browser'
  const os = /Mac OS X|Macintosh/.test(ua) ? 'macOS' : /Windows/.test(ua) ? 'Windows' : /iPhone|iPad/.test(ua) ? 'iOS' : /Android/.test(ua) ? 'Android' : /Linux/.test(ua) ? 'Linux' : ''
  return os ? `${browser} on ${os}` : browser
}

function Account() {
  const session = useQuery(sessionQuery)
  const sessions = useQuery(mySessionsQuery)
  const qc = useQueryClient()
  const user = session.data?.user
  const [cur, setCur] = useState('')
  const [next, setNext] = useState('')
  const [done, setDone] = useState(false)
  const changePw = useMutation({
    mutationFn: () => apiSend<void>('POST', '/session/password', { current_password: cur, new_password: next }),
    onSuccess: () => {
      setCur('')
      setNext('')
      setDone(true)
      void qc.invalidateQueries({ queryKey: qk.mySessions })
    },
  })
  const revoke = useMutation({
    mutationFn: (id: string) => apiSend<void>('DELETE', `/session/all/${encodeURIComponent(id)}`),
    onSuccess: () => void qc.invalidateQueries({ queryKey: qk.mySessions }),
  })
  const link = useMutation({
    mutationFn: (slug: string) => apiSend<{ auth_url: string }>('POST', `/auth/oidc/${encodeURIComponent(slug)}/link`),
    onSuccess: (r) => {
      window.location.assign(r.auth_url)
    },
  })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    setDone(false)
    changePw.mutate()
  }

  return (
    <>
      <PageHeader title="My account" />
      {session.data?.auth_method === 'breakglass' && (
        <Banner tone="warning">You signed in with a break-glass recovery link. Set a new password and review the audit log.</Banner>
      )}
      <div style={{ display: 'grid', gap: 'var(--space-4)', gridTemplateColumns: 'repeat(auto-fit, minmax(320px, 1fr))' }}>
        <Panel title="Profile">
          {user && (
            <KeyValues
              items={[
                ['Email', user.email],
                ['Name', user.display_name || '—'],
                ['Role', `${(session.data?.role ?? '').charAt(0).toUpperCase()}${(session.data?.role ?? '').slice(1)}${user.role_source === 'local' ? '' : ' (from single sign-on)'}`],
                ...(user.grants.length ? ([['Can also', user.grants.join(', ')]] as [string, string][]) : []),
                ['Last sign-in', fmtDateTime(user.last_login_at)],
              ]}
            />
          )}
        </Panel>
        <Panel title="Password">
          <form onSubmit={submit} style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            {user?.has_password && (
              <TextField label="Current password" type="password" autoComplete="current-password" value={cur} onChange={setCur} required />
            )}
            <TextField label="New password" type="password" autoComplete="new-password" value={next} onChange={setNext}
              hint="At least 12 characters. Your other sessions will be signed out." required />
            <MutationError error={changePw.error} />
            {done && <Banner tone="info" role="status">Password updated.</Banner>}
            <div>
              <Button type="submit" icon={KeyRound} busy={changePw.isPending} disabled={next.length < 12}>
                {user?.has_password ? 'Change password' : 'Set password'}
              </Button>
            </div>
          </form>
        </Panel>
        <Panel title="Linked sign-in identities">
          {user && user.identities.length === 0 && <p className="secondary">No single sign-on identity is linked.</p>}
          {user?.identities.map((i) => (
            <KeyValues key={i.issuer + i.subject} items={[['Issuer', <span className="mono">{i.issuer}</span>], ['Subject', <span className="mono">{i.subject}</span>], ['Last used', fmtRelative(i.last_seen_at)]]} />
          ))}
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginTop: 12 }}>
            {(session.data?.oidc_providers ?? []).map((p) => (
              <Button key={p.slug} size="small" icon={Link2} busy={link.isPending} onClick={() => link.mutate(p.slug)}>
                Link {p.display_name}
              </Button>
            ))}
          </div>
          <MutationError error={link.error} />
        </Panel>
      </div>
      <Panel title="Active sessions">
        {sessions.isError ? (
          <QueryError error={sessions.error} retry={() => void sessions.refetch()} />
        ) : !sessions.data ? (
          <TableSkeleton rows={2} />
        ) : (
          <DataTable
            label="Active sessions"
            rows={sessions.data.items ?? []}
            rowKey={(x) => x.id}
            columns={[
              {
                id: 'device',
                header: 'Device',
                mobile: 'title',
                cell: (x) => (
                  <span>
                    {uaSummary(x.user_agent)} <span className="muted">· {x.ip}</span> {x.current && <Badge tone="accent">This device</Badge>}
                  </span>
                ),
              },
              { id: 'method', header: 'Signed in with', mobile: 'meta', cell: (x) => methodName[x.auth_method] ?? x.auth_method },
              { id: 'seen', header: 'Last active', mobile: 'meta', cell: (x) => fmtRelative(x.last_seen_at) },
              { id: 'expires', header: 'Expires', mobile: 'meta', cell: (x) => fmtDateTime(x.expires_at) },
              {
                id: 'revoke',
                header: <span className="visually-hidden">Actions</span>,
                align: 'right',
                mobile: 'aside',
                cell: (x) =>
                  !x.current && (
                    <Button size="small" variant="ghost" icon={LogOut} onClick={() => revoke.mutate(x.id)}>
                      Sign out
                    </Button>
                  ),
              },
            ]}
          />
        )}
      </Panel>
    </>
  )
}
