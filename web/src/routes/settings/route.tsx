import { createFileRoute, Outlet } from '@tanstack/react-router'
import { Stack } from '@mantine/core'
import { SettingsHeader } from '@/features/settings/SettingsHeader'

export const Route = createFileRoute('/settings')({
  component: () => (
    <Stack gap="lg">
      <SettingsHeader />
      <Outlet />
    </Stack>
  ),
})
