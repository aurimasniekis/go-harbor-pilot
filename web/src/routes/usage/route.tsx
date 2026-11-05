import { createFileRoute, Outlet } from '@tanstack/react-router'
import { PageHeader } from '@/components/PageHeader'
import { SubNav } from '@/components/SubNav'

export const Route = createFileRoute('/usage')({
  component: UsageLayout,
})

function UsageLayout() {
  return (
    <>
      <PageHeader
        title="Usage & cost"
        subtitle="Gateway-observed usage and every cost category kept apart: estimated and actual API charges, list-price equivalents, subscription fees and internal allocations."
      />
      <SubNav
        label="Usage sections"
        items={[
          { to: '/usage', label: 'Usage', exact: true },
          { to: '/usage/budgets', label: 'Budgets' },
        ]}
      />
      <Outlet />
    </>
  )
}
