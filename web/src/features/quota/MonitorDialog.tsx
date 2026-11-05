import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Select, SimpleGrid, Text, Tooltip } from '@mantine/core'
import { AlertTriangle } from 'lucide-react'
import { Badge, Banner, Button, TierBadge } from '@/components/ui'
import {
  Advanced,
  Checkbox,
  Dialog,
  DialogActions,
  MutationError,
  NameField,
  nameError,
  NumberField,
  SecretRefField,
  SelectField,
  TextAreaField,
  TextField,
  checkSecretRef,
  useFieldErrors,
} from '@/components/form'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { quotaAdaptersQuery, resourceListQuery } from '@/lib/api/queries'
import { useSaveResource } from '@/lib/api/resources'
import type { AccountRes, QuotaAdapter, QuotaMonitorRes, QuotaMonitorSpec, Tier } from '@/lib/api/types'
import c from '@/features/config/config.module.css'
import q from './quota.module.css'

type SecretName = 'api_key' | 'management_key' | 'access_token' | 'service_account'
type RefField = 'api_key_ref' | 'management_key_ref' | 'access_token_ref' | 'service_account_ref'
type Mode = 'keep' | 'stored' | 'reference' | 'none'

const secretFields: { name: SecretName; ref: RefField; label: string }[] = [
  { name: 'api_key', ref: 'api_key_ref', label: 'API key' },
  { name: 'management_key', ref: 'management_key_ref', label: 'Management or admin key' },
  { name: 'access_token', ref: 'access_token_ref', label: 'OAuth access token' },
  { name: 'service_account', ref: 'service_account_ref', label: 'Service account JSON' },
]

/** Parses the options editor; returns an error message for invalid JSON. */
export function parseOptions(text: string): { value?: Record<string, unknown>; error?: string } {
  if (text.trim() === '') return { value: {} }
  try {
    const v: unknown = JSON.parse(text)
    if (v === null || typeof v !== 'object' || Array.isArray(v)) return { error: 'Options must be a JSON object.' }
    return { value: v as Record<string, unknown> }
  } catch (e) {
    return { error: `Invalid JSON: ${e instanceof Error ? e.message : 'parse error'}` }
  }
}

/** Groups of adapters by how they read the provider. */
const tierGroup: Record<string, string> = {
  official: 'Official APIs',
  partial: 'Official APIs',
  local: 'Official APIs',
  unofficial: 'Unofficial (undocumented endpoints)',
  client_reported: 'Reported by the tool',
  demo: 'Demo',
}

/** Adapter options grouped by tier and sorted by name. */
export function adapterOptions(list: QuotaAdapter[]): { group: string; items: { value: string; label: string }[] }[] {
  const order = ['Official APIs', 'Reported by the tool', 'Unofficial (undocumented endpoints)', 'Demo']
  const groups = new Map<string, { value: string; label: string }[]>()
  for (const a of [...list].sort((x, y) => x.display_name.localeCompare(y.display_name))) {
    const g = tierGroup[a.tier] ?? 'Other'
    groups.set(g, [...(groups.get(g) ?? []), { value: a.id, label: a.display_name }])
  }
  return [...groups.entries()].sort(([a], [b]) => order.indexOf(a) - order.indexOf(b)).map(([group, items]) => ({ group, items }))
}

const capPhrase: Record<string, string> = {
  quota_remaining: 'remaining quota',
  quota_used: 'quota used',
  quota_limit: 'quota limits',
  model_specific_quota: 'per-model quotas',
  fixed_reset_timestamp: 'exact reset times',
  rolling_window: 'rolling windows',
  balance: 'account balance',
  spend: 'spend',
  usage: 'usage',
  account_identity: 'which account it signed in as',
}

/** One sentence for what an adapter reports: "Reports remaining quota, per-model quotas and exact reset times." */
export function capabilitySentence(a: QuotaAdapter): string {
  const caps = Object.entries(a.capabilities).filter(([, v]) => v.status !== 'unsupported')
  if (caps.length === 0) return 'Reports what the provider exposes.'
  const words = caps.map(([k, v]) => `${capPhrase[k] ?? k.replaceAll('_', ' ')}${v.status === 'partial' ? ' (partly)' : ''}`)
  const list = words.length > 1 ? `${words.slice(0, -1).join(', ')} and ${words[words.length - 1]}` : words[0]
  return `Reports ${list}.`
}

