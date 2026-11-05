import { createFileRoute, Outlet, useNavigate, useRouterState } from '@tanstack/react-router'
import { Button, SegmentedControl } from '@mantine/core'
import { Plus } from 'lucide-react'
import { z } from 'zod'
import { PageHeader } from '@/ui/kit'

export const usageRanges = [
  { value: '1', label: '24h' },
  { value: '7', label: '7d' },
  { value: '30', label: '30d' },
  { value: '90', label: '90d' },
] as const

export type UsageRange = 1 | 7 | 30 | 90

export const Route = createFileRoute('/usage')({
  validateSearch: z.object({ range: z.union([z.literal(1), z.literal(7), z.literal(30), z.literal(90)]).optional().catch(undefined) }).parse,
  component: UsageLayout,
})

function UsageLayout() {
  const { range = 30 } = Route.useSearch()
  const navigate = useNavigate()
  const onSpend = useRouterState({ select: (r) => r.location.pathname.replace(/\/$/, '') === '/usage' })
  if (!onSpend) {
    return (
      <>
        <PageHeader title="Budgets" />
        <Outlet />
      </>
    )
  }
  return (
    <>
      <PageHeader
        title="Spend"
        actions={
          <>
            <SegmentedControl
              size="sm"
              aria-label="Time range"
              value={String(range)}
              onChange={(v) => void navigate({ to: '/usage', search: { range: Number(v) as UsageRange } })}
              data={usageRanges.map((r) => ({ value: r.value, label: r.label }))}
            />
            <Button variant="light" leftSection={<Plus size={14} />} onClick={() => void navigate({ to: '/quotas', search: { add: 'budget' } })}>
              Add budget
            </Button>
          </>
        }
      />
      <Outlet />
    </>
  )
}
