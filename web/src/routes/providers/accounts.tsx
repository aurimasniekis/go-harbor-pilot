import { createFileRoute, redirect } from '@tanstack/react-router'

// Accounts are listed under their provider on Providers.
export const Route = createFileRoute('/providers/accounts')({
  beforeLoad: () => {
    throw redirect({ to: '/providers', replace: true })
  },
})
