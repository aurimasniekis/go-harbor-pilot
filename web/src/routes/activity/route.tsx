import { createFileRoute, Outlet, useNavigate, useRouterState } from '@tanstack/react-router'
import { SegmentedControl } from '@mantine/core'
import { PageHeader } from '@/ui/kit'

export const Route = createFileRoute('/activity')({
  component: ActivityLayout,
})

function ActivityLayout() {
  const path = useRouterState({ select: (r) => r.location.pathname.replace(/\/$/, '') })
  const navigate = useNavigate()
  // A single request is its own page with breadcrumbs, not a tab.
  if (/^\/activity\/req_/.test(path)) return <Outlet />
  return (
    <>
      <PageHeader
        title="Requests"
        actions={
          <SegmentedControl
            size="sm"
            aria-label="View"
            value={path === '/activity/sessions' ? 'sessions' : 'requests'}
            onChange={(v) => void navigate({ to: v === 'sessions' ? '/activity/sessions' : '/activity' })}
            data={[
              { value: 'requests', label: 'All requests' },
              { value: 'sessions', label: 'By session' },
            ]}
          />
        }
      />
      <Outlet />
    </>
  )
}
