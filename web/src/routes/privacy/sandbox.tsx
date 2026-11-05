import { createFileRoute, redirect } from '@tanstack/react-router'

// The rule sandbox is the "Try a policy" section on Masking.
export const Route = createFileRoute('/privacy/sandbox')({
  beforeLoad: () => {
    throw redirect({ to: '/privacy', search: { try: true }, replace: true })
  },
})
