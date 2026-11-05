import { LinkTabs, PageHeader } from '@/ui/kit'

/** Header and tabs shared by every Alerts page (channels, rules, history, relays). */
export function AlertsHeader() {
  return (
    <>
      <PageHeader title="Alerts" info="Who gets told when something needs attention. Channels are where messages go; rules decide which events send them. Event relays forward every matching event to a webhook or Pulse." />
      <LinkTabs
        label="Alerts sections"
        items={[
          { to: '/notifications', label: 'Channels', exact: true },
          { to: '/notifications/rules', label: 'Rules' },
          { to: '/notifications/history', label: 'History' },
          { to: '/events/destinations', label: 'Event relays' },
        ]}
      />
    </>
  )
}
