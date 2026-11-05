import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { EnabledSwitch, ResourceList } from '@/features/config/ResourceList'
import { Text } from '@mantine/core'
import { RuleDialog } from '@/features/events/RuleDialog'
import { eventsPhrase, noisePhrase } from '@/features/events/events'
import { resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { ChannelRes, NotificationRuleRes } from '@/lib/api/types'

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
      empty="Add a rule, for example quota.* and budget.* at warning or above, sent to your team channel."
      columns={[
        {
          header: 'Sends',
          mobile: 'line',
          cell: (r) => (
            <Text size="sm">
              {eventsPhrase(r.spec.filter.events, r.spec.filter.min_severity)} → {r.spec.channel_ids.map((id) => ch[id]?.name ?? id).join(', ') || 'no channel'}
            </Text>
          ),
        },
        { header: 'Noise control', mobile: 'meta', cell: (r) => <Text size="xs" c="dimmed">{noisePhrase(r.spec.cooldown_seconds, r.spec.group_seconds, r.spec.recovery) || 'Every time'}</Text> },
        { header: 'Status', mobile: 'meta', cell: (r) => <EnabledSwitch plural="notification-rules" r={r} /> },
      ]}
      dialog={(r, close) => <RuleDialog rule={r} onClose={close} />}
    />
  )
}
