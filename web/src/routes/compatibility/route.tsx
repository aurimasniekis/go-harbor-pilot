import { createFileRoute, Outlet, useRouterState } from '@tanstack/react-router'
import { Stack } from '@mantine/core'
import { SettingsHeader } from '@/features/settings/SettingsHeader'
import { LinkTabs } from '@/ui/kit'

export const Route = createFileRoute('/compatibility')({
  component: DiagnosticsLayout,
})

/** Diagnostics under Settings: protocol issues and protocol changes. */
function DiagnosticsLayout() {
  const detail = useRouterState({ select: (r) => /^\/compatibility\/work-items\/[^/]+/.test(r.location.pathname) })
  if (detail) return <Outlet />
  return (
    <Stack gap="lg">
      <SettingsHeader />
      <LinkTabs
        label="Diagnostics sections"
        items={[
          { to: '/compatibility', label: 'Protocol issues', exact: true },
          { to: '/compatibility/watch', label: 'Protocol changes' },
          { to: '/compatibility/work-items', label: 'Follow-ups' },
        ]}
      />
      <Outlet />
    </Stack>
  )
}
