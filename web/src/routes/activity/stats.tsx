import { createFileRoute, redirect } from '@tanstack/react-router'

// The statistics breakdown lives on Spend; the headline numbers on Requests.
export const Route = createFileRoute('/activity/stats')({
  beforeLoad: () => {
    throw redirect({ to: '/usage/breakdown', replace: true })
  },
})
