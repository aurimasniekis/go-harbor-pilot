import { createFileRoute, Outlet } from '@tanstack/react-router'
import { LinkTabs, PageHeader } from '@/ui/kit'

export const Route = createFileRoute('/quotas')({
  component: LimitsLayout,
})

function LimitsLayout() {
  return (
    <>
      <PageHeader title="Limits" info="Provider quota windows, rate limits and spend budgets, and how they affect routing. Unknown values stay unknown, never zero." />
      <LinkTabs
        label="Limits sections"
        items={[
          { to: '/quotas', label: 'Overview', exact: true },
          { to: '/quotas/monitors', label: 'Quota tracking' },
        ]}
      />
      <Outlet />
    </>
  )
}
