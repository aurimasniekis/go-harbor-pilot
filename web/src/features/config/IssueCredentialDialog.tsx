import { useEffect, useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Accordion, Alert, Checkbox as MCheckbox, Group, Loader, Stack, Text } from '@mantine/core'
import { Link } from '@tanstack/react-router'
import { KeyRound } from 'lucide-react'
import { Button } from '@/components/ui'
import { Dialog, MutationError, NumberField, RevealDialog, SelectField, TextField, useFieldErrors } from '@/components/form'
import { apiSend } from '@/lib/api/client'
import type { BindingRes, Credential, IssuedCredential } from '@/lib/api/types'
import { ManualSetup } from '@/features/harness/ManualSetup'
import { harnessKinds, type HarnessKind } from '@/features/harness/guides'
import { DEFAULT_SCOPES, kindForTool, scopeLabel, tokenScopes } from '@/features/harness/tools'

const expiryOptions = [
  { value: '7', label: '7 days' },
  { value: '30', label: '30 days' },
  { value: '90', label: '90 days' },
  { value: '365', label: '1 year' },
  { value: 'custom', label: 'Custom…' },
  { value: 'never', label: 'Never expires' },
]

/** Seconds until expiry for the chosen option; 0 means no expiry. */
export function expirySeconds(choice: string, customDays: number | null): number | null {
  if (choice === 'never') return 0
  if (choice === 'custom') return customDays && customDays > 0 ? customDays * 86_400 : null
  const d = Number(choice)
  return Number.isInteger(d) && d > 0 ? d * 86_400 : null
}

/** How to connect the client's tool with the token just created. */
function NextSteps({ binding, issued }: { binding: BindingRes; issued: IssuedCredential }) {
  const own = kindForTool(binding.spec.expected_harness)
  const url = issued.base_urls.anthropic ?? ''
  const others = harnessKinds.filter((k) => k.value !== own)
  return (
    <Stack gap="sm">
      <Text fw={600}>Connect {harnessKinds.find((k) => k.value === own)?.label}</Text>
      <ManualSetup kind={own} input={{ url, model: '', bindingId: binding.id }} />
      <Accordion variant="contained" radius="md">
        {others.map((k) => (
          <Accordion.Item key={k.value} value={k.value}>
            <Accordion.Control>Use it with {k.label} instead</Accordion.Control>
            <Accordion.Panel>
              <ManualSetup kind={k.value as HarnessKind} input={{ url, model: '', bindingId: binding.id }} />
            </Accordion.Panel>
          </Accordion.Item>
        ))}
      </Accordion>
      <Text size="sm" c="dimmed">
        More options, including a command that writes the configuration for you:{' '}
        <Link to="/clients/setup" search={{ binding: binding.id }}>
          Set up a tool
        </Link>
        .
      </Text>
    </Stack>
  )
}

/**
 * Create an access token for a client. `auto` issues it right away with the
 * defaults (after "Create client and token"); `replacing` creates a
 * successor of an existing token and offers to revoke the old one.
 */
