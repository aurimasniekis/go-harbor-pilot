import { createFileRoute, Outlet } from '@tanstack/react-router'
import { Stack } from '@mantine/core'
import { PrivacyHeader } from '@/features/privacy/PrivacyHeader'

export const Route = createFileRoute('/privacy')({
  component: PrivacyLayout,
})

function PrivacyLayout() {
  return (
    <Stack gap="lg">
      <PrivacyHeader />
      <Outlet />
    </Stack>
  )
}
