import { createFileRoute, Outlet } from '@tanstack/react-router'
import { PageHeader } from '@/components/PageHeader'
import { SubNav } from '@/components/SubNav'

export const Route = createFileRoute('/notifications')({
  component: NotificationsLayout,
})

function NotificationsLayout() {
  return (
    <>
      <PageHeader
        title="Notifications"
        subtitle="Tell people when something needs attention: channels say where messages go, rules say which events send them."
      />
      <SubNav
        label="Notification sections"
        items={[
          { to: '/notifications', label: 'Channels', exact: true },
          { to: '/notifications/rules', label: 'Rules' },
          { to: '/notifications/history', label: 'History' },
        ]}
      />
      <Outlet />
    </>
  )
}
