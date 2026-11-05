import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { LimitsOverview } from '@/features/limits/LimitsPage'

export const Route = createFileRoute('/quotas/')({
  validateSearch: z.object({ add: z.literal('budget').optional().catch(undefined) }).parse,
  component: LimitsPage,
})

function LimitsPage() {
  const { add } = Route.useSearch()
  return <LimitsOverview addBudget={add === 'budget'} />
}
