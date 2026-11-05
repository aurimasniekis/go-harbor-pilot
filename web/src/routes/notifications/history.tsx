import { useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Group, Stack, Text } from '@mantine/core'
import { CheckCircle2, RotateCcw } from 'lucide-react'
import { Badge, Banner, EmptyState, type Tone } from '@/components/ui'
import { ConfirmButton, MutationError, SelectField } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { apiSend } from '@/lib/api/client'
import { notificationsQuery, resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { ChannelRes, NotificationItem } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { fmtDateTime, fmtRelative } from '@/lib/format'
import { severityTone } from '@/features/events/events'
import { type Column, DataTable, Section } from '@/ui/kit'
import { plural } from '@/ui/glossary'

export const Route = createFileRoute('/notifications/history')({
  component: History,
})

const statusTone: Record<NotificationItem['status'], Tone> = { sent: 'success', pending: 'info', digest: 'warning', digested: 'neutral', dead: 'error' }
const statusLabel: Record<NotificationItem['status'], string> = { sent: 'Sent', pending: 'Waiting', digest: 'Held for digest', digested: 'Sent in a digest', dead: 'Failed' }
type StatusFilter = '' | NotificationItem['status']

/** "Sent 2m ago", "Failed after 3 attempts", "Waiting". */
function statusLine(n: NotificationItem): string {
  if (n.status === 'sent') return n.sent_at ? `Sent ${fmtRelative(n.sent_at)}` : 'Sent'
  if (n.status === 'dead') return `Failed after ${plural(n.attempts, 'attempt')}`
  return `${statusLabel[n.status]} · created ${fmtRelative(n.created_at)}`
}

function History() {
  const qc = useQueryClient()
  const channels = useQuery(resourceListQuery<ChannelRes>('notification-channels'))
  const ch = byId(channels.data?.items)
  const [channel, setChannel] = useState('')
  const [status, setStatus] = useState<StatusFilter>('')
  const list = useQuery(notificationsQuery(channel || undefined, status || undefined))
  const items = list.data?.items ?? []
  const canReplay = useCan('notifications.manage')
  const [done, setDone] = useState<string | null>(null)
  const replay = useMutation({
    mutationFn: (n: NotificationItem) => apiSend<{ queued: boolean }>('POST', `/notifications/${encodeURIComponent(n.id)}/replay`, {}).then(() => n),
    onSuccess: (n) => {
      setDone(n.title)
      void qc.invalidateQueries({ queryKey: ['notifications', 'history'] })
    },
  })
  const channelName = (id: string) => ch[id]?.name ?? (channels.data ? 'Deleted channel' : '…')
  const sent = items.filter((n) => n.status === 'sent' || n.status === 'digested').length
  const failed = items.filter((n) => n.status === 'dead').length

  const cols: Column<NotificationItem>[] = [
    {
      id: 'message',
      header: 'Message',
      mobile: 'title',
      cell: (n) =>
        n.kind === 'test' ? (
          <Group gap={6} wrap="nowrap">
            <Badge>Test</Badge>
            <Text size="sm" c="dimmed">
              Test message
            </Text>
          </Group>
        ) : (
          <Stack gap={2} miw={0}>
            <Group gap={6} wrap="wrap">
              <Text size="sm" fw={500}>
                {n.title}
              </Text>
              {n.kind === 'recovery' && <Badge tone="success">Recovered</Badge>}
              {n.kind === 'digest' && <Badge tone="info">Digest</Badge>}
              {n.kind === 'alert' && n.severity && n.severity !== 'info' && <Badge tone={severityTone(n.severity)}>{n.severity === 'critical' ? 'Critical' : 'Warning'}</Badge>}
            </Group>
            {n.body && (
              <Text size="xs" c="dimmed" lineClamp={2} style={{ whiteSpace: 'pre-line' }}>
                {n.body}
              </Text>
            )}
          </Stack>
        ),
    },
    { id: 'channel', header: 'Channel', mobile: 'meta', mobileLabel: 'Channel', cell: (n) => (ch[n.channel_id] ? channelName(n.channel_id) : <span className="muted">{channelName(n.channel_id)}</span>) },
    {
      id: 'status',
      header: 'Status',
      mobile: 'aside',
      cell: (n) => <Badge tone={statusTone[n.status]}>{statusLabel[n.status]}</Badge>,
    },
    {
      id: 'when',
      header: 'When',
      mobile: 'line',
      cell: (n) => (
        <Stack gap={0}>
          <Text size="xs" c="dimmed" title={fmtDateTime(n.created_at, { seconds: true })}>
            {statusLine(n)}
          </Text>
          {n.last_error && (
            <Text size="xs" c="var(--error-text)" style={{ overflowWrap: 'anywhere' }}>
              {n.last_error}
            </Text>
          )}
        </Stack>
      ),
    },
    {
      id: 'actions',
      header: <span className="visually-hidden">Actions</span>,
      align: 'right',
      mobile: 'actions',
      cell: (n) =>
        canReplay && n.status === 'dead' && n.kind !== 'test' ? (
          <ConfirmButton size="small" variant="default" icon={RotateCcw} label={`Retry notification ${n.title}`} title="Send this alert again?"
            message="It goes back into the queue and is retried like a new message." confirmLabel="Send again" danger={false} onConfirm={() => { replay.reset(); setDone(null); replay.mutate(n) }}>
            Retry
          </ConfirmButton>
        ) : null,
    },
  ]

  return (
    <Section title="History" info="Messages sent, held for a digest, or failed after retries. Refreshes every 15 seconds.">
      <Group gap="md" align="flex-end" wrap="wrap">
        <SelectField label="Channel" value={channel} onChange={setChannel}
          options={[{ value: '', label: 'All channels' }, ...(channels.data?.items ?? []).map((c) => ({ value: c.id, label: c.name }))]} />
        <SelectField<StatusFilter> label="Status" value={status} onChange={setStatus}
          options={[{ value: '', label: 'Any status' }, ...(Object.keys(statusLabel) as NotificationItem['status'][]).map((s) => ({ value: s, label: statusLabel[s] }))]} />
        {list.data && items.length > 0 && (
          <Text size="sm" c="dimmed" pb={8}>
            {sent.toLocaleString()} sent · {failed.toLocaleString()} failed
          </Text>
        )}
      </Group>
      <MutationError error={replay.error} />
      {done && (
        <Banner tone="success" icon={CheckCircle2} role="status">
          Queued <strong>{done}</strong> for another try.
        </Banner>
      )}
      {list.isError ? (
        <QueryError error={list.error} retry={() => void list.refetch()} />
      ) : !list.data ? (
        <TableSkeleton />
      ) : items.length === 0 ? (
        <EmptyState title={channel || status ? 'No matching messages' : 'No messages yet'}>
          Messages appear here when a rule matches an event, or after you send a test from a channel.
        </EmptyState>
      ) : (
        <DataTable label="Notification history" rows={items} columns={cols} rowKey={(n) => n.id} />
      )}
    </Section>
  )
}
