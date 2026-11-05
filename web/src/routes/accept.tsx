import { type FormEvent, useState } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { KeyRound } from 'lucide-react'
import { z } from 'zod'
import { Banner, Button } from '@/components/ui'
import { MutationError, TextField } from '@/components/form'
import { useAccept } from '@/lib/session'
import { AuthCard } from './login'

const search = z.object({ token: z.string().catch(''), purpose: z.enum(['invite', 'reset']).catch('invite') })

export const Route = createFileRoute('/accept')({
  validateSearch: (s) => search.parse(s),
  component: Accept,
})

function Accept() {
  const { token, purpose } = Route.useSearch()
  const navigate = useNavigate()
  const accept = useAccept()
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const mismatch = confirm !== '' && confirm !== password
  const submit = (e: FormEvent) => {
    e.preventDefault()
    accept.mutate({ token, purpose, password }, { onSuccess: () => void navigate({ to: '/' }) })
  }
  return (
    <AuthCard title={purpose === 'reset' ? 'Reset your password' : 'Accept your invitation'}>
      {!token ? (
        <Banner tone="warning">This page needs the one-time link you received from an administrator.</Banner>
      ) : (
        <form onSubmit={submit} style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <TextField label="New password" type="password" autoComplete="new-password" value={password} onChange={setPassword}
            hint="At least 12 characters." required autoFocus />
          <TextField label="Confirm password" type="password" autoComplete="new-password" value={confirm} onChange={setConfirm}
            error={mismatch ? 'Passwords do not match' : undefined} required />
          <MutationError error={accept.error} />
          <Button type="submit" variant="primary" icon={KeyRound} busy={accept.isPending} disabled={password.length < 12 || mismatch}>
            Set password and sign in
          </Button>
        </form>
      )}
    </AuthCard>
  )
}
