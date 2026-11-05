import { createFileRoute, Outlet } from '@tanstack/react-router'
import { PageHeader } from '@/components/PageHeader'
import { SubNav } from '@/components/SubNav'

export const Route = createFileRoute('/quotas')({
  component: QuotasLayout,
})

function QuotasLayout() {
  return (
    <>
      <PageHeader
        title="Quotas"
        subtitle="Provider quota windows, balances and rate limits, how fresh they are, and how they affect routing. Unknown values stay unknown."
      />
      <SubNav
        label="Quota sections"
        items={[
          { to: '/quotas', label: 'Board', exact: true },
          { to: '/quotas/monitors', label: 'Monitors' },
        ]}
      />
      <Outlet />
    </>
  )
}
