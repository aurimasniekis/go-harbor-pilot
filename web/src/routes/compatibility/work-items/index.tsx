import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { z } from 'zod'
import { WorkItemsPanel } from '@/features/compat/WorkItems'
import type { WorkStatus } from '@/lib/api/types'

const statuses = ['new', 'acknowledged', 'investigating', 'fix_available', 'verified', 'resolved', 'ignored'] as const

const search = z.object({ status: z.enum(statuses).optional().catch(undefined) })

export const Route = createFileRoute('/compatibility/work-items/')({
  validateSearch: (s) => search.parse(s),
  component: WorkItems,
})

function WorkItems() {
  const { status } = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  return <WorkItemsPanel status={status} onStatus={(s: WorkStatus | undefined) => void navigate({ search: { status: s } })} />
}
