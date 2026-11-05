import { Button as MButton, Group, Paper, Stack, Title } from '@mantine/core'
import { type FormEvent, useState } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { KeyRound, LogIn, ShipWheel } from 'lucide-react'
import { z } from 'zod'
import { Banner, Button } from '@/components/ui'
import { TextField, formStyles as f } from '@/components/form'
import { useLogin } from '@/lib/session'
import { metaQuery, sessionQuery } from '@/lib/api/queries'
import { API, ApiError } from '@/lib/api/client'

const search = z.object({ next: z.string().optional().catch(undefined), error: z.string().optional().catch(undefined) })

export const Route = createFileRoute('/login')({
  validateSearch: (s) => search.parse(s),
  component: Login,
})

export function safeNext(next: string | undefined): string {
  // Only allow same-origin absolute paths to prevent open redirects.
  if (!next || !next.startsWith('/') || next.startsWith('//') || next.startsWith('/\\') || next.startsWith('/login')) return '/'
  return next
}

export function AuthCard({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <main id="main" className={f.authPage}>
      <Paper component="section" withBorder radius="lg" p="xl" w="min(420px, 100%)" aria-labelledby="auth-title">
        <Stack gap="md">
          <Group gap={10} wrap="nowrap">
            <ShipWheel size={22} color="var(--accent)" aria-hidden="true" />
            <Title order={1} id="auth-title" fz="lg">
              {title}
            </Title>
          </Group>
          {children}
        </Stack>
      </Paper>
    </main>
  )
}

/**
 * Messages the server may put in ?error= after single sign-on. Anything else
 * (including text an attacker puts in a link) shows a generic message, so
 * the page never displays arbitrary URL text as an official notice.
 */
const SSO_ERRORS: Record<string, string> = {
  'sign-in request expired or was not started from this browser': 'The sign-in request expired or was started in another browser. Try again.',
  'no Harbor Pilot account is linked to this identity; ask an administrator to invite you': 'No Harbor Pilot account is linked to this identity. Ask an administrator to invite you.',
  'your identity provider groups do not grant access to Harbor Pilot': 'Your identity provider groups do not grant access to Harbor Pilot.',
  'this sign-in provider is disabled': 'This sign-in provider is turned off.',
  'your email domain is not allowed to sign in': 'Your email domain is not allowed to sign in.',
  'this identity is already linked to another account': 'This identity is already linked to another account.',
}

export function ssoErrorMessage(code: string | undefined): string | undefined {
  if (!code) return undefined
  return SSO_ERRORS[code] ?? 'Single sign-on did not complete. Try again, or ask an administrator to check the identity provider settings.'
}

function Login() {
  const { next, error } = Route.useSearch()
  const navigate = useNavigate()
  const login = useLogin()
  const session = useQuery(sessionQuery)
  const meta = useQuery(metaQuery)
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')

  if (session.data?.authenticated) {
    void navigate({ to: safeNext(next), replace: true })
  }

  const submit = (e: FormEvent) => {
    e.preventDefault()
    login.mutate({ email, password }, { onSuccess: () => void navigate({ to: safeNext(next) }) })
  }
  const err = login.error
  const msg =
    err instanceof ApiError
      ? err.status === 429
        ? 'Too many attempts. Wait and try again.'
        : err.message
      : err
        ? 'Sign-in failed.'
        : null
  const providers = session.data?.oidc_providers ?? []

  return (
    <AuthCard title="Harbor Pilot">
      {error && (
        <Banner tone="error" role="alert">
          {ssoErrorMessage(error)}
        </Banner>
      )}
      {meta.data?.demo && (
        <Banner tone="purple">
          Demo instance with synthetic data. Sign in as <code>admin@example.test</code> with password <code>harbor-pilot-demo</code>.
        </Banner>
      )}
      {session.data?.setup_required && (
        <Banner tone="info">
          No administrator exists yet. Run <code>harbor-pilot admin bootstrap</code> on the server and open the one-time setup
          link it prints.
        </Banner>
      )}
      {providers.map((p) => (
        <MButton
          key={p.slug}
          component="a"
          variant="default"
          fullWidth
          leftSection={<KeyRound size={14} aria-hidden="true" />}
          href={`${API}/auth/oidc/${encodeURIComponent(p.slug)}/start?return_to=${encodeURIComponent(safeNext(next))}`}
        >
          Continue with {p.display_name}
        </MButton>
      ))}
      {providers.length > 0 && session.data?.password_login && <div className={f.divider}>or</div>}
      {session.data?.password_login !== false && (
        <form onSubmit={submit} style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <TextField label="Email" type="email" autoComplete="username" value={email} onChange={setEmail} required autoFocus />
          <TextField label="Password" type="password" autoComplete="current-password" value={password} onChange={setPassword} required />
          {msg && (
            <Banner tone="error" role="alert">
              {msg}
            </Banner>
          )}
          <Button type="submit" variant="primary" icon={LogIn} busy={login.isPending} disabled={!email || !password}>
            Sign in
          </Button>
        </form>
      )}
    </AuthCard>
  )
}
