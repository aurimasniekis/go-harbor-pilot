import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { AlertTriangle } from 'lucide-react'
import { Badge, Banner, Button, TierBadge } from '@/components/ui'
import { Advanced, Checkbox, Dialog, Field, MutationError, SelectField, TextField, formStyles as f } from '@/components/form'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { quotaAdaptersQuery, resourceListQuery } from '@/lib/api/queries'
import { useSaveResource } from '@/lib/api/resources'
import type { AccountRes, QuotaAdapter, QuotaMonitorRes, QuotaMonitorSpec, Tier } from '@/lib/api/types'
import { toInt } from '@/features/config/constants'
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

function AdapterInfo({ a }: { a: QuotaAdapter }) {
  const caps = Object.entries(a.capabilities).filter(([, v]) => v.status !== 'unsupported')
  return (
    <div className={c.row}>
      <div className={q.badges}>
        <strong>{a.display_name}</strong>
        <TierBadge tier={a.tier as Tier} />
        {a.push_only && <Badge tone="info">Push only</Badge>}
        <span className="muted" style={{ fontSize: 'var(--text-xs)' }}>default poll every {Math.round(a.default_poll_interval_secs / 60)} min</span>
      </div>
      <div className={q.small}>
        <span className="muted">Credentials: </span>
        {a.credential_types.join('; ') || 'none'}
      </div>
      {caps.length > 0 && (
        <div className={q.badges} aria-label="Capabilities">
          {caps.map(([k, v]) => (
            <Badge key={k} tone={v.status === 'supported' ? 'success' : 'warning'} title={v.note || undefined}>
              {k.replaceAll('_', ' ')}
              {v.status === 'partial' ? ' (partial)' : ''}
            </Badge>
          ))}
        </div>
      )}
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
  const [pollInterval, setPollInterval] = useState(init?.poll_interval_secs ? String(Math.round(init.poll_interval_secs / 60)) : '')
  const [below, setBelow] = useState(String(Math.round((init?.exhausted_below ?? 0) * 100)))
  const [staleAfter, setStaleAfter] = useState(init?.stale_after_secs ? String(Math.round(init.stale_after_secs / 60)) : '')
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
      poll_interval_secs: toInt(pollInterval) * 60,
      exhausted_below: Math.min(100, Math.max(0, toInt(below))) / 100,
      stale_after_secs: toInt(staleAfter) * 60,
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
          onChange={(v) => setModes({ ...modes, [sf.name]: v })}
          options={[
            ...(info?.source === 'stored' ? [{ value: 'keep' as Mode, label: `Keep the saved one (${info.fingerprint ?? 'set'})` }] : []),
            { value: 'stored', label: info?.source === 'stored' ? 'Replace it' : 'Paste it (stored encrypted)' },
            { value: 'reference', label: 'Read from an environment variable or file' },
            { value: 'none', label: info?.source === 'stored' ? 'Remove it' : 'Not needed' },
          ]}
        />
        {m === 'stored' && (
          <TextField label={`${sf.label} value`} type="password" autoComplete="off" value={values[sf.name]} onChange={(v) => setValues({ ...values, [sf.name]: v })}
            hint="Never shown again after saving." />
        )}
        {m === 'reference' && (
          <TextField label={`${sf.label} reference`} value={refs[sf.ref]} onChange={(v) => setRefs({ ...refs, [sf.ref]: v })} placeholder="env:NAME or file:/run/secrets/key" />
        )}
      </div>
    )
  }
  const primary = secretFields.filter((sf) => sf.name === 'api_key' || modes[sf.name] !== 'none')
  const others = secretFields.filter((sf) => !primary.includes(sf))
  return (
    <Dialog open wide title={monitor ? `Edit quota tracker ${monitor.name}` : 'Track a provider quota'} onClose={onClose}>
      <TerraformEditWarning r={monitor} />
      <div className={f.grid}>
        <SelectField label="Adapter" value={adapter} onChange={setAdapter} placeholder="Choose the provider API"
          options={[{ value: '', label: adapters.isLoading ? 'Loading…' : 'Choose the provider API' }, ...list.map((a) => ({ value: a.id, label: a.display_name }))]} />
        <TextField label="Name" value={name} onChange={setName} disabled={!!monitor} required placeholder="e.g. claude-team" hint={monitor ? undefined : 'Lowercase; cannot be changed later.'} />
        <SelectField label="Routes for" value={accountID} onChange={setAccountID}
          hint="When a limit is reached, routing skips this account until the window resets."
          options={[{ value: '', label: 'No account (watch only)' }, ...accountItems.map((a) => ({ value: a.id, label: a.name }))]} />
      </div>
      {selected && <AdapterInfo a={selected} />}
      {needsAck && (
        <Banner tone="warning" icon={AlertTriangle}>
          <strong>Unofficial adapter.</strong> It reads undocumented provider endpoints that may change or break without notice, and using
          subscription credentials this way may conflict with the provider&apos;s terms.
          <div style={{ marginTop: 8 }}>
            <Checkbox label="I understand and accept these risks for this monitor" checked={ack} onChange={setAck} />
          </div>
        </Banner>
      )}
      {primary.map(credential)}
      <Advanced>
        <div className={f.grid}>
          <TextField label="Check every" type="number" suffix="min" value={pollInterval} onChange={setPollInterval} placeholder="Provider default" />
          <TextField label="Mark out of date after" type="number" suffix="min" value={staleAfter} onChange={setStaleAfter} placeholder="3 checks" />
          <TextField label="Treat as used up at" type="number" suffix="% left" value={below} onChange={setBelow} hint="0 means only when fully used." />
          <TextField label="Shared scope" value={scope} onChange={setScope} placeholder="Optional" hint="Trackers reading the same provider quota share one id so it is not counted twice." />
        </div>
        {others.map(credential)}
        <Field label="Adapter options (JSON object)" error={opts.error} hint="Non-secret settings such as base URL, organization id or token_source. See the adapter documentation.">
          {(p) => (
            <textarea {...p} className={f.textarea} rows={6} spellCheck={false} value={optText} onChange={(e) => setOptText(e.target.value)} placeholder='{"token_source": "env"}' style={{ fontFamily: 'var(--font-mono)' }} />
          )}
        </Field>
        <Checkbox label="Tracking on" checked={enabled} onChange={setEnabled} />
      </Advanced>
      <MutationError error={save.error} />
      <div className={f.actions}>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} disabled={!name || !adapter || !!opts.error || (needsAck && !ack)} onClick={submit}>
          {monitor ? 'Save changes' : 'Start tracking'}
        </Button>
      </div>
    </Dialog>
  )
}
