import { createFileRoute, Outlet } from '@tanstack/react-router'
import { PageHeader } from '@/components/PageHeader'
import { SubNav } from '@/components/SubNav'

export const Route = createFileRoute('/privacy')({
  component: PrivacyLayout,
})

function PrivacyLayout() {
  return (
    <>
      <PageHeader
        title="Privacy"
        subtitle="Outbound masking before anything reaches a provider, a sandbox to test rules, and the short-lived trace policies plans can opt into."
      />
      <SubNav
        label="Privacy sections"
        items={[
          { to: '/privacy', label: 'Masking policies', exact: true },
          { to: '/privacy/sandbox', label: 'Rule sandbox' },
          { to: '/privacy/trace-policies', label: 'Trace policies' },
        ]}
      />
      <Outlet />
    </>
  )
}
