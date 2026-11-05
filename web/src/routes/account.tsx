import { type FormEvent, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { KeyRound, Link2, LogOut } from 'lucide-react'
import { PageHeader } from '@/components/PageHeader'
import { Badge, Banner, Button, KeyValues, Panel, TableScroll } from '@/components/ui'
import { MutationError, TextField } from '@/components/form'
import { TableSkeleton, QueryError } from '@/components/states'
import { apiSend } from '@/lib/api/client'
import { mySessionsQuery, qk, sessionQuery } from '@/lib/api/queries'
import { fmtDateTime, fmtRelative } from '@/lib/format'

export const Route = createFileRoute('/account')({
  component: Account,
})

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
      <PageHeader title="My account" subtitle="Your profile, password, linked sign-in identities and active sessions." />
      {session.data?.auth_method === 'breakglass' && (
        <Banner tone="warning">You signed in with a break-glass recovery link. Set a new password and review the audit log.</Banner>
      )}
      <div style={{ display: 'grid', gap: 'var(--space-4)', gridTemplateColumns: 'repeat(auto-fit, minmax(320px, 1fr))' }}>
        <Panel title="Profile">
          {user && (
            <KeyValues
              items={[
                ['Email', user.email],
                ['Name', user.display_name],
                ['Role', <Badge>{session.data?.role}</Badge>],
                ['Role source', user.role_source === 'local' ? 'Assigned in Harbor Pilot' : 'Single sign-on groups'],
                ['Explicit grants', user.grants.length ? user.grants.join(', ') : 'None'],
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
          <TableScroll label="Active sessions">
            <table className="data-table" style={{ width: '100%' }}>
              <thead>
                <tr>
                  <th>Method</th>
                  <th>Started</th>
                  <th>Last seen</th>
                  <th>Expires</th>
                  <th>Client</th>
                  <th aria-label="Actions" />
                </tr>
              </thead>
              <tbody>
                {(sessions.data.items ?? []).map((s) => (
                  <tr key={s.id}>
                    <td>
                      {s.auth_method} {s.current && <Badge tone="accent">this session</Badge>}
                    </td>
                    <td>{fmtDateTime(s.created_at)}</td>
                    <td>{fmtRelative(s.last_seen_at)}</td>
                    <td>{fmtDateTime(s.expires_at)}</td>
                    <td className="muted" style={{ maxWidth: 320, overflowWrap: 'anywhere' }}>
                      {s.ip} {s.user_agent}
                    </td>
                    <td>
                      {!s.current && (
                        <Button size="small" variant="ghost" icon={LogOut} onClick={() => revoke.mutate(s.id)}>
                          Revoke
                        </Button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </TableScroll>
        )}
      </Panel>
    </>
  )
}
