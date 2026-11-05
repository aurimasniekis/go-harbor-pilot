import { createFileRoute, Outlet } from '@tanstack/react-router'
import { PageHeader } from '@/components/PageHeader'
import { SubNav } from '@/components/SubNav'

export const Route = createFileRoute('/compatibility')({
  component: CompatibilityLayout,
})

function CompatibilityLayout() {
  return (
    <>
      <PageHeader
        title="Compatibility"
        subtitle="Circuit breakers for provider protocol paths, the degradation plans apply while they are open, and the changes Protocol Watch has observed."
      />
      <SubNav
        label="Compatibility sections"
        items={[
          { to: '/compatibility', label: 'Breakers', exact: true },
          { to: '/compatibility/watch', label: 'Protocol Watch' },
          { to: '/compatibility/work-items', label: 'Work items' },
        ]}
      />
      <Outlet />
    </>
  )
}
