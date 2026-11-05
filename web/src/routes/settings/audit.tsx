import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { AuditLog } from '@/features/audit/AuditLog'

const search = z.object({
  who: z.string().optional().catch(undefined),
  what: z.string().optional().catch(undefined),
  outcome: z.enum(['denied', 'error']).optional().catch(undefined),
  range: z.enum(['1', '7', '30', 'all']).optional().catch(undefined),
  q: z.string().optional().catch(undefined),
})

export type AuditSearch = z.infer<typeof search>

export const Route = createFileRoute('/settings/audit')({
  validateSearch: (s) => search.parse(s),
  component: AuditRoute,
})

function AuditRoute() {
  const params = Route.useSearch()
  const navigate = Route.useNavigate()
  return <AuditLog search={params} onSearch={(next) => void navigate({ search: (prev) => ({ ...prev, ...next }), replace: true })} />
}
