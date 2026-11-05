import { Fragment, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { ChevronDown, ChevronLeft, ChevronRight, ChevronUp, Radio } from 'lucide-react'
import { Badge, Button, EmptyState, Panel, TableScroll } from '@/components/ui'
import { SelectField } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { eventCatalogQuery, eventsQuery } from '@/lib/api/queries'
import type { EventEnvelope, EventItem } from '@/lib/api/types'
import { fmtDateTime, fmtRelative } from '@/lib/format'
import { severityTone } from '@/features/events/events'
import e from '@/features/events/events.module.css'

export const Route = createFileRoute('/events/')({
  component: EventLog,
})

function envelopeOf(ev: EventItem): EventEnvelope | null {
  const env = ev.envelope as EventEnvelope | null | undefined
  return env && typeof env === 'object' ? env : null
}

function subjectsText(env: EventEnvelope | null): string {
  return Object.entries(env?.subjects ?? {})
    .map(([k, v]) => `${k}=${v}`)
    .join(' ')
}

function EventLog() {
  const catalog = useQuery(eventCatalogQuery)
  const [type, setType] = useState('')
  const [severity, setSeverity] = useState('')
  const [cursors, setCursors] = useState<string[]>([])
  const [open, setOpen] = useState<string | null>(null)
  const cursor = cursors[cursors.length - 1]
  const log = useQuery(eventsQuery({ type: type || undefined, severity: severity || undefined, cursor }))
  const items = log.data?.items ?? []
  const reset = () => {
    setCursors([])
    setOpen(null)
  }

  return (
    <Panel title="Event log" subtitle={cursor ? 'Older events. Live updates resume on the newest page.' : 'Newest first; refreshes every 15 seconds.'}>
      <div className={e.toolbar}>
        <SelectField label="Event" value={type} onChange={(v) => { setType(v); reset() }}
          options={[{ value: '', label: 'All events' }, ...(catalog.data?.items ?? []).map((k) => ({ value: k.type, label: k.name }))]} />
        <SelectField label="Severity" value={severity} onChange={(v) => { setSeverity(v); reset() }}
          options={[{ value: '', label: 'Any severity' }, { value: 'info', label: 'Info' }, { value: 'warning', label: 'Warning' }, { value: 'critical', label: 'Critical' }]} />
      </div>
      {log.isError ? (
        <QueryError error={log.error} retry={() => void log.refetch()} />
      ) : !log.data ? (
        <TableSkeleton />
      ) : items.length === 0 ? (
        <EmptyState icon={Radio} title={type || severity ? 'No matching events' : 'No events yet'}>
          Events are recorded for state transitions such as an account switch, an exhausted quota window or an opened breaker.
        </EmptyState>
      ) : (
        <TableScroll label="Event log">
          <table className="data-table" style={{ width: '100%' }}>
            <thead>
              <tr>
                <th>Time</th>
                <th>Event</th>
                <th>Severity</th>
                <th>Subjects</th>
                <th aria-label="Envelope" />
              </tr>
            </thead>
            <tbody>
              {items.map((ev) => {
                const env = envelopeOf(ev)
                const expanded = open === ev.id
                return (
                  <Fragment key={ev.id}>
                    <tr>
                      <td style={{ whiteSpace: 'nowrap' }} title={fmtDateTime(ev.occurred_at, { seconds: true })}>{fmtRelative(ev.occurred_at)}</td>
                      <td><span className="mono">{env?.name ?? ev.type}</span></td>
                      <td><Badge tone={severityTone(ev.severity)}>{ev.severity}</Badge></td>
                      <td><span className="ident muted" style={{ fontSize: 'var(--text-xs)' }}>{subjectsText(env) || '—'}</span></td>
                      <td style={{ textAlign: 'right' }}>
                        <Button size="small" variant="ghost" icon={expanded ? ChevronUp : ChevronDown} aria-expanded={expanded}
                          aria-label={`${expanded ? 'Hide' : 'Show'} envelope for ${env?.name ?? ev.type}`} onClick={() => setOpen(expanded ? null : ev.id)} />
                      </td>
                    </tr>
                    {expanded && (
                      <tr>
                        <td colSpan={5}>
                          <pre className={e.envelope} aria-label="Event envelope">{JSON.stringify(ev.envelope, null, 2)}</pre>
                        </td>
                      </tr>
                    )}
                  </Fragment>
                )
              })}
            </tbody>
          </table>
        </TableScroll>
      )}
      {(cursors.length > 0 || log.data?.next_cursor) && (
        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 12 }}>
          <Button icon={ChevronLeft} disabled={cursors.length === 0} onClick={() => { setCursors(cursors.slice(0, -1)); setOpen(null) }}>Newer</Button>
          <Button icon={ChevronRight} disabled={!log.data?.next_cursor} onClick={() => { setCursors([...cursors, log.data!.next_cursor!]); setOpen(null) }}>Older</Button>
        </div>
      )}
    </Panel>
  )
}
