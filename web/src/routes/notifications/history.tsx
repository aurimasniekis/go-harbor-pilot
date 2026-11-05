import { useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CheckCircle2, RotateCcw } from 'lucide-react'
import { Badge, Banner, Button, EmptyState, Panel, TableScroll, type Tone } from '@/components/ui'
import { MutationError, SelectField } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { apiSend } from '@/lib/api/client'
import { notificationsQuery, resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { ChannelRes, NotificationItem } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { fmtDateTime, fmtRelative } from '@/lib/format'
import { severityTone } from '@/features/events/events'
import e from '@/features/events/events.module.css'

export const Route = createFileRoute('/notifications/history')({
  component: History,
})

const statusTone: Record<NotificationItem['status'], Tone> = { sent: 'success', pending: 'info', digest: 'warning', digested: 'neutral', dead: 'error' }
const statusLabel: Record<NotificationItem['status'], string> = { sent: 'Sent', pending: 'Pending', digest: 'Waiting for digest', digested: 'In a digest', dead: 'Failed' }
type StatusFilter = '' | NotificationItem['status']

function History() {
  const qc = useQueryClient()
  const channels = useQuery(resourceListQuery<ChannelRes>('notification-channels'))
  const ch = byId(channels.data?.items)
  const [channel, setChannel] = useState('')
  const [status, setStatus] = useState<StatusFilter>('')
  const list = useQuery(notificationsQuery(channel || undefined, status || undefined))
  const items = list.data?.items ?? []
  const canReplay = useCan('notifications.manage')
  const [confirm, setConfirm] = useState<string | null>(null)
  const [done, setDone] = useState<string | null>(null) // title of the replayed notification
  const replay = useMutation({
    mutationFn: (id: string) => apiSend<{ queued: boolean }>('POST', `/notifications/${encodeURIComponent(id)}/replay`, {}),
    onSuccess: (_, id) => {
      setDone(items.find((n) => n.id === id)?.title ?? id)
      setConfirm(null)
      void qc.invalidateQueries({ queryKey: ['notifications', 'history'] })
    },
  })
  return (
    <Panel title="History" subtitle="Messages sent, deferred into digests, or failed after retries. Refreshes every 15 seconds.">
      <div className={e.toolbar}>
        <SelectField label="Channel" value={channel} onChange={setChannel}
          options={[{ value: '', label: 'All channels' }, ...(channels.data?.items ?? []).map((c) => ({ value: c.id, label: c.name }))]} />
        <SelectField<StatusFilter> label="Status" value={status} onChange={setStatus}
          options={[{ value: '', label: 'Any status' }, ...(Object.keys(statusLabel) as NotificationItem['status'][]).map((s) => ({ value: s, label: statusLabel[s] }))]} />
      </div>
      <MutationError error={replay.error} />
      {done && (
        <Banner tone="info" icon={CheckCircle2} role="status">
          Queued <strong>{done}</strong> for another attempt. It leaves the Failed list once it is pending.
        </Banner>
      )}
      {list.isError ? (
        <QueryError error={list.error} retry={() => void list.refetch()} />
      ) : !list.data ? (
        <TableSkeleton />
      ) : items.length === 0 ? (
        <EmptyState title={channel || status ? 'No matching notifications' : 'No notifications yet'}>
          Notifications appear here when a rule matches an event, or after you send a test from a channel.
        </EmptyState>
      ) : (
        <TableScroll label="Notification history">
          <table className="data-table" style={{ width: '100%' }}>
            <thead>
              <tr>
                <th>Created</th>
                <th>Message</th>
                <th>Channel</th>
                <th>Status</th>
                <th>Attempts</th>
                <th aria-label="Actions" />
              </tr>
            </thead>
            <tbody>
              {items.map((n) => (
                <tr key={n.id}>
                  <td style={{ whiteSpace: 'nowrap' }} title={fmtDateTime(n.created_at, { seconds: true })}>{fmtRelative(n.created_at)}</td>
                  <td style={{ maxWidth: 420 }}>
                    <div style={{ display: 'flex', gap: 6, alignItems: 'center', flexWrap: 'wrap' }}>
                      <strong>{n.title}</strong>
                      {n.kind !== 'alert' && <Badge tone={n.kind === 'recovery' ? 'success' : 'info'}>{n.kind}</Badge>}
                      {n.severity && <Badge tone={severityTone(n.severity)}>{n.severity}</Badge>}
                    </div>
                    {n.body && <div className="muted ident" style={{ fontSize: 'var(--text-xs)', whiteSpace: 'pre-line' }}>{n.body.length > 240 ? n.body.slice(0, 240) + '…' : n.body}</div>}
                    {n.last_error && <div className="ident" style={{ fontSize: 'var(--text-xs)', color: 'var(--error-text)' }}>{n.last_error}</div>}
                  </td>
                  <td><span className="mono">{ch[n.channel_id]?.name ?? n.channel_id}</span></td>
                  <td>
                    <Badge tone={statusTone[n.status]}>{statusLabel[n.status]}</Badge>
                    {n.status === 'sent' && n.sent_at && <div className="muted" style={{ fontSize: 'var(--text-xs)' }}>{fmtRelative(n.sent_at)}</div>}
                  </td>
                  <td className="num">{n.attempts}</td>
                  <td style={{ textAlign: 'right' }}>
                    {canReplay && n.status === 'dead' && n.kind !== 'test' && (confirm === n.id ? (
                      <span className={e.confirm} role="group" aria-label="Confirm replay" style={{ display: 'inline-flex' }}>
                        <span>Send again?</span>
                        <Button size="small" variant="primary" busy={replay.isPending} onClick={() => replay.mutate(n.id)}>Confirm</Button>
                        <Button size="small" variant="ghost" onClick={() => setConfirm(null)}>Cancel</Button>
                      </span>
                    ) : (
                      <Button size="small" icon={RotateCcw} aria-label={`Replay notification ${n.title}`} onClick={() => { replay.reset(); setDone(null); setConfirm(n.id) }}>Replay</Button>
                    ))}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableScroll>
      )}
    </Panel>
  )
}
