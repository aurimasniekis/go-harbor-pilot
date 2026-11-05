import { createFileRoute } from '@tanstack/react-router'
import { Badge } from '@/components/ui'
import { ResourceList } from '@/features/config/ResourceList'
import { ChannelDialog } from '@/features/events/ChannelDialog'
import { SendTest } from '@/features/events/SendTest'
import { channelTypes } from '@/features/events/events'
import type { ChannelRes } from '@/lib/api/types'
import { useCan } from '@/lib/session'

export const Route = createFileRoute('/notifications/')({
  component: Channels,
})

function target(r: ChannelRes) {
  if (r.spec.type === 'telegram') return <>Chat <span className="mono">{r.spec.telegram_chat_id}</span></>
  if (r.spec.type === 'webhook') return <span className="ident" style={{ fontSize: 'var(--text-xs)' }}>{r.spec.webhook_url}</span>
  if (r.spec.secret_ref) return <span className="mono">{r.spec.secret_ref}</span>
  return r.secrets?.secret?.source === 'stored' ? 'Stored webhook URL' : <Badge tone="warning">No webhook URL</Badge>
}

function delivery(r: ChannelRes): string {
  const parts: string[] = []
  const qh = r.spec.quiet_hours
  if (qh?.enabled) parts.push(`Quiet ${qh.start}–${qh.end}${r.spec.timezone ? ` ${r.spec.timezone}` : ' UTC'}${qh.critical_bypass ? ', critical bypasses' : ''}`)
  if (r.spec.digest_minutes) parts.push(`digest every ${r.spec.digest_minutes} min`)
  parts.push(`≤ ${r.spec.rate_per_minute ?? 20}/min`)
  return parts.join('; ')
}

function Channels() {
  const canTest = useCan('notifications.manage')
  return (
    <ResourceList<ChannelRes>
      plural="notification-channels"
      title="Channels"
      subtitle="Telegram, Slack, Discord or a signed webhook. Credentials are write-only and never shown again."
      noun="channel"
      empty="Add a channel, send a test message, then create a rule that routes events to it."
      columns={[
        { header: 'Type', cell: (r) => channelTypes.find((t) => t.value === r.spec.type)?.label ?? r.spec.type },
        { header: 'Target', cell: target },
        { header: 'Delivery', cell: (r) => <span className="muted" style={{ fontSize: 'var(--text-xs)' }}>{delivery(r)}</span> },
        { header: 'State', cell: (r) => (r.spec.enabled ? <Badge tone="success">Enabled</Badge> : <Badge>Disabled</Badge>) },
        ...(canTest ? [{ header: 'Test', cell: (r: ChannelRes) => <SendTest id={r.id} name={r.name} /> }] : []),
      ]}
      dialog={(r, close) => <ChannelDialog channel={r} onClose={close} />}
    />
  )
}
