import { type FormEvent, useState } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { UserPlus } from 'lucide-react'
import { z } from 'zod'
import { Banner, Button } from '@/components/ui'
import { MutationError, TextField } from '@/components/form'
import { useSetup } from '@/lib/session'
import { AuthCard } from './login'

const search = z.object({ token: z.string().catch('') })

export const Route = createFileRoute('/setup')({
  validateSearch: (s) => search.parse(s),
  component: Setup,
})

function Setup() {
  const { token } = Route.useSearch()
  const navigate = useNavigate()
  const setup = useSetup()
  const [email, setEmail] = useState('')
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const mismatch = confirm !== '' && confirm !== password

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (mismatch) return
    setup.mutate({ token, email, display_name: name, password }, { onSuccess: () => void navigate({ to: '/' }) })
  }
  return (
    <AuthCard title="Create the first administrator">
      {!token ? (
        <Banner tone="warning">This page needs the one-time link printed by <code>harbor-pilot admin bootstrap</code>.</Banner>
      ) : (
        <form onSubmit={submit} style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <p className="secondary">The setup link works once and expires 30 minutes after it was created.</p>
          <TextField label="Email" type="email" autoComplete="username" value={email} onChange={setEmail} required autoFocus />
          <TextField label="Display name" value={name} onChange={setName} maxLength={120} />
          <TextField label="Password" type="password" autoComplete="new-password" value={password} onChange={setPassword}
            hint="At least 12 characters." required />
          <TextField label="Confirm password" type="password" autoComplete="new-password" value={confirm} onChange={setConfirm}
            error={mismatch ? 'Passwords do not match' : undefined} required />
          <MutationError error={setup.error} />
          <Button type="submit" variant="primary" icon={UserPlus} busy={setup.isPending} disabled={!email || password.length < 12 || mismatch}>
            Create administrator
          </Button>
        </form>
      )}
    </AuthCard>
  )
}
