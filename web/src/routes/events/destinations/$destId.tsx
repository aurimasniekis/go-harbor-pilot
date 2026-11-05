import { useState } from 'react'
import { createFileRoute, Link } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, CheckCircle2, History, Pencil, RotateCcw } from 'lucide-react'
import { Badge, Banner, Button, EmptyState, KeyValues, Panel, Skeleton, TableScroll, Tabs, type Tone } from '@/components/ui'
import { MutationError, TextField } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { ResourceBadges } from '@/components/ResourceBadges'
import { apiSend } from '@/lib/api/client'
import { deliveriesQuery, resourceQuery } from '@/lib/api/queries'
import type { Delivery, DestinationRes } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { fmtDateTime, fmtNumber, fmtRelative } from '@/lib/format'
import { DestinationDialog } from '@/features/events/DestinationDialog'
import { fromLocalInput, toLocalInput } from '@/features/privacy/privacy'
import e from '@/features/events/events.module.css'

export const Route = createFileRoute('/events/destinations/$destId')({
  component: DestinationDetail,
})

const statusTone: Record<Delivery['status'], Tone> = { delivered: 'success', pending: 'info', deferred: 'warning', dead: 'error' }
type StatusFilter = '' | Delivery['status']

type Replay = { kind: 'dead' } | { kind: 'one'; id: string } | { kind: 'since' }

