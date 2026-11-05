import { createFileRoute, Outlet, useRouterState } from '@tanstack/react-router'
import { PageHeader } from '@/components/PageHeader'
import { SubNav } from '@/components/SubNav'

export const Route = createFileRoute('/activity')({
  component: ActivityLayout,
})

function ActivityLayout() {
  const path = useRouterState({ select: (r) => r.location.pathname })
  const list = path === '/activity' || path === '/activity/' || path === '/activity/sessions'
  return (
    <>
      {list && (
        <>
          <PageHeader
            title="Activity"
            subtitle="Every request through the gateway: what was asked for, where it went, why, and what it cost. Metadata only — content is never stored here."
          />
          <SubNav
            label="Activity sections"
            items={[
              { to: '/activity', label: 'Requests', exact: true },
              { to: '/activity/sessions', label: 'Sessions' },
            ]}
          />
        </>
      )}
      <Outlet />
    </>
  )
}
