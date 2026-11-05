import { type FormEvent, useMemo, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Group, PasswordInput, SimpleGrid, Stack, Text, Tooltip } from '@mantine/core'
import { KeyRound, Link2, LogOut } from 'lucide-react'
import { Badge, Banner, Button, KeyValues, Panel } from '@/components/ui'
import { ConfirmButton, MutationError, NewPasswordFields, newPasswordReady, useFieldErrors } from '@/components/form'
import { TableSkeleton, QueryError } from '@/components/states'
import { apiSend } from '@/lib/api/client'
import { mySessionsQuery, qk, sessionQuery } from '@/lib/api/queries'
import type { WebSession } from '@/lib/api/types'
import { fmtDateTime, fmtRelative, serverNow } from '@/lib/format'
import { DataTable, PageHeader } from '@/ui/kit'

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

/** "Ends after 30 min idle": a session expiry that slides with activity. */
export function idleText(s: Pick<WebSession, 'expires_at'>, now = serverNow()): string {
  const min = Math.round((new Date(s.expires_at).getTime() - now) / 60_000)
  if (min <= 0) return 'Ending now'
  if (min < 120) return `Ends after ${min} min idle`
  return `Ends ${fmtRelative(s.expires_at)}`
}

/** This device first, then the most recently active. */
export function orderSessions(items: WebSession[]): WebSession[] {
  return [...items].sort((a, b) => Number(b.current) - Number(a.current) || b.last_seen_at.localeCompare(a.last_seen_at))
}