function AdapterInfo({ a }: { a: QuotaAdapter }) {
  const caps = Object.entries(a.capabilities).filter(([, v]) => v.status !== 'unsupported')
  return (
    <div className={c.row}>
      <div className={q.badges}>
        <strong>{a.display_name}</strong>
        {a.tier !== 'official' && <TierBadge tier={a.tier as Tier} />}
        {a.push_only && <Badge tone="info">Push only</Badge>}
      </div>
      <Tooltip
        multiline
        w={320}
        label={caps.map(([k, v]) => `${k.replaceAll('_', ' ')}${v.status === 'partial' ? ' (partial)' : ''}${v.note ? `: ${v.note}` : ''}`).join(' · ')}
        disabled={caps.length === 0}
      >
        <Text size="sm">{capabilitySentence(a)}</Text>
      </Tooltip>
      <Text size="xs" c="dimmed">
        Checks every {Math.round(a.default_poll_interval_secs / 60)} min by default · Needs {a.credential_types.join(' or ') || 'no credential'}
      </Text>
      {a.limitations.length > 0 && (
        <ul className={q.list}>
          {a.limitations.map((l) => (
            <li key={l}>{l}</li>
          ))}
        </ul>
      )}
    </div>
  )
}

/** Create or edit a quota monitor. Credentials are write-only. */
export function MonitorDialog({ monitor, onClose }: { monitor: QuotaMonitorRes | null; onClose: () => void }) {
  const adapters = useQuery(quotaAdaptersQuery)
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const init = monitor?.spec
  const [name, setName] = useState(monitor?.name ?? '')
  const [adapter, setAdapter] = useState(init?.adapter ?? '')
  const [accountID, setAccountID] = useState(init?.account_id ?? '')
  const [enabled, setEnabled] = useState(init?.enabled ?? true)
  // Shown in minutes; empty means the provider's default.
  const [pollInterval, setPollInterval] = useState<number | null>(init?.poll_interval_secs ? Math.round(init.poll_interval_secs / 60) : null)
  const [below, setBelow] = useState<number | null>(Math.round((init?.exhausted_below ?? 0) * 100))
  const [staleAfter, setStaleAfter] = useState<number | null>(init?.stale_after_secs ? Math.round(init.stale_after_secs / 60) : null)
  const [scope, setScope] = useState(init?.shared_scope_id ?? '')
  const [ack, setAck] = useState(init?.acknowledge_terms_risk ?? false)
  const [optText, setOptText] = useState(init?.options && Object.keys(init.options).length ? JSON.stringify(init.options, null, 2) : '')
  const initMode = (s: SecretName, r: RefField): Mode => (init?.[r] ? 'reference' : monitor?.secrets?.[s]?.source === 'stored' ? 'keep' : monitor ? 'none' : 'none')
  const [modes, setModes] = useState<Record<SecretName, Mode>>(() =>
    Object.fromEntries(secretFields.map((s) => [s.name, initMode(s.name, s.ref)])) as Record<SecretName, Mode>,
  )
  const [values, setValues] = useState<Record<SecretName, string>>({ api_key: '', management_key: '', access_token: '', service_account: '' })
  const [refs, setRefs] = useState<Record<RefField, string>>({
    api_key_ref: init?.api_key_ref ?? '',
    management_key_ref: init?.management_key_ref ?? '',
    access_token_ref: init?.access_token_ref ?? '',
    service_account_ref: init?.service_account_ref ?? '',
  })
  const save = useSaveResource<QuotaMonitorSpec>('quota-monitors')
  const errs = useFieldErrors(save.error)
  // A stale server error goes away as soon as the user changes something.
  const edit =
    <T,>(f: (v: T) => void) =>
    (v: T) => {
      if (save.error) save.reset()
      f(v)
    }
  const list = adapters.data?.items ?? []
  const selected = list.find((a) => a.id === adapter)
  const opts = parseOptions(optText)
  const needsAck = selected?.requires_terms_ack ?? false

  const submit = () => {
    const spec: QuotaMonitorSpec = {
      adapter,
      account_id: accountID,
      options: opts.value ?? {},
      enabled,
      poll_interval_secs: (pollInterval ?? 0) * 60,
      exhausted_below: (below ?? 0) / 100,
      stale_after_secs: (staleAfter ?? 0) * 60,
      shared_scope_id: scope.trim(),
      acknowledge_terms_risk: needsAck ? ack : false,
      api_key_ref: '',
      management_key_ref: '',
      access_token_ref: '',
      service_account_ref: '',
    }
    const secrets: Record<string, string> = {}
    for (const s of secretFields) {
      const m = modes[s.name]
      if (m === 'reference') spec[s.ref] = refs[s.ref].trim()
      if (m === 'stored' && values[s.name]) secrets[s.name] = values[s.name]
      if (m === 'none' && monitor?.secrets?.[s.name]?.source === 'stored') secrets[s.name] = ''
    }
    save.mutate({ existing: monitor ?? undefined, body: { name, spec, secrets: Object.keys(secrets).length ? secrets : undefined } }, { onSuccess: onClose })
  }

  const accountItems = accounts.data?.items ?? []
  const credential = (sf: (typeof secretFields)[number]) => {
    const info = monitor?.secrets?.[sf.name]
    const m = modes[sf.name]
    return (
      <div key={sf.name} className={c.row}>
        <SelectField<Mode>
          label={sf.label}
          value={m}
          onChange={edit((v: Mode) => setModes({ ...modes, [sf.name]: v }))}
          options={[
            ...(info?.source === 'stored' ? [{ value: 'keep' as Mode, label: `Keep the saved one (${info.fingerprint ?? 'set'})` }] : []),
            { value: 'stored', label: info?.source === 'stored' ? 'Replace it' : 'Paste it (stored encrypted)' },
            { value: 'reference', label: 'Read from an environment variable or file' },
            { value: 'none', label: info?.source === 'stored' ? 'Remove it' : 'Not needed' },
          ]}
        />
        {m === 'stored' && (
          <TextField label={`${sf.label} value`} type="password" autoComplete="off" value={values[sf.name]} onChange={edit((v: string) => setValues({ ...values, [sf.name]: v }))}
            hint="Never shown again after saving." />
        )}
        {m === 'reference' && (
          <SecretRefField
            label={`${sf.label} reference`}
            value={refs[sf.ref]}
            onChange={edit((v: string) => setRefs({ ...refs, [sf.ref]: v }))}
            example={sf.name.toUpperCase()}
            error={errs[sf.ref]}
          />
        )}
      </div>
    )
  }
  const refProblem = secretFields.some((sf) => modes[sf.name] === 'reference' && !!checkSecretRef(refs[sf.ref].trim()))
  const primary = secretFields.filter((sf) => sf.name === 'api_key' || modes[sf.name] !== 'none')
  const others = secretFields.filter((sf) => !primary.includes(sf))
  return (
    <Dialog open wide title={monitor ? `Edit quota tracker ${monitor.name}` : 'Track a provider quota'} onClose={onClose}>
      <TerraformEditWarning r={monitor} />
      <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
        <Select
          label="Provider API"
          placeholder={adapters.isLoading ? 'Loading…' : 'Choose what to track'}
          data={adapterOptions(list)}
          value={adapter || null}
          onChange={(v) => edit(setAdapter)(v ?? '')}
          searchable
          required
          error={errs.adapter}
          comboboxProps={{ withinPortal: true }}
        />
        <NameField label="Name" value={name} onChange={edit(setName)} locked={!!monitor} error={errs.name} />
        <SelectField label="Routes for" value={accountID} onChange={edit(setAccountID)}
          hint="When a limit is reached, routing skips this account until the window resets."
          options={[{ value: '', label: 'No account (watch only)' }, ...accountItems.map((a) => ({ value: a.id, label: a.name }))]} />
      </SimpleGrid>
      {selected && <AdapterInfo a={selected} />}
      {needsAck && (
        <Banner tone="warning" icon={AlertTriangle}>
          <strong>Unofficial adapter.</strong> It reads undocumented provider endpoints that may change or break without notice, and using
          subscription credentials this way may conflict with the provider&apos;s terms.
          <div style={{ marginTop: 8 }}>
            <Checkbox label="I understand and accept these risks for this tracker" checked={ack} onChange={edit(setAck)} />
          </div>
        </Banner>
      )}
      {primary.map(credential)}
      <Advanced>
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
          <NumberField label="Check every" unit="min" min={1} max={1440} value={pollInterval} onChange={edit(setPollInterval)} placeholder="Provider default" w="100%" error={errs.poll_interval_secs} />
          <NumberField label="Mark out of date after" unit="min" min={1} max={10080} value={staleAfter} onChange={edit(setStaleAfter)} placeholder="3 checks" w="100%" error={errs.stale_after_secs} />
          <NumberField label="Treat as used up at" unit="% left" min={0} max={100} value={below} onChange={edit(setBelow)} hint="0 means only when fully used." w="100%" error={errs.exhausted_below} />
          <TextField label="Shared scope" value={scope} onChange={edit(setScope)} placeholder="Optional" hint="Trackers reading the same provider quota share one id so it is not counted twice." error={errs.shared_scope_id} />
        </SimpleGrid>
        {others.map(credential)}
        <TextAreaField
          label="Adapter options (JSON object)"
          mono
          minRows={4}
          value={optText}
          onChange={edit(setOptText)}
          placeholder='{"token_source": "env"}'
          error={opts.error ?? errs.options}
          hint="Non-secret settings such as base URL, organization id or token_source. See the adapter documentation."
        />
        <Checkbox label="Tracking on" checked={enabled} onChange={edit(setEnabled)} />
      </Advanced>
      <MutationError error={save.error} />
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button
          variant="primary"
          busy={save.isPending}
          disabled={!name || !!nameError(name) || !adapter || !!opts.error || (needsAck && !ack) || refProblem}
          onClick={submit}
        >
          {monitor ? 'Save changes' : 'Start tracking'}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
