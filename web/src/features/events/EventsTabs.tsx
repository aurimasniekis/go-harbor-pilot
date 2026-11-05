import { LinkTabs } from '@/ui/kit'

/** Switch between the event stream and the event catalog. */
export function EventsTabs() {
  return (
    <LinkTabs
      label="Event pages"
      items={[
        { to: '/events', label: 'Stream', exact: true },
        { to: '/events/catalog', label: 'Catalog' },
      ]}
    />
  )
}
