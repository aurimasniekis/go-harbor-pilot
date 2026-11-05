import { useState } from 'react'
import { Banner, Button } from '@/components/ui'
import { Checkbox, Dialog, MutationError, SelectField, TextField, formStyles as f, useFieldErrors } from '@/components/form'
import { KeyValueEditor } from '@/components/KeyValueEditor'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { useSaveResource } from '@/lib/api/resources'
import type { DestinationRes, DestinationSpec } from '@/lib/api/types'
import { defaultDestination } from './events'
import { FilterEditor } from './FilterEditor'
import { SecretInput, secretChanges } from './SecretInput'

const reservedHeader = (k: string) => k.toLowerCase() === 'authorization' || k.toLowerCase().startsWith('x-harbor-')

/** Create or edit an event destination (Pulse or signed webhook). */
export function DestinationDialog({ destination, onClose }: { destination: DestinationRes | null; onClose: () => void }) {
  const [name, setName] = useState(destination?.name ?? '')
  const [spec, setSpec] = useState<DestinationSpec>(() => ({ ...defaultDestination(), ...destination?.spec }))
  const [filterValid, setFilterValid] = useState(true)
  const [headersValid, setHeadersValid] = useState(true)
  const [signing, setSigning] = useState('')
  const [clearSigning, setClearSigning] = useState(false)
  const [previous, setPrevious] = useState('')
  const [clearPrevious, setClearPrevious] = useState(false)
  const [creds, setCreds] = useState('')
  const [clearCreds, setClearCreds] = useState(false)
  const save = useSaveResource<DestinationSpec>('event-destinations')
  const errs = useFieldErrors(save.error)
  const err = (loc: string) => errs[`body.spec.${loc}`]
  const set = <K extends keyof DestinationSpec>(k: K, v: DestinationSpec[K]) => setSpec((s) => ({ ...s, [k]: v }))
  const sec = destination?.secrets ?? {}

  const webhook = spec.type === 'webhook'
  const url = webhook ? spec.webhook_url ?? '' : spec.pulse_url ?? ''
  const urlOK = webhook ? /^https?:\/\/[^/\s]+/.test(url) && (url.startsWith('https://') || !!spec.allow_private_network) : /^(nats|tls):\/\/[^/\s]+/.test(url)
  const badHeaders = Object.keys(spec.headers ?? {}).filter(reservedHeader)
  const timeout = spec.timeout_seconds ?? 10
  const numbersOK = timeout >= 1 && timeout <= 60 && (spec.max_attempts ?? 50) >= 1 && (spec.max_attempts ?? 50) <= 1000 && (spec.max_age_hours ?? 24) >= 1 && (spec.max_age_hours ?? 24) <= 720
  const valid = !!name && urlOK && filterValid && headersValid && badHeaders.length === 0 && numbersOK

  const submit = () => {
    const body: DestinationSpec = webhook
      ? { type: 'webhook', enabled: spec.enabled, filter: spec.filter, webhook_url: spec.webhook_url, allow_private_network: spec.allow_private_network, timeout_seconds: timeout, headers: spec.headers ?? {}, max_attempts: spec.max_attempts, max_age_hours: spec.max_age_hours }
      : { type: 'pulse', enabled: spec.enabled, filter: spec.filter, pulse_url: spec.pulse_url, pulse_source: spec.pulse_source, creds_ref: spec.creds_ref, ca_ref: spec.ca_ref, timeout_seconds: timeout, max_attempts: spec.max_attempts, max_age_hours: spec.max_age_hours }
    const secrets = secretChanges(webhook
      ? [{ name: 'signing_secret', value: signing, clear: clearSigning }, { name: 'previous_signing_secret', value: previous, clear: clearPrevious }]
      : [{ name: 'creds', value: creds, clear: clearCreds }])
    save.mutate({ existing: destination ?? undefined, body: { name, spec: body, secrets } }, { onSuccess: onClose })
  }

  const urlError = (() => {
    if (!url) return err(webhook ? 'webhook_url' : 'pulse_url')
    if (urlOK) return err(webhook ? 'webhook_url' : 'pulse_url')
    if (!webhook) return 'Use a nats:// or tls:// URL.'
    if (url.startsWith('http://')) return 'Plain http needs “Allow private network” (for receivers on this host or LAN).'
    return 'Use an absolute https:// URL.'
  })()

  return (
    <Dialog open wide title={destination ? `Edit event destination ${destination.name}` : 'Add event destination'} onClose={onClose}>
      <TerraformEditWarning r={destination} />
      <div className={f.grid}>
        <TextField label="Name" value={name} onChange={setName} disabled={!!destination} required error={errs['body.name']} />
        <SelectField<DestinationSpec['type']> label="Type" value={spec.type} onChange={(v) => set('type', v)} disabled={!!destination}
          hint={webhook ? 'HTTPS POST of each event as JSON, signed with HMAC-SHA256.' : 'Publishes CloudEvents to a Pulse (NATS JetStream) server.'}
          options={[{ value: 'webhook', label: 'Signed webhook' }, { value: 'pulse', label: 'Pulse relay' }]} />
        {webhook ? (
          <TextField label="Webhook URL" value={spec.webhook_url ?? ''} onChange={(v) => set('webhook_url', v)} placeholder="https://hooks.example.com/harbor" required error={urlError} />
        ) : (
          <>
            <TextField label="Pulse server URL" value={spec.pulse_url ?? ''} onChange={(v) => set('pulse_url', v)} placeholder="tls://pulse.example.com:4222" required error={urlError} />
            <TextField label="Source (ce-source)" value={spec.pulse_source ?? ''} onChange={(v) => set('pulse_source', v)} placeholder="harbor-pilot" />
          </>
        )}
        <TextField label="Timeout (seconds, 1–60)" type="number" value={String(timeout)} onChange={(v) => set('timeout_seconds', Number(v))} />
        <TextField label="Dead-letter after attempts" type="number" value={String(spec.max_attempts ?? 50)} onChange={(v) => set('max_attempts', Number(v))}
          hint="Failed deliveries retry with backoff, then move to dead letters." />
        <TextField label="Dead-letter after age (hours)" type="number" value={String(spec.max_age_hours ?? 24)} onChange={(v) => set('max_age_hours', Number(v))}
          error={numbersOK ? undefined : 'Timeout 1–60 s, attempts 1–1000, age 1–720 hours.'} />
      </div>
      <Checkbox label="Enabled" checked={spec.enabled} onChange={(v) => set('enabled', v)} />
      {webhook && (
        <Checkbox label="Allow private network (loopback and LAN receivers; plain http allowed; cloud metadata stays blocked)" checked={!!spec.allow_private_network}
          onChange={(v) => set('allow_private_network', v)} />
      )}

      <FilterEditor value={spec.filter ?? { events: [], min_severity: 'info' }} errors={{ events: err('filter.events') }}
        onChange={(fl, ok) => { set('filter', fl); setFilterValid(ok) }} legend="Which events to relay" />

      {webhook ? (
        <>
          <KeyValueEditor label="Extra headers (non-secret)" value={spec.headers ?? {}} hint="One Name=value per line. Authorization and X-Harbor-* are reserved."
            onChange={(v, ok) => { set('headers', v); setHeadersValid(ok) }} />
          {(badHeaders.length > 0 || err('headers')) && (
            <Banner tone="error">{badHeaders.length > 0 ? `Remove ${badHeaders.join(', ')}: credentials belong in the signing secret, not headers.` : err('headers')}</Banner>
          )}
          <fieldset className={f.grid} style={{ border: 'none', padding: 0, margin: 0 }}>
            <SecretInput label="Signing secret" info={sec.signing_secret} value={signing} onChange={setSigning} clear={clearSigning} onClear={setClearSigning}
              hint="Each request carries X-Harbor-Signature: t=<unix>,v1=<hex>, an HMAC-SHA256 of “<t>.<body>”." />
            <SecretInput label="Previous signing secret" info={sec.previous_signing_secret} value={previous} onChange={setPrevious} clear={clearPrevious} onClear={setClearPrevious}
              hint="Rotation: move the old secret here and set a new signing secret. Requests carry one v1= signature per secret until receivers switch; then remove this one." />
          </fieldset>
        </>
      ) : (
        <div className={f.grid}>
          <SecretInput label="Credentials file" multiline info={sec.creds} value={creds} onChange={setCreds} clear={clearCreds} onClear={setClearCreds}
            hint="Paste the .creds file content, or use a file reference instead." />
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <TextField label="Credentials file reference" value={spec.creds_ref ?? ''} onChange={(v) => set('creds_ref', v)} placeholder="file:/etc/harbor/pulse.creds" error={err('creds_ref')} />
            <TextField label="CA bundle reference" value={spec.ca_ref ?? ''} onChange={(v) => set('ca_ref', v)} placeholder="file:/etc/harbor/pulse-ca.pem" error={err('ca_ref')} />
          </div>
        </div>
      )}
      <MutationError error={save.error} />
      <div className={f.actions}>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} disabled={!valid} onClick={submit}>Save destination</Button>
      </div>
    </Dialog>
  )
}
