import { createFileRoute, Outlet } from '@tanstack/react-router'
import { PageHeader } from '@/components/PageHeader'
import { SubNav } from '@/components/SubNav'

export const Route = createFileRoute('/routing')({
  component: RoutingLayout,
})

function RoutingLayout() {
  return (
    <>
      <PageHeader title="Routing" subtitle="Model aliases clients request, and versioned routing plans that decide which account and model serve them." />
      <SubNav label="Routing sections" items={[{ to: '/routing', label: 'Aliases', exact: true }, { to: '/routing/plans', label: 'Plans' }]} />
      <Outlet />
    </>
  )
}
