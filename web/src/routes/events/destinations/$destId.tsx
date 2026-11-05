import { useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ActionIcon, Group, Menu, SegmentedControl, Select, Stack, Text } from '@mantine/core'
import { CheckCircle2, History, MoreHorizontal, Pencil, RotateCcw } from 'lucide-react'
import { Badge, Banner, Button, EmptyState, Skeleton, type Tone } from '@/components/ui'
import { ConfirmDialog, DateTimeField, Dialog, DialogActions, MutationError } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { apiSend } from '@/lib/api/client'
import { deliveriesQuery, eventCatalogQuery, resourceQuery } from '@/lib/api/queries'
import type { Delivery, DestinationRes, EventKindInfo } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { fmtDateTime, fmtNumber, fmtRelative } from '@/lib/format'
import { EnabledSwitch } from '@/features/config/ResourceList'
import { DestinationDialog } from '@/features/events/DestinationDialog'
import { eventLabel, eventsPhrase } from '@/features/events/events'
import { type Column, CopyText, DataTable, Fields, PageHeader, Section, useCompact } from '@/ui/kit'
import { deliveryStatus, managedByName, plural } from '@/ui/glossary'

export const Route = createFileRoute('/events/destinations/$destId')({
  component: DestinationDetail,
})

const statusTone: Record<Delivery['status'], Tone> = { delivered: 'success', pending: 'info', deferred: 'warning', dead: 'error' }
type StatusFilter = '' | Delivery['status']
const statusFilters: { value: StatusFilter; label: string }[] = [
  { value: '', label: 'All' },
  { value: 'dead', label: deliveryStatus.dead! },
  { value: 'deferred', label: deliveryStatus.deferred! },
  { value: 'pending', label: deliveryStatus.pending! },
  { value: 'delivered', label: deliveryStatus.delivered! },
]

/** "harborpilot.quota.window_exhausted.v1" → the catalog name, e.g. quota.window.exhausted. */
function eventName(type: string, catalog: EventKindInfo[]): string {
  const k = catalog.find((x) => x.type === type)
  if (k) return k.name
  const cfg = /^harborpilot\.config\.(.+)_(created|updated|deleted)\.v\d+$/.exec(type)
  if (cfg) return `config.${cfg[1]}.${cfg[2]}`
  return type.replace(/^harborpilot\./, '').replace(/\.v\d+$/, '')
}

function eventText(name: string): string {
  const cfg = /^config\.(.+)\.(created|updated|deleted)$/.exec(name)
  if (cfg) return `${(cfg[1] ?? '').replace(/_/g, ' ')} ${cfg[2]}`.replace(/^./, (c) => c.toUpperCase())
  return eventLabel(name)
}

type Replay = { kind: 'dead' } | { kind: 'one'; id: string } | { kind: 'since'; since: string }

const sincePresets = [
  { value: '15', label: '15 min' },
  { value: '60', label: '1 hour' },
  { value: '360', label: '6 hours' },
  { value: '1440', label: '24 hours' },
  { value: 'custom', label: 'Custom' },
]

/** Re-send every matching event since a time, with quick presets. */
function ResendDialog({ open, onClose, onConfirm, busy }: { open: boolean; onClose: () => void; onConfirm: (since: string) => void; busy: boolean }) {
  const [preset, setPreset] = useState('60')
  const [custom, setCustom] = useState('')
  const since = preset === 'custom' ? custom : new Date(Date.now() - Number(preset) * 60_000).toISOString()
  const valid = !!since && new Date(since).getTime() < Date.now()
  return (
    <Dialog open={open} title="Re-send events" onClose={onClose} dirty={false}>
      <Text size="sm" c="dimmed">
        Every event this relay matches since the chosen time is sent again. Receivers may see duplicates and should deduplicate by event id.
      </Text>
      <Stack gap={4}>
        <Text size="sm" fw={500}>
          From the last
        </Text>
        <SegmentedControl value={preset} onChange={setPreset} data={sincePresets} style={{ alignSelf: 'flex-start' }} aria-label="Re-send window" />
      </Stack>
      {preset === 'custom' && <DateTimeField label="Since" value={custom} onChange={setCustom} error={custom && !valid ? 'Choose a time in the past.' : undefined} />}
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" icon={History} busy={busy} disabled={!valid} onClick={() => onConfirm(since)}>
          Re-send
        </Button>
      </DialogActions>
    </Dialog>
  )
}

