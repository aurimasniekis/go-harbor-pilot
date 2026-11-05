import { createFileRoute, Outlet, useNavigate, useRouterState } from '@tanstack/react-router'
import { ActionIcon, Button, Group, SegmentedControl, Tooltip } from '@mantine/core'
import { Plus } from 'lucide-react'
import { z } from 'zod'
import { LinkTabs, PageHeader, useCompact } from '@/ui/kit'

export const usageRanges = [
  { value: '1', label: '24h' },
  { value: '7', label: '7d' },
  { value: '30', label: '30d' },
  { value: '90', label: '90d' },
] as const

export type UsageRange = 1 | 7 | 30 | 90

const rangeSchema = z.coerce
  .number()
  .pipe(z.union([z.literal(1), z.literal(7), z.literal(30), z.literal(90)]))
  .optional()
  .catch(undefined)

/**
 * Spend state lives in the URL so reloads, back/forward and shared links
 * keep the period, grouping and chart choices. Both tabs share the range.
 */
export const spendSearch = z.object({
  range: rangeSchema,
  /** Overview breakdown grouping. */
  group: z.enum(['account', 'binding', 'model', 'project']).optional().catch(undefined),
  /** Overview chart: cost or tokens. */
  view: z.enum(['cost', 'tokens']).optional().catch(undefined),
  /** Breakdown grouping. */
  by: z.enum(['client', 'harness', 'intent', 'model', 'account', 'provider', 'plan', 'project', 'session']).optional().catch(undefined),
  /** Breakdown chart metric. */
  metric: z.enum(['requests', 'tokens', 'latency', 'cost']).optional().catch(undefined),
})

export type SpendSearch = z.infer<typeof spendSearch>

export const Route = createFileRoute('/usage')({
  validateSearch: spendSearch.parse,
  component: UsageLayout,
})

export const DEFAULT_RANGE: UsageRange = 30

function UsageLayout() {
  const search = Route.useSearch()
  const range = search.range ?? DEFAULT_RANGE
  const navigate = useNavigate()
  const compact = useCompact()
  const onBreakdown = useRouterState({ select: (r) => r.location.pathname.startsWith('/usage/breakdown') })
  const setRange = (v: string) =>
    void navigate({ to: onBreakdown ? '/usage/breakdown' : '/usage', search: { ...search, range: Number(v) as UsageRange } })
  const rangeControl = (
    <SegmentedControl
      size={compact ? 'md' : 'sm'}
      aria-label="Time range"
      value={String(range)}
      onChange={setRange}
      data={usageRanges.map((r) => ({ value: r.value, label: r.label }))}
    />
  )
  const addBudget = () => void navigate({ to: '/quotas', search: { add: 'budget' } })
  return (
    <>
      <PageHeader
        title="Spend"
        actions={
          compact ? (
            <Tooltip label="Add budget">
              <ActionIcon variant="light" size="lg" aria-label="Add budget" onClick={addBudget}>
                <Plus size={16} />
              </ActionIcon>
            </Tooltip>
          ) : (
            <>
              {rangeControl}
              <Button variant="light" leftSection={<Plus size={14} />} onClick={addBudget}>
                Add budget
              </Button>
            </>
          )
        }
      />
      <LinkTabs
        label="Spend sections"
        search={search.range ? { range: search.range } : undefined}
        items={[
          { to: '/usage', label: 'Overview', exact: true },
          { to: '/usage/breakdown', label: 'Breakdown' },
        ]}
      />
      {compact && <Group mb="md">{rangeControl}</Group>}
      <Outlet />
    </>
  )
}
