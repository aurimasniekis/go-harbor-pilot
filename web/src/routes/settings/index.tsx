import { createFileRoute, Navigate } from '@tanstack/react-router'
import { useCan } from '@/lib/session'
import { EmptyState, Panel } from '@/components/ui'
import { Lock } from 'lucide-react'

export const Route = createFileRoute('/settings/')({
  component: SettingsIndex,
})

function SettingsIndex() {
  const users = useCan('users.manage')
  const audit = useCan('audit.read')
  const system = useCan('system.manage')
  if (users) return <Navigate to="/settings/users" replace />
  if (audit) return <Navigate to="/settings/audit" replace />
  if (system) return <Navigate to="/settings/system" replace />
  return (
    <Panel>
      <EmptyState icon={Lock} title="No settings available">
        Your role does not include access to user, sign-in or audit settings.
      </EmptyState>
    </Panel>
  )
}
