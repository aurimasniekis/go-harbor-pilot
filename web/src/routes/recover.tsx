import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { LifeBuoy } from 'lucide-react'
import { z } from 'zod'
import { Banner, Button } from '@/components/ui'
import { MutationError } from '@/components/form'
import { useRecover } from '@/lib/session'
import { AuthCard } from './login'

const search = z.object({ token: z.string().catch('') })

export const Route = createFileRoute('/recover')({
  validateSearch: (s) => search.parse(s),
  component: Recover,
})

function Recover() {
  const { token } = Route.useSearch()
  const navigate = useNavigate()
  const recover = useRecover()
  return (
    <AuthCard title="Break-glass recovery">
      <Banner tone="warning">
        Recovery sign-in is audited and raises a persistent security alert until an administrator acknowledges it.
      </Banner>
      {!token ? (
        <p className="secondary">Open the link printed by <code>harbor-pilot admin recover --email …</code>.</p>
      ) : (
        <>
          <MutationError error={recover.error} />
          <Button variant="primary" icon={LifeBuoy} busy={recover.isPending}
            onClick={() => recover.mutate(token, { onSuccess: () => void navigate({ to: '/account' }) })}>
            Sign in with recovery link
          </Button>
        </>
      )}
    </AuthCard>
  )
}