function Account() {
  const session = useQuery(sessionQuery)
  const sessions = useQuery(mySessionsQuery)
  const qc = useQueryClient()
  const user = session.data?.user
  const [cur, setCur] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [done, setDone] = useState(false)
  const changePw = useMutation({
    mutationFn: () => apiSend<void>('POST', '/session/password', { current_password: cur, new_password: next }),
    onSuccess: () => {
      setCur('')
      setNext('')
      setConfirm('')
      setDone(true)
      void qc.invalidateQueries({ queryKey: qk.mySessions })
    },
  })
  const errs = useFieldErrors(changePw.error)
  const revoke = useMutation({
    mutationFn: (id: string) => apiSend<void>('DELETE', `/session/all/${encodeURIComponent(id)}`),
    onSuccess: () => void qc.invalidateQueries({ queryKey: qk.mySessions }),
  })
  const others = useMemo(() => (sessions.data?.items ?? []).filter((x) => !x.current), [sessions.data])
  const revokeOthers = useMutation({
    mutationFn: () => Promise.all(others.map((x) => apiSend<void>('DELETE', `/session/all/${encodeURIComponent(x.id)}`))),
    onSuccess: () => void qc.invalidateQueries({ queryKey: qk.mySessions }),
  })
  const link = useMutation({
    mutationFn: (slug: string) => apiSend<{ auth_url: string }>('POST', `/auth/oidc/${encodeURIComponent(slug)}/link`),
    onSuccess: (r) => {
      window.location.assign(r.auth_url)
    },
  })
  // Any edit hides the last result, so old messages never linger.
  const edit = (f: (v: string) => void) => (v: string) => {
    if (done) setDone(false)
    if (changePw.error) changePw.reset()
    f(v)
  }
  const submit = (e: FormEvent) => {
    e.preventDefault()
    setDone(false)
    changePw.mutate()
  }
  const hasPw = !!user?.has_password
  const ready = newPasswordReady(next, confirm, hasPw ? cur : undefined) && (!hasPw || cur.length > 0)

  return (
    <>
      <PageHeader title="My account" />
      {session.data?.auth_method === 'breakglass' && (
        <Banner tone="warning">You signed in with a break-glass recovery link. Set a new password and review the audit log.</Banner>
      )}
      <SimpleGrid cols={{ base: 1, md: 2 }} spacing="md">
        <Panel title="Profile">
          {user && (
            <KeyValues
              items={[
                ['Name', user.display_name || '—'],
                ['Email', user.email],
                ['Role', `${(session.data?.role ?? '').charAt(0).toUpperCase()}${(session.data?.role ?? '').slice(1)}${user.role_source === 'local' ? '' : ' (from single sign-on)'}`],
                ...(user.grants.length ? ([['Can also', user.grants.join(', ')]] as [string, string][]) : []),
                ['Last sign-in', fmtDateTime(user.last_login_at)],
              ]}
            />
          )}
          <Stack gap={6} mt="md">
            <Text size="sm" fw={600}>
              Single sign-on
            </Text>
            {user && user.identities.length === 0 && (
              <Text size="sm" c="dimmed">
                No single sign-on identity is linked.
              </Text>
            )}
            {user?.identities.map((i) => (
              <Text key={i.issuer + i.subject} size="sm">
                <span className="mono">{i.issuer}</span> <span className="muted">· last used {fmtRelative(i.last_seen_at)}</span>
              </Text>
            ))}
            {(session.data?.oidc_providers ?? []).length > 0 && (
              <Group gap={8} wrap="wrap">
                {(session.data?.oidc_providers ?? []).map((p) => (
                  <Button key={p.slug} size="small" icon={Link2} busy={link.isPending} onClick={() => link.mutate(p.slug)}>
                    Link {p.display_name}
                  </Button>
                ))}
              </Group>
            )}
            <MutationError error={link.error} />
          </Stack>
        </Panel>
        <Panel title={hasPw ? 'Change password' : 'Set a password'}>
          <form onSubmit={submit}>
            <Stack gap="sm">
              {hasPw && (
                <PasswordInput
                  label="Current password"
                  autoComplete="current-password"
                  value={cur}
                  onChange={(e) => edit(setCur)(e.currentTarget.value)}
                  error={errs.current_password}
                  required
                />
              )}
              <NewPasswordFields password={next} confirm={confirm} onPassword={edit(setNext)} onConfirm={edit(setConfirm)} current={hasPw ? cur : undefined} />
              {errs.new_password && (
                <Text size="xs" c="red">
                  {errs.new_password}
                </Text>
              )}
              <Text size="xs" c="dimmed">
                Your other sessions are signed out when the password changes.
              </Text>
              <MutationError error={changePw.error} inlineFields={['current_password', 'new_password']} />
              {done && (
                <Banner tone="success" role="status">
                  Password updated. Other sessions were signed out.
                </Banner>
              )}
              <div>
                <Button type="submit" variant="primary" icon={KeyRound} busy={changePw.isPending} disabled={!ready}>
                  {hasPw ? 'Change password' : 'Set password'}
                </Button>
              </div>
            </Stack>
          </form>
        </Panel>
      </SimpleGrid>
      <Panel
        title="Active sessions"
        actions={
          others.length > 0 ? (
            <ConfirmButton
              title="Sign out your other sessions?"
              message={`${others.length === 1 ? 'One other session' : `${others.length} other sessions`} will be signed out. This device stays signed in.`}
              confirmLabel="Sign out others"
              onConfirm={() => revokeOthers.mutate()}
              busy={revokeOthers.isPending}
              icon={LogOut}
              label="Sign out other sessions"
              variant="default"
            >
              Sign out other sessions
            </ConfirmButton>
          ) : undefined
        }
      >
        <MutationError error={revoke.error ?? revokeOthers.error} />
        {sessions.isError ? (
          <QueryError error={sessions.error} retry={() => void sessions.refetch()} />
        ) : !sessions.data ? (
          <TableSkeleton rows={2} />
        ) : (
          <DataTable
            label="Active sessions"
            rows={orderSessions(sessions.data.items ?? [])}
            rowKey={(x) => x.id}
            columns={[
              {
                id: 'device',
                header: 'Device',
                mobile: 'title',
                cell: (x) => (
                  <Group gap={6} wrap="nowrap" mih={28}>
                    <span>
                      {uaSummary(x.user_agent)} <span className="muted">· {x.ip || 'unknown address'}</span>
                    </span>
                    {x.current && <Badge tone="accent">This device</Badge>}
                  </Group>
                ),
              },
              { id: 'method', header: 'Signed in with', mobile: 'meta', cell: (x) => methodName[x.auth_method] ?? x.auth_method },
              {
                id: 'started',
                header: 'Signed in',
                mobile: 'meta',
                cell: (x) => (
                  <Tooltip label={fmtDateTime(x.created_at)}>
                    <span>{fmtRelative(x.created_at)}</span>
                  </Tooltip>
                ),
              },
              { id: 'seen', header: 'Last active', mobile: 'meta', cell: (x) => fmtRelative(x.last_seen_at) },
              {
                id: 'expires',
                header: 'Session',
                mobile: 'meta',
                cell: (x) => (
                  <Tooltip label={`Ends at ${fmtDateTime(x.expires_at)} unless used before then`}>
                    <span>{idleText(x)}</span>
                  </Tooltip>
                ),
              },
              {
                id: 'revoke',
                header: <span className="visually-hidden">Actions</span>,
                align: 'right',
                width: 110,
                mobile: 'actions',
                cell: (x) =>
                  x.current ? (
                    <span />
                  ) : (
                    <Button size="small" variant="ghost" icon={LogOut} busy={revoke.isPending && revoke.variables === x.id} onClick={() => revoke.mutate(x.id)}>
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