function DestinationDetail() {
  const { destId } = Route.useParams()
  const qc = useQueryClient()
  const compact = useCompact()
  const dest = useQuery(resourceQuery<DestinationRes>('event-destinations', destId))
  const catalog = useQuery(eventCatalogQuery)
  const [status, setStatus] = useState<StatusFilter>('')
  const deliveries = useQuery(deliveriesQuery(destId, status || undefined))
  const failed = useQuery(deliveriesQuery(destId, 'dead'))
  const canWrite = useCan('config.write')
  const canReplay = useCan('events.manage')
  const [editing, setEditing] = useState(false)
  const [confirm, setConfirm] = useState<Replay | null>(null)
  const [resending, setResending] = useState(false)
  const [queued, setQueued] = useState<number | null>(null)
  const replay = useMutation({
    mutationFn: (r: Replay) =>
      apiSend<{ queued: number }>('POST', `/event-destinations/${encodeURIComponent(destId)}/replay`,
        r.kind === 'one' ? { delivery_id: r.id } : r.kind === 'since' ? { since: r.since } : {}),
    onSuccess: (res) => {
      setQueued(res.queued)
      setConfirm(null)
      setResending(false)
      void qc.invalidateQueries({ queryKey: ['events', 'deliveries', destId] })
    },
  })
  const ask = (r: Replay) => {
    setQueued(null)
    replay.reset()
    setConfirm(r)
  }

  if (dest.isError) return <QueryError error={dest.error} retry={() => void dest.refetch()} back={{ to: '/events/destinations', label: 'Back to event relays' }} />
  const d = dest.data
  const items = deliveries.data?.items ?? []
  const failedCount = failed.data?.items?.length ?? 0
  const lastDelivered = items.find((x) => x.status === 'delivered')
  const sec = d?.secrets ?? {}
  const kinds = catalog.data?.items ?? []

  const cols: Column<Delivery>[] = [
    {
      id: 'event',
      header: 'Event',
      mobile: 'title',
      cell: (x) => {
        const name = eventName(x.event_type, kinds)
        return (
          <Stack gap={0} miw={0}>
            <Text size="sm">{eventText(name)}</Text>
            <Text size="xs" c="dimmed" ff="monospace" truncate title={x.event_id}>
              {name}
            </Text>
          </Stack>
        )
      },
    },
    { id: 'status', header: 'Status', mobile: 'aside', cell: (x) => <Badge tone={statusTone[x.status]}>{deliveryStatus[x.status] ?? x.status}</Badge> },
    {
      id: 'when',
      header: 'Happened',
      mobile: 'meta',
      cell: (x) => <span title={fmtDateTime(x.occurred_at, { seconds: true })}>{fmtRelative(x.occurred_at)}</span>,
    },
    { id: 'attempts', header: 'Attempts', align: 'right', mobile: 'meta', cell: (x) => fmtNumber(x.attempts) },
    {
      id: 'result',
      header: 'Last result',
      mobile: 'line',
      cell: (x) =>
        x.last_status || x.last_error ? (
          <Text size="xs" c="dimmed" style={{ overflowWrap: 'anywhere' }}>
            {x.last_status ? <span className="mono">HTTP {x.last_status}</span> : null}
            {x.last_status && x.last_error ? ' · ' : null}
            {x.last_error}
          </Text>
        ) : (
          <Text size="xs" c="dimmed">
            —
          </Text>
        ),
    },
    {
      id: 'next',
      header: 'Next try / sent',
      mobile: 'meta',
      cell: (x) => (x.status === 'delivered' ? `Sent ${fmtRelative(x.delivered_at)}` : x.status === 'dead' ? 'No more tries' : `Next try ${fmtRelative(x.next_attempt_at)}`),
    },
    {
      id: 'actions',
      header: <span className="visually-hidden">Actions</span>,
      align: 'right',
      mobile: 'actions',
      cell: (x) =>
        canReplay && x.status === 'dead' ? (
          <Button size="small" icon={RotateCcw} aria-label={`Retry delivery ${x.id}`} onClick={() => ask({ kind: 'one', id: x.id })}>
            Retry
          </Button>
        ) : null,
    },
  ]

  return (
    <Stack gap="lg">
      <PageHeader
        crumbs={[{ label: 'Alerts', to: '/notifications' }, { label: 'Event relays', to: '/events/destinations' }, { label: d?.name }]}
        title={d ? d.name : <Skeleton width={200} height={28} />}
        context={d ? `${d.spec.type === 'pulse' ? 'Pulse (NATS)' : 'Signed webhook'} · ${eventsPhrase(d.spec.filter?.events, d.spec.filter?.min_severity)}` : undefined}
        actions={
          d && (
            <Group gap="xs" wrap="nowrap">
              {canWrite && (
                <Button icon={Pencil} onClick={() => setEditing(true)}>
                  Edit
                </Button>
              )}
              {canReplay && (
                <Menu position="bottom-end" withinPortal>
                  <Menu.Target>
                    <ActionIcon variant="default" size="lg" aria-label="More relay actions">
                      <MoreHorizontal size={16} />
                    </ActionIcon>
                  </Menu.Target>
                  <Menu.Dropdown>
                    <Menu.Item leftSection={<History size={14} />} onClick={() => { setQueued(null); setResending(true) }}>
                      Re-send events since…
                    </Menu.Item>
                  </Menu.Dropdown>
                </Menu>
              )}
            </Group>
          )
        }
      />

      {!d ? (
        <Skeleton height={100} />
      ) : (
        <Section title="Relay">
          <Fields
            cols={3}
            items={[
              ['Status', <EnabledSwitch plural="event-destinations" r={d} offLabel="Off — not relaying" />],
              ['Sends to', <CopyText value={(d.spec.type === 'pulse' ? d.spec.pulse_url : d.spec.webhook_url) ?? ''} maw="100%" />],
              ['Events', eventsPhrase(d.spec.filter?.events, d.spec.filter?.min_severity)],
              ...(Object.keys(d.spec.filter?.subjects ?? {}).length
                ? ([['Only about', <span className="mono">{Object.entries(d.spec.filter?.subjects ?? {}).map(([k, v]) => `${k}=${v}`).join(', ')}</span>]] as [React.ReactNode, React.ReactNode][])
                : []),
              ['Retries', `Gives up after ${plural(d.spec.max_attempts ?? 50, 'attempt')} or ${plural(d.spec.max_age_hours ?? 24, 'hour')}`],
              d.spec.type === 'webhook'
                ? ['Signing', sec.signing_secret?.source === 'stored'
                    ? <Group gap={6}><Badge tone="success">Signed</Badge>{sec.previous_signing_secret?.source === 'stored' && <Badge tone="warning">Rotating: two signatures</Badge>}</Group>
                    : <Badge tone="warning">Unsigned</Badge>]
                : ['Credentials', sec.creds?.source === 'stored' ? 'Stored credentials file' : d.spec.creds_ref ? <span className="mono">{d.spec.creds_ref}</span> : 'None'],
              ...(d.managed_by === 'terraform' ? ([['Managed by', managedByName.terraform]] as [React.ReactNode, React.ReactNode][]) : []),
            ]}
          />
        </Section>
      )}

      <Section
        title="Deliveries"
        info="Delivery is at-least-once; receivers deduplicate by event id. Refreshes every 15 seconds."
        actions={canReplay && failedCount > 0 && <Button icon={RotateCcw} onClick={() => ask({ kind: 'dead' })}>Retry {plural(failedCount, 'failed delivery', 'failed deliveries')}</Button>}
      >
        <Text size="sm" c="dimmed">
          {lastDelivered ? `Last delivered ${fmtRelative(lastDelivered.delivered_at)}` : 'Nothing delivered yet'} · {failedCount ? plural(failedCount, 'failed delivery', 'failed deliveries') : 'no failures'}
        </Text>
        <MutationError error={replay.error} />
        {queued !== null && (
          <Banner tone="success" icon={CheckCircle2} role="status">
            {queued > 0 ? `Queued ${plural(queued, 'delivery', 'deliveries')} for another try.` : 'Nothing to retry.'}
          </Banner>
        )}
        {compact ? (
          <Select aria-label="Delivery status" data={statusFilters.map((f) => ({ value: f.value || 'all', label: f.label }))} value={status || 'all'}
            onChange={(v) => setStatus((v === 'all' ? '' : v) as StatusFilter)} allowDeselect={false} />
        ) : (
          <SegmentedControl aria-label="Delivery status" value={status || 'all'} onChange={(v) => setStatus((v === 'all' ? '' : v) as StatusFilter)}
            data={statusFilters.map((f) => ({ value: f.value || 'all', label: f.label }))} style={{ alignSelf: 'flex-start' }} />
        )}
        {deliveries.isError ? (
          <QueryError error={deliveries.error} retry={() => void deliveries.refetch()} />
        ) : !deliveries.data ? (
          <TableSkeleton />
        ) : items.length === 0 ? (
          <EmptyState title={status ? `No ${(statusFilters.find((f) => f.value === status)?.label ?? status).toLowerCase()} deliveries` : 'No deliveries yet'}>
            Matching events appear here as they are relayed.
          </EmptyState>
        ) : (
          <DataTable label="Deliveries" rows={items} columns={cols} rowKey={(x) => x.id} />
        )}
      </Section>

      <ConfirmDialog
        open={!!confirm}
        title={confirm?.kind === 'one' ? 'Retry this delivery?' : `Retry ${plural(failedCount, 'failed delivery', 'failed deliveries')}?`}
        message="They are sent again in order. Receivers should deduplicate by event id."
        confirmLabel="Retry"
        danger={false}
        onCancel={() => setConfirm(null)}
        onConfirm={() => confirm && replay.mutate(confirm)}
      />
      <ResendDialog open={resending} busy={replay.isPending} onClose={() => setResending(false)} onConfirm={(since) => replay.mutate({ kind: 'since', since })} />
      {editing && d && <DestinationDialog destination={d} onClose={() => setEditing(false)} />}
    </Stack>
  )
}
