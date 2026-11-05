import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { EnabledSwitch, ResourceList } from '@/features/config/ResourceList'
import { Group, Text } from '@mantine/core'
import { Badge } from '@/components/ui'
import { RuleDialog } from '@/features/events/RuleDialog'
import { eventLabel, eventsPhrase, noisePhrase } from '@/features/events/events'
import { resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { ChannelRes, NotificationRuleRes } from '@/lib/api/types'

/** "Quota used up, Budget used up" (first three, then a count). */
function eventsLabel(events: string[]): string {
  const names = events.map(eventLabel)
  return names.length <= 3 ? names.join(', ') : `${names.slice(0, 3).join(', ')} and ${names.length - 3} more`
}

export const Route = createFileRoute('/notifications/rules')({
  component: Rules,
})

function Rules() {
  const channels = useQuery(resourceListQuery<ChannelRes>('notification-channels'))
  const ch = byId(channels.data?.items)
  return (
    <ResourceList<NotificationRuleRes>
      plural="notification-rules"
      title="Rules"
      subtitle="Each rule decides which events send a message to which channels, with limits to keep noise down."
      noun="rule"
      empty="Add a rule, for example “Quota used up” and “Budget warnings”, sent to your team channel."
      columns={[
        {
          header: 'Sends',
          mobile: 'line',
          cell: (r) => {
            const off = r.spec.channel_ids.filter((id) => ch[id] && !ch[id].spec.enabled)
            const missing = r.spec.channel_ids.filter((id) => channels.data && !ch[id])
            return (
              <Group gap={6} wrap="wrap">
                <Text size="sm">
                  {r.spec.filter.events?.length ? eventsLabel(r.spec.filter.events) : eventsPhrase(r.spec.filter.events, r.spec.filter.min_severity)} →{' '}
                  {r.spec.channel_ids.map((id) => ch[id]?.name ?? 'deleted channel').join(', ') || 'no channel'}
                </Text>
                {off.length > 0 && <Badge tone="warning" title="Nothing is sent through a channel that is off">{off.length === r.spec.channel_ids.length ? 'Channel off' : 'A channel is off'}</Badge>}
                {missing.length > 0 && <Badge tone="error">Channel deleted</Badge>}
              </Group>
            )
          },
        },
        { header: 'Noise control', mobile: 'meta', cell: (r) => <Text size="xs" c="dimmed">{noisePhrase(r.spec.cooldown_seconds, r.spec.group_seconds, r.spec.recovery) || 'Every time'}</Text> },
        { header: 'Status', mobile: 'meta', cell: (r) => <EnabledSwitch plural="notification-rules" r={r} /> },
      ]}
      dialog={(r, close) => <RuleDialog rule={r} onClose={close} />}
    />
  )
}
