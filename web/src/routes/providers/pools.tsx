import { createFileRoute, redirect } from '@tanstack/react-router'

// Pools are "fallback groups" under Routing.
export const Route = createFileRoute('/providers/pools')({
  beforeLoad: () => {
    throw redirect({ to: '/routing/pools', replace: true })
  },
})
