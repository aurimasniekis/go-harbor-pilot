import { createFileRoute, Outlet, useRouterState } from '@tanstack/react-router'
import { Stack } from '@mantine/core'
import { PrivacyHeader } from '@/features/privacy/PrivacyHeader'

export const Route = createFileRoute('/traces')({
  component: TracesLayout,
})

/** Captured content is a Privacy tab; one capture is its own page. */
function TracesLayout() {
  const detail = useRouterState({ select: (r) => /^\/traces\/[^/]+/.test(r.location.pathname) })
  if (detail) return <Outlet />
  return (
    <Stack gap="lg">
      <PrivacyHeader />
      <Outlet />
    </Stack>
  )
}