function DestinationDetail() {
  const { destId } = Route.useParams()
  const qc = useQueryClient()
  const dest = useQuery(resourceQuery<DestinationRes>('event-destinations', destId))
  const [status, setStatus] = useState<StatusFilter>('')
  const deliveries = useQuery(deliveriesQuery(destId, status || undefined))
  const canWrite = useCan('config.write')
  const canReplay = useCan('events.manage')
  const [editing, setEditing] = useState(false)
  const [confirm, setConfirm] = useState<Replay | null>(null)
  const [since, setSince] = useState(() => toLocalInput(new Date(Date.now() - 3600_000).toISOString()))
  const [queued, setQueued] = useState<number | null>(null)
  const replay = useMutation({
    mutationFn: (r: Replay) =>
      apiSend<{ queued: number }>('POST', `/event-destinations/${encodeURIComponent(destId)}/replay`,
        r.kind === 'one' ? { delivery_id: r.id } : r.kind === 'since' ? { since: fromLocalInput(since) } : {}),
    onSuccess: (res) => {
      setQueued(res.queued)
      setConfirm(null)
      void qc.invalidateQueries({ queryKey: ['events', 'deliveries', destId] })
    },
  })
  const ask = (r: Replay) => {
    setQueued(null)
    replay.reset()
    setConfirm(r)
  }

  if (dest.isError) return <QueryError error={dest.error} retry={() => void dest.refetch()} />
  const d = dest.data
  const items = deliveries.data?.items ?? []
  const sinceValid = !!fromLocalInput(since) && new Date(since).getTime() < Date.now()
  const sec = d?.secrets ?? {}

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div>
        <Link to="/events/destinations" style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
          <ArrowLeft size={14} aria-hidden="true" /> All destinations
        </Link>
      </div>
      <Panel title={d ? <span className="mono">{d.name}</span> : 'Destination'} subtitle={d ? (d.spec.type === 'pulse' ? 'Pulse relay' : 'Signed webhook') : undefined}
        actions={d && canWrite && <Button icon={Pencil} onClick={() => setEditing(true)}>Edit</Button>}>
        {!d ? (
          <Skeleton height={100} />
        ) : (
          <KeyValues
            items={[
              ['State', d.spec.enabled ? <Badge tone="success">Enabled</Badge> : <Badge>Disabled</Badge>],
              ['Target', <span className="ident">{d.spec.type === 'pulse' ? d.spec.pulse_url : d.spec.webhook_url}</span>],
              ['Events', <span className="ident">{(d.spec.filter?.events ?? []).join(', ') || 'All events'}{d.spec.filter?.min_severity && d.spec.filter.min_severity !== 'info' ? ` (${d.spec.filter.min_severity} and above)` : ''}</span>],
              ...(Object.keys(d.spec.filter?.subjects ?? {}).length
                ? ([['Subjects', <span className="ident">{Object.entries(d.spec.filter?.subjects ?? {}).map(([k, v]) => `${k}=${v}`).join(' ')}</span>]] as [React.ReactNode, React.ReactNode][])
                : []),
              ['Retry policy', `Timeout ${d.spec.timeout_seconds ?? 10} s; dead-letter after ${d.spec.max_attempts ?? 50} attempts or ${d.spec.max_age_hours ?? 24} hours`],
              d.spec.type === 'webhook'
                ? ['Signing', sec.signing_secret?.source === 'stored'
                    ? <>Signed{sec.previous_signing_secret?.source === 'stored' && <> <Badge tone="warning">Rotating: two signatures</Badge></>}</>
                    : <Badge tone="warning">Unsigned</Badge>]
                : ['Credentials', sec.creds?.source === 'stored' ? 'Stored credentials file' : d.spec.creds_ref ? <span className="mono">{d.spec.creds_ref}</span> : 'None'],
              ['Managed by', <ResourceBadges r={d} />],
            ]}
          />
        )}
      </Panel>

      <Panel title="Deliveries" subtitle="Delivery is at-least-once; receivers deduplicate by event id. Refreshes every 15 seconds."
        actions={canReplay && <Button icon={RotateCcw} onClick={() => ask({ kind: 'dead' })}>Replay dead</Button>}>
        {canReplay && (
          <div className={e.toolbar}>
            <TextField label="Re-send matching events since" type="datetime-local" value={since} onChange={setSince}
              error={sinceValid ? undefined : 'Choose a time in the past.'} />
            <Button icon={History} disabled={!sinceValid} onClick={() => ask({ kind: 'since' })}>Re-send</Button>
          </div>
        )}
        {confirm && (
          <div className={e.confirm} role="group" aria-label="Confirm replay" style={{ marginBottom: 12 }}>
            <span>
              {confirm.kind === 'dead' && 'Queue every dead delivery for this destination again?'}
              {confirm.kind === 'one' && <>Queue dead delivery <span className="mono">{confirm.id}</span> again?</>}
              {confirm.kind === 'since' && <>Re-send every matching event since {fmtDateTime(fromLocalInput(since))}? Receivers may see duplicates.</>}
            </span>
            <Button variant="primary" busy={replay.isPending} onClick={() => replay.mutate(confirm)}>Confirm replay</Button>
            <Button variant="ghost" onClick={() => setConfirm(null)}>Cancel</Button>
          </div>
        )}
        <MutationError error={replay.error} />
        {queued !== null && (
          <Banner tone="info" icon={CheckCircle2} role="status">
            {queued > 0 ? `Queued ${fmtNumber(queued)} deliver${queued === 1 ? 'y' : 'ies'}.` : 'Nothing to replay.'}
          </Banner>
        )}
        <div style={{ marginBottom: 12 }}>
          <Tabs<StatusFilter> label="Delivery status" variant="segmented" value={status} onChange={setStatus}
            items={[{ value: '', label: 'All' }, { value: 'pending', label: 'Pending' }, { value: 'deferred', label: 'Retrying' }, { value: 'delivered', label: 'Delivered' }, { value: 'dead', label: 'Dead' }]} />
        </div>
        {deliveries.isError ? (
          <QueryError error={deliveries.error} retry={() => void deliveries.refetch()} />
        ) : !deliveries.data ? (
          <TableSkeleton />
        ) : items.length === 0 ? (
          <EmptyState title={status ? `No ${status} deliveries` : 'No deliveries yet'}>Matching events appear here as they are relayed.</EmptyState>
        ) : (
          <TableScroll label="Deliveries">
            <table className="data-table" style={{ width: '100%' }}>
              <thead>
                <tr>
                  <th>Event</th>
                  <th>Occurred</th>
                  <th>Status</th>
                  <th>Attempts</th>
                  <th>Last result</th>
                  <th>Next / delivered</th>
                  <th aria-label="Actions" />
                </tr>
              </thead>
              <tbody>
                {items.map((x) => (
                  <tr key={x.id}>
                    <td><span className="mono" title={x.event_id}>{x.event_type}</span></td>
                    <td style={{ whiteSpace: 'nowrap' }} title={fmtDateTime(x.occurred_at, { seconds: true })}>{fmtRelative(x.occurred_at)}</td>
                    <td><Badge tone={statusTone[x.status]}>{x.status}</Badge></td>
                    <td className="num">{x.attempts}</td>
                    <td style={{ maxWidth: 360 }}>
                      {x.last_status ? <span className="mono">HTTP {x.last_status} </span> : null}
                      {x.last_error ? <span className="ident muted" style={{ fontSize: 'var(--text-xs)' }}>{x.last_error}</span> : !x.last_status && '—'}
                    </td>
                    <td style={{ whiteSpace: 'nowrap' }}>
                      {x.status === 'delivered' ? fmtDateTime(x.delivered_at) : x.status === 'dead' ? '—' : fmtRelative(x.next_attempt_at)}
                    </td>
                    <td style={{ textAlign: 'right' }}>
                      {canReplay && x.status === 'dead' && (
                        <Button size="small" icon={RotateCcw} aria-label={`Replay delivery ${x.id}`} onClick={() => ask({ kind: 'one', id: x.id })}>Replay</Button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </TableScroll>
        )}
      </Panel>
      {editing && d && <DestinationDialog destination={d} onClose={() => setEditing(false)} />}
    </div>
  )
}
