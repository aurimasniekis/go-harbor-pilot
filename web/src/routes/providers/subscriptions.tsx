import { createFileRoute, redirect } from '@tanstack/react-router'

// Subscriptions are part of each account on Providers.
export const Route = createFileRoute('/providers/subscriptions')({
  beforeLoad: () => {
    throw redirect({ to: '/providers', replace: true })
  },
})
