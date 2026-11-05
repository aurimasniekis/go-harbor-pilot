import { createFileRoute } from '@tanstack/react-router'
import { Badge } from '@/components/ui'
import { EnabledSwitch, ResourceList } from '@/features/config/ResourceList'
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
  return (
    <ResourceList<ChannelRes>
      plural="notification-channels"
      title="Channels"
      subtitle="Telegram, Slack, Discord or a signed webhook. Credentials are write-only and never shown again. Send a test after adding one."
      noun="channel"
      empty="Add a channel, send a test message, then create a rule that routes events to it."
      columns={[
        { header: 'Sends to', mobile: 'meta', cell: (r) => <span>{channelTypes.find((t) => t.value === r.spec.type)?.label ?? r.spec.type} · <span className="muted">{target(r)}</span></span> },
        { header: 'Delivery', mobile: 'meta', cell: (r) => <span className="muted" style={{ fontSize: 'var(--text-xs)' }}>{delivery(r)}</span> },
        { header: 'Status', mobile: 'line', cell: (r) => <EnabledSwitch plural="notification-channels" r={r} /> },
        ...(canTest ? [{ header: 'Test', mobile: 'meta' as const, cell: (r: ChannelRes) => <span onClick={(e) => e.stopPropagation()}><SendTest id={r.id} name={r.name} /></span> }] : []),
      ]}
      dialog={(r, close) => <ChannelDialog channel={r} onClose={close} />}
    />
  )
}
