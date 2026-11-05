import { createFileRoute, Outlet, useRouterState } from '@tanstack/react-router'
import { Stack } from '@mantine/core'
import { AlertsHeader } from '@/features/alerts/AlertsHeader'
import { SettingsHeader } from '@/features/settings/SettingsHeader'

export const Route = createFileRoute('/events')({
  component: EventsLayout,
})

/** Event relays belong to Alerts; the event stream and catalog to Settings. */
function EventsLayout() {
  const path = useRouterState({ select: (r) => r.location.pathname })
  if (/^\/events\/destinations\/[^/]+/.test(path)) return <Outlet />
  return (
    <Stack gap="lg">
      {path.startsWith('/events/destinations') ? <AlertsHeader /> : <SettingsHeader />}
      <Outlet />
    </Stack>
  )
}
