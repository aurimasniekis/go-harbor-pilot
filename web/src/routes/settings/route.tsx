import { createFileRoute, Outlet } from '@tanstack/react-router'
import { PageHeader } from '@/components/PageHeader'
import { SubNav } from '@/components/SubNav'
import { useCan } from '@/lib/session'

export const Route = createFileRoute('/settings')({
  component: SettingsLayout,
})

function SettingsLayout() {
  const users = useCan('users.manage')
  const authSettings = useCan('auth.settings')
  const audit = useCan('audit.read')
  const items = [
    ...(users ? [{ to: '/settings/users', label: 'Users' }, { to: '/settings/service-principals', label: 'Service principals' }] : []),
    ...(authSettings ? [{ to: '/settings/auth', label: 'Authentication' }] : []),
    ...(audit ? [{ to: '/settings/audit', label: 'Audit log' }] : []),
  ]
  return (
    <>
      <PageHeader title="Settings" subtitle="People, automation identities, sign-in and the audit trail." />
      <SubNav label="Settings sections" items={items} />
      <Outlet />
    </>
  )
}
