import { createFileRoute } from '@tanstack/react-router'
import { EnabledSwitch, NameCell, ResourceList } from '@/features/config/ResourceList'
import { Text } from '@mantine/core'
import { useNavigate } from '@tanstack/react-router'
import { eventsPhrase } from '@/features/events/events'
import { DestinationDialog } from '@/features/events/DestinationDialog'
import type { DestinationRes } from '@/lib/api/types'

export const Route = createFileRoute('/events/destinations/')({
  component: Destinations,
})

function Destinations() {
  const navigate = useNavigate()
  return (
    <ResourceList<DestinationRes>
      plural="event-destinations"
      title="Event relays"
      subtitle="Forward every matching event to a Pulse server or a signed webhook. Failed deliveries are retried automatically, then wait on the relay's page for you to retry them."
      noun="event relay"
      empty="Add an event relay to forward events to Pulse or to any HTTPS receiver."
      nameCell={(r) => <NameCell name={r.name} />}
      onRow={(r) => void navigate({ to: '/events/destinations/$destId', params: { destId: r.id } })}
      columns={[
        {
          header: 'Sends to',
          mobile: 'meta',
          cell: (r) => {
            const url = (r.spec.type === 'pulse' ? r.spec.pulse_url : r.spec.webhook_url) ?? ''
            return (
              <Text size="sm" truncate title={url} maw="min(360px, 100%)">
                {r.spec.type === 'pulse' ? 'Pulse' : 'Signed webhook'} · <Text span size="xs" c="dimmed" ff="monospace">{url}</Text>
              </Text>
            )
          },
        },
        { header: 'Events', mobile: 'line', cell: (r) => <Text size="sm">{eventsPhrase(r.spec.filter?.events, r.spec.filter?.min_severity)}</Text> },
        { header: 'Status', mobile: 'meta', cell: (r) => <EnabledSwitch plural="event-destinations" r={r} offLabel="Off — not relaying" /> },
      ]}
      dialog={(r, close) => <DestinationDialog destination={r} onClose={close} />}
    />
  )
}
