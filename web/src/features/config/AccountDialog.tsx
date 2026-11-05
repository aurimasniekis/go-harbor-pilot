import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Alert, Checkbox as MCheckbox, Code, Group, SegmentedControl, SimpleGrid, Stack, Switch, Text } from '@mantine/core'
import { Button } from '@/components/ui'
import {
  Advanced,
  Checkbox,
  Dialog,
  MutationError,
  NameField,
  NumberField,
  SecretRefField,
  SelectField,
  TextField,
  checkSecretRef,
  nameError,
  slugify,
  useFieldErrors,
} from '@/components/form'
import { KeyValueEditor } from '@/components/KeyValueEditor'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { ApiError } from '@/lib/api/client'
import { resourceListQuery } from '@/lib/api/queries'
import { useSaveResource } from '@/lib/api/resources'
import type { AccountRes, AccountSpec, ProviderRes } from '@/lib/api/types'
import { CodeBlock } from '@/features/harness/CodeBlock'
import { billingLabels, freeName, isBridge, providerLabel } from '@/features/providers/labels'

type CredMode = 'stored' | 'reference' | 'none'

/** What the credential is called for a provider type. */
export function credentialName(type: string | undefined): string {
  return isBridge(type) ? 'Sign-in token' : 'API key'
}

/** The billing a new account of a provider type most likely has. */
export function defaultBilling(type: string | undefined): AccountSpec['billing'] {
  if (isBridge(type)) return 'subscription'
  if (type === 'ollama') return 'local'
  return 'metered_api'
}

/** Placeholder variable name for a provider type (HP_SECRET_<this>). */
function refExample(type: string | undefined): string {
  switch (type) {
    case 'openai':
      return 'OPENAI_KEY'
    case 'anthropic':
      return 'ANTHROPIC_KEY'
    case 'gemini':
      return 'GEMINI_KEY'
    case 'claude_code':
      return 'CLAUDE_SETUP_TOKEN'
    case 'codex':
      return 'CODEX_AUTH'
    default:
      return 'API_KEY'
  }
}

/** How to get a sign-in token for a subscription bridge. */
export function BridgeSignIn({ type }: { type: string | undefined }) {
  if (type === 'claude_code') {
    return (
      <Alert color="blue" variant="light" title="Get a sign-in token for this subscription">
        <Stack gap={6}>
          <Text size="sm">
            On a machine where you are signed in to Claude Code with that subscription, run this and paste the token it prints (it starts with{' '}
            <Code>sk-ant-oat</Code>). Each account needs its own subscription login.
          </Text>
          <CodeBlock code="claude setup-token" label="Create a Claude sign-in token" />
        </Stack>
      </Alert>
    )
  }
  if (type === 'codex') {
    return (
      <Alert color="blue" variant="light" title="Get a sign-in file for this ChatGPT plan">
        <Stack gap={6}>
          <Text size="sm">Sign in to Codex in a separate folder, then paste the whole contents of the auth.json file it writes.</Text>
          <CodeBlock code={'export CODEX_HOME="$(mktemp -d)" && codex login && cat "$CODEX_HOME/auth.json"'} label="Create a Codex sign-in file" />
        </Stack>
      </Alert>
    )
  }
  return null
}

