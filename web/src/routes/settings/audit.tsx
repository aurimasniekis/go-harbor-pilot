import { createFileRoute } from '@tanstack/react-router'
import { AuditLog } from '@/features/audit/AuditLog'

export const Route = createFileRoute('/settings/audit')({
  component: AuditLog,
})
