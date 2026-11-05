import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Button } from '@/components/ui'
import { Advanced, Checkbox, Dialog, MutationError, SelectField, TextField, formStyles as f } from '@/components/form'
import { SegmentedControl, Stack, Switch, Text } from '@mantine/core'
import { KeyValueEditor } from '@/components/KeyValueEditor'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { resourceListQuery } from '@/lib/api/queries'
import { useSaveResource } from '@/lib/api/resources'
import type { AccountRes, AccountSpec, ProviderRes } from '@/lib/api/types'
import { billingClasses, toInt, toggle } from './constants'
import c from './config.module.css'

type CredMode = 'stored' | 'reference' | 'none'

/** Create or edit a provider account. Credentials are write-only. */
export function AccountDialog({ account, onClose, providerID: presetProvider }: { account: AccountRes | null; onClose: () => void; providerID?: string }) {
  const providers = useQuery(resourceListQuery<ProviderRes>('providers'))
  const init = account?.spec
  const cred = account?.secrets?.credential
  const [name, setName] = useState(account?.name ?? '')
  const [providerID, setProviderID] = useState(init?.provider_id ?? presetProvider ?? '')
  const [displayName, setDisplayName] = useState(init?.display_name ?? '')
  const [billing, setBilling] = useState<AccountSpec['billing']>(init?.billing ?? 'metered_api')
  const [mode, setMode] = useState<CredMode>(init?.credential_ref ? 'reference' : cred?.source === 'stored' ? 'stored' : account ? 'none' : 'stored')
  const [secret, setSecret] = useState('')
  const [clear, setClear] = useState(false)
  const [ref, setRef] = useState(init?.credential_ref ?? '')
  const [allowed, setAllowed] = useState<string[]>(init?.allowed_models ?? [])
  const [maxConc, setMaxConc] = useState(String(init?.max_concurrency ?? 0))
  const [rpm, setRpm] = useState(String(init?.requests_per_minute ?? 0))
  const [enabled, setEnabled] = useState(init?.enabled ?? true)
  const [labels, setLabels] = useState<Record<string, string>>(init?.labels ?? {})
  const [labelsValid, setLabelsValid] = useState(true)
  const save = useSaveResource<AccountSpec>('provider-accounts')

  const items = providers.data?.items ?? []
  const provider = items.find((p) => p.id === providerID)
  const models = provider?.spec.models ?? []

  const submit = () => {
    const spec: AccountSpec = {
      provider_id: providerID,
      display_name: displayName,
      credential_ref: mode === 'reference' ? ref.trim() : '',
      billing,
      allowed_models: allowed,
      max_concurrency: toInt(maxConc),
      requests_per_minute: toInt(rpm),
      enabled,
      labels,
    }
    let secrets: Record<string, string> | undefined
    if (mode === 'stored' && secret) secrets = { credential: secret }
    else if ((mode === 'stored' && clear) || (mode === 'none' && cred?.source === 'stored')) secrets = { credential: '' }
    save.mutate({ existing: account ?? undefined, body: { name, spec, secrets } }, { onSuccess: onClose })
  }

  return (
    <Dialog open title={account ? `Edit account ${account.name}` : 'Add account'} onClose={onClose}>
      <TerraformEditWarning r={account} />
      <SelectField label="Provider" value={providerID} onChange={(v) => { setProviderID(v); setAllowed([]) }} disabled={!!account}
        options={[{ value: '', label: providers.isLoading ? 'Loading…' : 'Select a provider' }, ...items.map((p) => ({ value: p.id, label: p.spec.display_name || p.name }))]} />
      <TextField label="Name" value={name} onChange={setName} disabled={!!account} required placeholder="e.g. team-key-1" hint={account ? undefined : 'Lowercase; cannot be changed later.'} />
      <Stack gap={6}>
        <Text size="sm" fw={500}>
          Credential
        </Text>
        <SegmentedControl
          aria-label="Credential source"
          size="sm"
          value={mode}
          onChange={(v) => setMode(v as CredMode)}
          data={[
            { value: 'stored', label: 'Paste it' },
            { value: 'reference', label: 'From env or file' },
            { value: 'none', label: 'None' },
          ]}
        />
        {mode === 'stored' && (
          <>
            <TextField label="Credential" type="password" value={secret} onChange={(v) => { setSecret(v); setClear(false) }} autoComplete="off"
              hint={cred?.source === 'stored' ? `One is saved (${cred.fingerprint ?? 'set'}). Leave empty to keep it.` : 'API key or setup token. Stored encrypted and never shown again.'} />
            {cred?.source === 'stored' && <Checkbox label="Remove the saved credential" checked={clear} onChange={(v) => { setClear(v); if (v) setSecret('') }} />}
          </>
        )}
        {mode === 'reference' && (
          <TextField label="Credential reference" value={ref} onChange={setRef} placeholder="env:ANTHROPIC_API_KEY or file:/run/secrets/key"
            hint="Read at request time; the value is never stored." />
        )}
      </Stack>
      <SelectField label="How it is paid" value={billing} onChange={setBilling} options={billingClasses.map((b) => ({ value: b.value, label: b.label }))}
        hint="Routing uses this to decide when a paid fallback is allowed." />
      <Advanced>
        <TextField label="Display name" value={displayName} onChange={setDisplayName} maxLength={120} placeholder="Optional" />
        {models.length > 0 && (
          <fieldset className={c.checks}>
            <legend className={c.legend}>Models this account may use (none checked means all)</legend>
            {models.map((m) => (
              <Checkbox key={m.id} label={<span className="mono">{m.id}</span>} checked={allowed.includes(m.id)} onChange={(on) => setAllowed(toggle(allowed, m.id, on))} />
            ))}
          </fieldset>
        )}
        <div className={f.grid}>
          <TextField label="Requests at once" type="number" suffix="max" placeholder="Unlimited" value={maxConc === '0' ? '' : maxConc} onChange={setMaxConc} />
          <TextField label="Requests per minute" type="number" suffix="/min" placeholder="Unlimited" value={rpm === '0' ? '' : rpm} onChange={setRpm} />
        </div>
        <KeyValueEditor label="Labels" value={labels} onChange={(v, ok) => { setLabels(v); setLabelsValid(ok) }}
          hint="Available to routing rules as account.labels. One key=value per line." />
        <Switch label="Account on" description="Off accounts are never routed to." checked={enabled} onChange={(e) => setEnabled(e.currentTarget.checked)} />
      </Advanced>
      <MutationError error={save.error} />
      <div className={f.actions}>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} disabled={!name || !providerID || !labelsValid || (mode === 'reference' && !ref)} onClick={submit}>
          Save account
        </Button>
      </div>
    </Dialog>
  )
}
