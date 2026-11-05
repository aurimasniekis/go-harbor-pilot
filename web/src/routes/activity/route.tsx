import { createFileRoute, Outlet, useRouterState } from '@tanstack/react-router'
import { LinkTabs, PageHeader } from '@/ui/kit'

export const Route = createFileRoute('/activity')({
  component: ActivityLayout,
})

function ActivityLayout() {
  // A single request is its own page with breadcrumbs, not a tab.
  const detail = useRouterState({ select: (r) => /^\/activity\/req_/.test(r.location.pathname) })
  if (detail) return <Outlet />
  return (
    <>
      <PageHeader title="Requests" />
      <LinkTabs
        label="Request views"
        items={[
          { to: '/activity', label: 'Requests', exact: true },
          { to: '/activity/sessions', label: 'Sessions', exact: true },
          { to: '/activity/stats', label: 'Statistics', exact: true },
        ]}
      />
      <Outlet />
    </>
  )
}
