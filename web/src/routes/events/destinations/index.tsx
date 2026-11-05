import { createFileRoute, Link } from '@tanstack/react-router'
import { Badge } from '@/components/ui'
import { ResourceList } from '@/features/config/ResourceList'
import { DestinationDialog } from '@/features/events/DestinationDialog'
import type { DestinationRes } from '@/lib/api/types'

export const Route = createFileRoute('/events/destinations/')({
  component: Destinations,
})

function filterSummary(r: DestinationRes): string {
  const evs = r.spec.filter?.events ?? []
  const sev = r.spec.filter?.min_severity && r.spec.filter.min_severity !== 'info' ? `${r.spec.filter.min_severity}+` : ''
  return [evs.length ? evs.join(', ') : 'All events', sev].filter(Boolean).join(' · ')
}

function Destinations() {
  return (
    <ResourceList<DestinationRes>
      plural="event-destinations"
      title="Event destinations"
      subtitle="Relay events to a Pulse server or a signed webhook. Deliveries retry with backoff and dead-letter for replay."
      noun="destination"
      empty="Add a destination to relay events to Pulse or to any HTTPS receiver."
      nameCell={(r) => (
        <Link to="/events/destinations/$destId" params={{ destId: r.id }} className="mono">{r.name}</Link>
      )}
      columns={[
        { header: 'Type', cell: (r) => (r.spec.type === 'pulse' ? 'Pulse' : 'Webhook') },
        { header: 'Target', cell: (r) => <span className="ident" style={{ fontSize: 'var(--text-xs)' }}>{r.spec.type === 'pulse' ? r.spec.pulse_url : r.spec.webhook_url}</span> },
        { header: 'Events', cell: (r) => <span className="ident" style={{ fontSize: 'var(--text-xs)' }}>{filterSummary(r)}</span> },
        { header: 'State', cell: (r) => (r.spec.enabled ? <Badge tone="success">Enabled</Badge> : <Badge>Disabled</Badge>) },
      ]}
      dialog={(r, close) => <DestinationDialog destination={r} onClose={close} />}
    />
  )
}
