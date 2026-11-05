import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Badge } from '@/components/ui'
import { ResourceList } from '@/features/config/ResourceList'
import { RuleDialog } from '@/features/events/RuleDialog'
import { fmtSeconds } from '@/features/events/events'
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
      subtitle="Each rule matches events and sends them to channels, with grouping and cooldown to keep noise down."
      noun="rule"
      empty="Add a rule, for example quota.* and budget.* at warning or above, sent to your team channel."
      columns={[
        { header: 'Events', cell: (r) => (
          <span className="ident" style={{ fontSize: 'var(--text-xs)' }}>
            {(r.spec.filter.events ?? []).join(', ') || 'All events'}
            {r.spec.filter.min_severity && r.spec.filter.min_severity !== 'info' ? ` · ${r.spec.filter.min_severity}+` : ''}
          </span>
        ) },
        { header: 'Channels', cell: (r) => (
          <span style={{ display: 'inline-flex', flexWrap: 'wrap', gap: 4 }}>
            {r.spec.channel_ids.map((id) => <Badge key={id}>{ch[id]?.name ?? id}</Badge>)}
          </span>
        ) },
        { header: 'Noise control', cell: (r) => (
          <span className="muted" style={{ fontSize: 'var(--text-xs)' }}>
            Cooldown {fmtSeconds(r.spec.cooldown_seconds)}{r.spec.group_seconds ? `; group ${fmtSeconds(r.spec.group_seconds)}` : ''}{r.spec.recovery ? '; recovery' : ''}
          </span>
        ) },
        { header: 'State', cell: (r) => (r.spec.enabled ? <Badge tone="success">Enabled</Badge> : <Badge>Disabled</Badge>) },
      ]}
      dialog={(r, close) => <RuleDialog rule={r} onClose={close} />}
    />
  )
}
