import { createFileRoute, Outlet } from '@tanstack/react-router'
import { PageHeader } from '@/components/PageHeader'
import { SubNav } from '@/components/SubNav'

export const Route = createFileRoute('/providers')({
  component: ProvidersLayout,
})

function ProvidersLayout() {
  return (
    <>
      <PageHeader title="Providers" subtitle="Upstream endpoints, the authorized accounts that call them, subscriptions and the pools routing draws from." />
      <SubNav
        label="Provider sections"
        items={[
          { to: '/providers', label: 'Providers', exact: true },
          { to: '/providers/accounts', label: 'Accounts' },
          { to: '/providers/subscriptions', label: 'Subscriptions' },
          { to: '/providers/pools', label: 'Pools' },
        ]}
      />
      <Outlet />
    </>
  )
}
