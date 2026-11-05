import { createFileRoute, Outlet, useRouterState } from '@tanstack/react-router'
import { LinkTabs, PageHeader } from '@/ui/kit'

export const Route = createFileRoute('/routing')({
  component: RoutingLayout,
})

function RoutingLayout() {
  // A single plan is its own page with breadcrumbs, not a tab.
  const detail = useRouterState({ select: (r) => /^\/routing\/plans\/pln_/.test(r.location.pathname) })
  if (detail) return <Outlet />
  return (
    <>
      <PageHeader title="Routing" info="Where each model request goes: plans decide, model names point at fallback groups, and fallback groups list the accounts to try." />
      <LinkTabs
        label="Routing sections"
        items={[
          { to: '/routing/plans', label: 'Plans' },
          { to: '/routing', label: 'Model names', exact: true },
          { to: '/routing/pools', label: 'Fallback groups' },
        ]}
      />
      <Outlet />
    </>
  )
}
