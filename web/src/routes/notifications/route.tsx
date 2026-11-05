import { createFileRoute, Outlet } from '@tanstack/react-router'
import { Stack } from '@mantine/core'
import { AlertsHeader } from '@/features/alerts/AlertsHeader'

export const Route = createFileRoute('/notifications')({
  component: () => (
    <Stack gap="lg">
      <AlertsHeader />
      <Outlet />
    </Stack>
  ),
})