export function IssueCredentialDialog({ binding, onClose, auto, replacing }: {
  binding: BindingRes
  onClose: () => void
  auto?: boolean
  replacing?: Credential
}) {
  const qc = useQueryClient()
  const [name, setName] = useState(replacing?.name ?? binding.name)
  const [scopes, setScopes] = useState<string[]>(replacing?.scopes ?? DEFAULT_SCOPES)
  const [expiry, setExpiry] = useState('90')
  const [customDays, setCustomDays] = useState<number | null>(30)
  const [showScopes, setShowScopes] = useState(false)
  const [issued, setIssued] = useState<IssuedCredential | null>(null)
  const [oldRevoked, setOldRevoked] = useState(false)
  const ttl = expirySeconds(expiry, customDays)
  const issue = useMutation({
    mutationFn: () =>
      apiSend<IssuedCredential>('POST', `/client-bindings/${encodeURIComponent(binding.id)}/credentials`, { name: name.trim(), scopes, ttl_seconds: ttl ?? 0 }),
    onSuccess: (r) => {
      setIssued(r)
      void qc.invalidateQueries({ queryKey: ['resources'] })
    },
  })
  const revokeOld = useMutation({
    mutationFn: () =>
      apiSend<void>('DELETE', `/client-bindings/${encodeURIComponent(binding.id)}/credentials/${encodeURIComponent(replacing!.id)}`),
    onSuccess: () => {
      setOldRevoked(true)
      void qc.invalidateQueries({ queryKey: ['resources'] })
    },
  })
  const fe = useFieldErrors(issue.error)
  const started = useRef(false)
  useEffect(() => {
    if (auto && !started.current) {
      started.current = true
      issue.mutate()
    }
  }, [auto, issue])

  if (issued) {
    return (
      <RevealDialog open title="Access token created" value={issued.token} label="access token" onDone={onClose}>
        {replacing && (
          <Alert color={oldRevoked ? 'green' : 'blue'} variant="light" title={oldRevoked ? 'Old token revoked' : 'Replace the old token'}>
            <Stack gap="xs">
              <Text size="sm">
                {oldRevoked
                  ? `${replacing.name} (hpk_${replacing.prefix}_…) no longer works.`
                  : `Give the new token to the tool first. Then revoke ${replacing.name} (hpk_${replacing.prefix}_…) so it stops working.`}
              </Text>
              {!oldRevoked && (
                <Group>
                  <Button size="small" busy={revokeOld.isPending} onClick={() => revokeOld.mutate()}>
                    Revoke the old token now
                  </Button>
                </Group>
              )}
              <MutationError error={revokeOld.error} />
            </Stack>
          </Alert>
        )}
        <NextSteps binding={binding} issued={issued} />
      </RevealDialog>
    )
  }

  if (auto && !issue.isError) {
    return (
      <Dialog open title="Create access token" onClose={onClose} lockClose>
        <Group gap="sm">
          <Loader size="sm" />
          <Text size="sm">Creating an access token for {binding.spec.display_name || binding.name}…</Text>
        </Group>
      </Dialog>
    )
  }

  const nameMissing = !name.trim()
  const reason = nameMissing ? 'Give the token a name.' : scopes.length === 0 ? 'Choose at least one permission.' : ttl === null ? 'Choose when it expires.' : ''
  return (
    <Dialog
      open
      title={replacing ? `Replace ${replacing.name}` : 'Create access token'}
      onClose={onClose}
      footer={
        <>
          {reason && (
            <Text size="xs" c="dimmed" mr="auto">
              {reason}
            </Text>
          )}
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" icon={KeyRound} busy={issue.isPending} disabled={!!reason} onClick={() => issue.mutate()}>
            Create token
          </Button>
        </>
      }
    >
      <TextField label="Token name" value={name} onChange={setName} maxLength={80} required autoFocus error={fe.name ?? (nameMissing ? 'Required' : undefined)}
        hint="Where it is used (a machine or a person), so you know which one to revoke." />
      <Group align="flex-start" gap="md" wrap="wrap">
        <SelectField label="Expires" value={expiry} onChange={setExpiry} options={expiryOptions} />
        {expiry === 'custom' && (
          <NumberField label="After" unit="days" value={customDays} onChange={setCustomDays} min={1} max={3650} w={160} required
            error={customDays ? undefined : 'Enter a number of days'} />
        )}
      </Group>
      <Stack gap={4}>
        <Text size="sm" fw={500}>
          Permissions
        </Text>
        <Text size="sm" c={scopes.length ? undefined : 'red'}>
          {scopes.length ? `Can: ${scopes.map(scopeLabel).join(', ')}` : 'No permissions chosen'}
          {' · '}
          <Text span size="sm" c="var(--link)" style={{ cursor: 'pointer' }} onClick={() => setShowScopes(!showScopes)} role="button" tabIndex={0}
            onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && setShowScopes(!showScopes)}>
            {showScopes ? 'Done' : 'Change'}
          </Text>
        </Text>
        {showScopes && (
          <MCheckbox.Group value={scopes} onChange={setScopes} aria-label="This token can" error={scopes.length ? undefined : 'Choose at least one permission.'}>
            <Stack gap={6} mt={4}>
              {tokenScopes.map((sc) => (
                <MCheckbox key={sc.value} value={sc.value} label={sc.label} description={sc.hint} />
              ))}
            </Stack>
          </MCheckbox.Group>
        )}
      </Stack>
      <MutationError error={issue.error} />
    </Dialog>
  )
}
