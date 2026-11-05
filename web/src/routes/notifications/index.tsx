import { createFileRoute } from '@tanstack/react-router'
import { Badge } from '@/components/ui'
import { Text } from '@mantine/core'
import { EnabledSwitch, ResourceList } from '@/features/config/ResourceList'
import { ChannelDialog } from '@/features/events/ChannelDialog'
import { SendTest } from '@/features/events/SendTest'
import { channelTypes } from '@/features/events/events'
import { useQuery } from '@tanstack/react-query'
import { resourceListQuery } from '@/lib/api/queries'
import type { ChannelRes, NotificationRuleRes } from '@/lib/api/types'
import { useCan } from '@/lib/session'

export const Route = createFileRoute('/notifications/')({
  component: Channels,
})

function target(r: ChannelRes) {
  if (r.spec.type === 'telegram') return <>chat <span className="mono">{r.spec.telegram_chat_id}</span></>
  if (r.spec.type === 'webhook') return <span className="mono" title={r.spec.webhook_url}>{r.spec.webhook_url}</span>
  if (r.spec.secret_ref) return <>server secret <span className="mono">{r.spec.secret_ref}</span></>
  return r.secrets?.secret?.source === 'stored' ? 'saved webhook URL' : <Badge tone="warning">No webhook URL</Badge>
}

function delivery(r: ChannelRes): string {
  const parts: string[] = []
  const qh = r.spec.quiet_hours
  if (qh?.enabled) parts.push(`Quiet ${qh.start}–${qh.end}${r.spec.timezone ? ` ${r.spec.timezone}` : ' UTC'}${qh.critical_bypass ? ' except critical' : ''}`)
  if (r.spec.digest_minutes) parts.push(`digest every ${r.spec.digest_minutes} min`)
  return parts.join('; ') || 'Immediately'
}

function Channels() {
  const canTest = useCan('notifications.manage')
  const rules = useQuery(resourceListQuery<NotificationRuleRes>('notification-rules'))
  return (
    <ResourceList<ChannelRes>
      plural="notification-channels"
      title="Channels"
      subtitle="Where alerts go: Slack, Discord, Telegram or a signed webhook. Credentials are write-only and never shown again. Send a test after adding one."
      noun="channel"
      empty="Add a channel, send a test message, then add a rule that sends alerts to it."
      columns={[
        {
          header: 'Sends to',
          mobile: 'meta',
          cell: (r) => (
            <Text size="sm" truncate maw="min(340px, 100%)">
              {channelTypes.find((t) => t.value === r.spec.type)?.label ?? r.spec.type} <Text span size="xs" c="dimmed">· {target(r)}</Text>
            </Text>
          ),
        },
        { header: 'Delivery', mobile: 'meta', cell: (r) => <Text size="xs" c="dimmed">{delivery(r)}</Text> },
        { header: 'Status', mobile: 'line', cell: (r) => <div style={{ whiteSpace: 'nowrap' }}><EnabledSwitch plural="notification-channels" r={r} /></div> },
        ...(canTest ? [{ header: 'Test', mobile: 'meta' as const, cell: (r: ChannelRes) => <span data-row-action onClick={(e) => e.stopPropagation()}><SendTest id={r.id} name={r.name} /></span> }] : []),
      ]}
      usedBy={(r) => (rules.data?.items ?? []).filter((x) => x.spec.channel_ids.includes(r.id)).map((x) => `rule ${x.name}`)}
      dialog={(r, close) => <ChannelDialog channel={r} onClose={close} />}
    />
  )
}
