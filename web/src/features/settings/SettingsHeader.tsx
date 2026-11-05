import { useNavigate, useRouterState } from '@tanstack/react-router'
import { Select } from '@mantine/core'
import { useCan } from '@/lib/session'
import { LinkTabs, PageHeader, useCompact } from '@/ui/kit'

/** Settings sections the current user can open. */
function useSections(): { to: string; label: string; exact?: boolean }[] {
  const users = useCan('users.manage')
  const authSettings = useCan('auth.settings')
  const audit = useCan('audit.read')
  const system = useCan('system.manage')
  const config = useCan('config.read')
  return [
    ...(users ? [{ to: '/settings/users', label: 'People' }, { to: '/settings/service-principals', label: 'API tokens' }] : []),
    ...(authSettings ? [{ to: '/settings/auth', label: 'Sign-in & SSO' }] : []),
    ...(config ? [{ to: '/workspaces', label: 'Workspaces & projects' }] : []),
    ...(audit ? [{ to: '/settings/audit', label: 'Audit log' }] : []),
    ...(config ? [{ to: '/events', label: 'Event stream' }, { to: '/compatibility', label: 'Diagnostics' }] : []),
    ...(system ? [{ to: '/settings/system', label: 'System & backups' }] : []),
  ]
}

/**
 * Header for every Settings page. Wide screens get tabs; phones get a
 * section picker so no section is hidden off-screen.
 */
export function SettingsHeader() {
  const items = useSections()
  const compact = useCompact()
  const navigate = useNavigate()
  const path = useRouterState({ select: (r) => r.location.pathname.replace(/\/$/, '') })
  const current = items.find((i) => path === i.to || path.startsWith(`${i.to}/`))?.to ?? null
  return (
    <>
      <PageHeader title="Settings" />
      {compact ? (
        <Select aria-label="Settings section" value={current} allowDeselect={false} onChange={(v) => v && void navigate({ to: v })} data={items.map((i) => ({ value: i.to, label: i.label }))} />
      ) : (
        <LinkTabs label="Settings sections" items={items} />
      )}
    </>
  )
}
