import { createFileRoute, Outlet } from '@tanstack/react-router'
import { PageHeader } from '@/components/PageHeader'
import { SubNav } from '@/components/SubNav'

export const Route = createFileRoute('/events')({
  component: EventsLayout,
})

function EventsLayout() {
  return (
    <>
      <PageHeader
        title="Events"
        subtitle="Domain events Harbor Pilot records for state transitions (account switches, quota windows, budgets, breakers), and the destinations that relay them."
      />
      <SubNav
        label="Events sections"
        items={[
          { to: '/events', label: 'Event log', exact: true },
          { to: '/events/catalog', label: 'Catalog' },
          { to: '/events/destinations', label: 'Destinations' },
        ]}
      />
      <Outlet />
    </>
  )
}