/** Create or edit a provider account. Credentials are write-only. */
export function AccountDialog({ account, onClose, onSaved, providerID: presetProvider }: {
  account: AccountRes | null
  onClose: () => void
  /** Called with the saved account; `created` is true for a new one. */
  onSaved?: (a: AccountRes, created: boolean) => void
  providerID?: string
}) {
  const providers = useQuery(resourceListQuery<ProviderRes>('providers'))
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const init = account?.spec
  const cred = account?.secrets?.credential
  const [providerID, setProviderID] = useState(init?.provider_id ?? presetProvider ?? '')
  const items = providers.data?.items ?? []
  const provider = items.find((p) => p.id === providerID)
  const type = provider?.spec.type
  const taken = useMemo(() => new Set((accounts.data?.items ?? []).map((a) => a.name)), [accounts.data])
  const [displayName, setDisplayName] = useState(init?.display_name ?? '')
  const [slug, setSlug] = useState('')
  const [slugEdited, setSlugEdited] = useState(false)
  const [billing, setBilling] = useState<AccountSpec['billing'] | ''>(init?.billing ?? '')
  const [mode, setMode] = useState<CredMode>(init?.credential_ref ? 'reference' : cred?.source === 'stored' ? 'stored' : account ? 'none' : 'stored')
  const [secret, setSecret] = useState('')
  const [clear, setClear] = useState(false)
  const [ref, setRef] = useState(init?.credential_ref ?? '')
  const [allowed, setAllowed] = useState<string[]>(init?.allowed_models ?? [])
  const [maxConc, setMaxConc] = useState<number | null>(init?.max_concurrency || null)
  const [rpm, setRpm] = useState<number | null>(init?.requests_per_minute || null)
  const [enabled, setEnabled] = useState(init?.enabled ?? true)
  const [labels, setLabels] = useState<Record<string, string>>(init?.labels ?? {})
  const [labelsValid, setLabelsValid] = useState(true)
  const save = useSaveResource<AccountSpec, AccountRes>('provider-accounts')
  const fe = useFieldErrors(save.error)

  const models = provider?.spec.models ?? []
  const effBilling = billing || defaultBilling(type)
  const name = account
    ? account.name
    : slugEdited
      ? slug
      : displayName.trim()
        ? freeName(slugify(displayName), taken)
        : provider
          ? freeName(`${provider.name}-${isBridge(type) ? 'seat' : 'key'}`, taken)
          : ''
  const credLabel = credentialName(type)
  const conflict = save.error instanceof ApiError && save.error.status === 409
  const reason = !providerID
    ? 'Choose a provider.'
    : !name || nameError(name)
      ? 'Fix the short name.'
      : mode === 'reference' && (!ref.trim() || checkSecretRef(ref))
        ? 'Fix the credential reference.'
        : !labelsValid
          ? 'Fix the labels.'
          : ''

  const submit = () => {
    const spec: AccountSpec = {
      provider_id: providerID,
      display_name: displayName.trim(),
      credential_ref: mode === 'reference' ? ref.trim() : '',
      billing: effBilling,
      allowed_models: allowed,
      max_concurrency: maxConc ?? 0,
      requests_per_minute: rpm ?? 0,
      enabled,
      labels,
    }
    let secrets: Record<string, string> | undefined
    if (mode === 'stored' && secret) secrets = { credential: secret.trim() }
    else if ((mode === 'stored' && clear) || (mode === 'none' && cred?.source === 'stored')) secrets = { credential: '' }
    save.mutate({ existing: account ?? undefined, body: { name, spec, secrets } }, { onSuccess: (a) => (onSaved ? onSaved(a, !account) : onClose()) })
  }

  return (
    <Dialog
      open
      title={account ? `Edit ${account.spec.display_name || account.name}` : provider ? `Add an account at ${providerLabel(provider)}` : 'Add account'}
      onClose={onClose}
      footer={
        <>
          {reason && (
            <Text size="xs" c="dimmed" mr="auto">
              {reason}
            </Text>
          )}
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" busy={save.isPending} disabled={!!reason} onClick={submit}>
            {account ? 'Save account' : 'Add account'}
          </Button>
        </>
      }
    >
      <TerraformEditWarning r={account} />
      {!presetProvider || account ? (
        <SelectField label="Provider" value={providerID} onChange={(v) => { setProviderID(v); setAllowed([]) }} disabled={!!account}
          options={items.map((p) => ({ value: p.id, label: providerLabel(p) }))} placeholder={providers.isLoading ? 'Loading…' : 'Choose a provider'} required />
      ) : null}
      <TextField label="Name" value={displayName} onChange={setDisplayName} maxLength={120} autoFocus
        placeholder={isBridge(type) ? 'e.g. Max seat — Alex' : 'e.g. Production key'} hint="How the account is shown in lists." error={fe.display_name} />
      {!account && (
        <NameField
          value={name}
          onChange={(v) => {
            setSlugEdited(v !== '')
            setSlug(v)
          }}
          error={fe.name ?? (conflict ? `An account called ${name} already exists (names are unique across all providers).` : undefined)}
        />
      )}
      <Stack gap={6}>
        <Text size="sm" fw={500}>
          {credLabel}
        </Text>
        <SegmentedControl
          aria-label={`${credLabel} source`}
          size="sm"
          value={mode}
          onChange={(v) => setMode(v as CredMode)}
          data={[
            { value: 'stored', label: 'Paste it' },
            { value: 'reference', label: 'From the server environment' },
            { value: 'none', label: 'None' },
          ]}
        />
        {mode === 'stored' && (
          <>
            {isBridge(type) && !cred?.set && <BridgeSignIn type={type} />}
            <TextField label={credLabel} type="password" value={secret} onChange={(v) => { setSecret(v); setClear(false) }} autoComplete="off"
              hint={cred?.source === 'stored' ? `One is saved (${cred.fingerprint ?? 'set'}). Leave empty to keep it.` : 'Stored encrypted and never shown again.'}
              error={fe.credential} />
            {cred?.source === 'stored' && <Checkbox label="Remove the saved credential" checked={clear} onChange={(v) => { setClear(v); if (v) setSecret('') }} />}
          </>
        )}
        {mode === 'reference' && <SecretRefField label={`${credLabel} reference`} value={ref} onChange={setRef} example={refExample(type)} error={fe.credential_ref} />}
        {mode === 'none' && type !== 'ollama' && (
          <Text size="xs" c="dimmed">
            Requests through this account will fail until it has a credential.
          </Text>
        )}
      </Stack>
      <SelectField label="How it is paid" value={effBilling} onChange={(v) => setBilling(v as AccountSpec['billing'])} options={Object.entries(billingLabels).map(([value, b]) => ({ value, label: `${b.label} — ${b.hint}` }))}
        hint="Routing uses this to decide when falling back to a paid account is allowed." error={fe.billing} />
      <Switch label="Account on" description="Off accounts are never routed to." checked={enabled} onChange={(e) => setEnabled(e.currentTarget.checked)} />
      <Advanced label="Limits, models and labels">
        {models.length > 0 && (
          <MCheckbox.Group label="Models this account may use" description="None checked means all of the provider’s models." value={allowed} onChange={setAllowed}>
            <Group gap="md" mt={6}>
              {models.map((m) => (
                <MCheckbox key={m.id} value={m.id} label={m.display_name || m.id} />
              ))}
            </Group>
          </MCheckbox.Group>
        )}
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
          <NumberField label="Requests at once" unit="at once" placeholder="Unlimited" min={1} max={10_000} value={maxConc} onChange={setMaxConc} error={fe.max_concurrency} />
          <NumberField label="Requests per minute" unit="/min" placeholder="Unlimited" min={1} max={1_000_000} value={rpm} onChange={setRpm} error={fe.requests_per_minute} />
        </SimpleGrid>
        <KeyValueEditor label="Labels" value={labels} onChange={(v, ok) => { setLabels(v); setLabelsValid(ok) }}
          hint="Available to routing rules as account.labels. One key=value per line." error={fe.labels} />
      </Advanced>
      <MutationError error={conflict ? null : save.error} />
    </Dialog>
  )
}

