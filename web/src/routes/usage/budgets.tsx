import { createFileRoute, redirect } from '@tanstack/react-router'

// Budgets live on Limits next to quota windows.
export const Route = createFileRoute('/usage/budgets')({
  beforeLoad: () => {
    throw redirect({ to: '/quotas', hash: 'budgets', replace: true })
  },
})
