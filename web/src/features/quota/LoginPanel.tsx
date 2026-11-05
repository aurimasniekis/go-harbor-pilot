import { useEffect, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ExternalLink, LogIn, LogOut } from 'lucide-react'
import { Badge, Banner, Button, Panel, TableScroll, TierBadge } from '@/components/ui'
import { Dialog, Field, MutationError, formStyles as f, ConfirmButton } from '@/components/form'
import { QueryError } from '@/components/states'
import { apiSend } from '@/lib/api/client'
import { quotaLoginsQuery } from '@/lib/api/queries'
import type { QuotaLogin, Tier } from '@/lib/api/types'
import { fmtDateTime, fmtRelative } from '@/lib/format'
import { useCan } from '@/lib/session'

interface Started {
  authorize_url: string
  state: string
  instructions: string
}

/** Callback URL for providers that redirect back after authorization. */
export function loginCallbackURL(monitorID: string): string {
  return `${window.location.origin}/quotas/monitors?login=${encodeURIComponent(monitorID)}`
}

function useComplete(monitorID: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (b: { state: string; code: string }) =>
      apiSend<{ status: string; refresh: string }>('POST', `/quota/logins/${encodeURIComponent(monitorID)}/complete`, b),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['quota'] })
    },
  })
}

function SignInDialog({ login, onClose }: { login: QuotaLogin; onClose: () => void }) {
  const start = useMutation({
    mutationFn: () => apiSend<Started>('POST', `/quota/logins/${encodeURIComponent(login.monitor_id)}/start`, { callback_url: loginCallbackURL(login.monitor_id) }),
  })
  const complete = useComplete(login.monitor_id)
  const [code, setCode] = useState('')
  const started = useRef(false)
  useEffect(() => {
    if (!started.current) {
      started.current = true
      start.mutate()
    }
  }, [start])
  return (
    <Dialog open title={`Sign in: ${login.name}`} onClose={onClose}>
      {login.tier === 'unofficial' && (
        <Banner tone="warning">This sign-in uses an unofficial integration. The provider may change it or restrict third-party use.</Banner>
      )}
      <MutationError error={start.error} />
      {start.data && (
        <>
          <p>{start.data.instructions}</p>
          <a href={start.data.authorize_url} target="_blank" rel="noopener noreferrer" style={{ display: 'inline-flex', gap: 6, alignItems: 'center' }}>
            <ExternalLink size={14} aria-hidden="true" /> Open the provider sign-in page
          </a>
          <Field label="Code or redirected URL" hint="Paste what the provider shows after you approve access. It is exchanged once and never displayed again.">
            {(p) => <textarea {...p} className={f.textarea} rows={3} spellCheck={false} autoComplete="off" value={code} onChange={(e) => setCode(e.target.value)} />}
          </Field>
        </>
      )}
      <MutationError error={complete.error} />
      {complete.data && <Banner tone="info" role="status">Signed in. Poll: {complete.data.refresh.replaceAll('_', ' ')}.</Banner>}
      <div className={f.actions}>
        <Button onClick={onClose}>{complete.data ? 'Close' : 'Cancel'}</Button>
        {!complete.data && (
          <Button variant="primary" busy={complete.isPending} disabled={!start.data || !code.trim()} onClick={() => complete.mutate({ state: start.data!.state, code: code.trim() })}>
            Complete sign-in
          </Button>
        )}
      </div>
    </Dialog>
  )
}

/** Completes a key-exchange sign-in when the provider redirects back. */
export function LoginCallback({ monitorID, state, code, onDone }: { monitorID: string; state: string; code: string; onDone: () => void }) {
  const complete = useComplete(monitorID)
  const fired = useRef(false)
  useEffect(() => {
    if (!fired.current) {
      fired.current = true
      complete.mutate({ state, code })
    }
  }, [complete, state, code])
  return (
    <>
      {complete.isPending && <Banner tone="info" role="status">Completing sign-in…</Banner>}
      {complete.data && (
        <Banner tone="info" role="status">
          Signed in. <Button size="small" variant="ghost" onClick={onDone}>Dismiss</Button>
        </Banner>
      )}
      <MutationError error={complete.error} />
    </>
  )
}

/** Monitors that use in-app sign-in (options.token_source = "login"). */
export function LoginPanel() {
  const logins = useQuery(quotaLoginsQuery)
  const canWrite = useCan('secrets.write')
  const qc = useQueryClient()
  const [active, setActive] = useState<QuotaLogin | null>(null)
  const logout = useMutation({
    mutationFn: (id: string) => apiSend<void>('DELETE', `/quota/logins/${encodeURIComponent(id)}`),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['quota'] }),
  })
  const items = logins.data?.items ?? []
  if (logins.isError) return <QueryError error={logins.error} retry={() => void logins.refetch()} />
  if (items.length === 0) return null
  return (
    <Panel title="In-app sign-in" subtitle="Monitors configured with token_source login. Tokens are sealed with the master key and never displayed.">
      <MutationError error={logout.error} />
      <TableScroll label="Sign-in status by monitor">
        <table className="data-table" style={{ width: '100%' }}>
          <thead>
            <tr>
              <th>Monitor</th>
              <th>Adapter</th>
              <th>Status</th>
              <th>Token expires</th>
              <th aria-label="Actions" />
            </tr>
          </thead>
          <tbody>
            {items.map((l) => (
              <tr key={l.monitor_id}>
                <td className="mono">{l.name}</td>
                <td>
                  {l.adapter} {l.tier && <TierBadge tier={l.tier as Tier} />}
                </td>
                <td>{l.signed_in ? <Badge tone="success">Signed in{l.obtained_at ? ` ${fmtRelative(l.obtained_at)}` : ''}</Badge> : <Badge tone="warning">Not signed in</Badge>}</td>
                <td>{l.expires_at ? fmtDateTime(l.expires_at) : <span className="muted">—</span>}</td>
                <td style={{ textAlign: 'right', whiteSpace: 'nowrap' }}>
                  {canWrite && (
                    <>
                      <Button size="small" icon={LogIn} onClick={() => setActive(l)}>{l.signed_in ? 'Sign in again' : 'Sign in'}</Button>{' '}
                      {l.signed_in && (
                        <ConfirmButton size="small" icon={LogOut} busy={logout.isPending && logout.variables === l.monitor_id}
                          title={`Sign out of ${l.name}?`} message="The stored sign-in token is removed. Quota tracking stops until someone signs in again."
                          confirmLabel="Sign out" onConfirm={() => logout.mutate(l.monitor_id)}>
                          Sign out
                        </ConfirmButton>
                      )}
                    </>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableScroll>
      {active && <SignInDialog login={active} onClose={() => setActive(null)} />}
    </Panel>
  )
}
