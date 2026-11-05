import { useState } from 'react'
import { Anchor, Group, SegmentedControl, Stack, Switch, Text } from '@mantine/core'
import { Button } from '@/components/ui'
import { Advanced, Checkbox, Dialog, DialogActions, MutationError, NameField, NumberField, TextField, useFieldErrors } from '@/components/form'
import { KeyValueEditor } from '@/components/KeyValueEditor'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { useSaveResource } from '@/lib/api/resources'
import type { DestinationRes, DestinationSpec } from '@/lib/api/types'
import { title } from '@/ui/glossary'
import { defaultDestination } from './events'
import { FilterEditor } from './FilterEditor'
import { SecretInput, secretChanges } from './SecretInput'

const reservedHeader = (k: string) => k.toLowerCase() === 'authorization' || k.toLowerCase().startsWith('x-harbor-')

/** Problem with a file-only reference (Pulse credentials, CA bundle), or undefined. */
function fileRefError(v: string): string | undefined {
  if (!v) return undefined
  return /^file:\/\S+$/.test(v.trim()) ? undefined : 'Use file:/absolute/path to a file on the server.'
}

/** Create or edit an event relay (signed webhook or Pulse). */
export function DestinationDialog({ destination, onClose }: { destination: DestinationRes | null; onClose: () => void }) {
  const [name, setName] = useState(destination?.name ?? '')
  const [spec, setSpec] = useState<DestinationSpec>(() => ({ ...defaultDestination(), ...destination?.spec }))
  const [filterValid, setFilterValid] = useState(true)
  const [headersValid, setHeadersValid] = useState(true)
  const [signing, setSigning] = useState('')
  const [clearSigning, setClearSigning] = useState(false)
  const [previous, setPrevious] = useState('')
  const [clearPrevious, setClearPrevious] = useState(false)
  const [rotating, setRotating] = useState(destination?.secrets?.previous_signing_secret?.source === 'stored')
  const [creds, setCreds] = useState('')
  const [clearCreds, setClearCreds] = useState(false)
  const [credsMode, setCredsMode] = useState<'paste' | 'file'>(destination?.spec.creds_ref ? 'file' : 'paste')
  const [submitted, setSubmitted] = useState(false)
  const save = useSaveResource<DestinationSpec>('event-destinations')
  const errs = useFieldErrors(save.error)
  const err = (loc: string) => errs[`body.spec.${loc}`]
  const set = <K extends keyof DestinationSpec>(k: K, v: DestinationSpec[K]) => setSpec((s) => ({ ...s, [k]: v }))
  const sec = destination?.secrets ?? {}

  const webhook = spec.type === 'webhook'
  const url = webhook ? spec.webhook_url ?? '' : spec.pulse_url ?? ''
  const urlProblem = (() => {
    if (!url) return webhook ? 'Enter the URL that receives the events.' : 'Enter the Pulse server URL.'
    if (!webhook) return /^(nats|tls):\/\/[^/\s]+/.test(url) ? undefined : 'Use a nats:// or tls:// URL.'
    if (/^https:\/\/[^/\s]+/.test(url)) return undefined
    if (/^http:\/\/[^/\s]+/.test(url)) return spec.allow_private_network ? undefined : 'Plain http works only with “Receiver is on a private network” turned on.'
    return 'Use a full https:// URL.'
  })()
  const badHeaders = Object.keys(spec.headers ?? {}).filter(reservedHeader)
  const credsRefProblem = credsMode === 'file' ? fileRefError(spec.creds_ref ?? '') : undefined
  const caProblem = fileRefError(spec.ca_ref ?? '')
  const problems = [!name, urlProblem, !filterValid, !headersValid, badHeaders.length > 0, credsRefProblem, caProblem].filter(Boolean)
  const show = (p: string | undefined, hasValue: boolean) => (submitted || hasValue ? p : undefined)

  const submit = () => {
    setSubmitted(true)
    if (problems.length > 0) return
    const timeout = spec.timeout_seconds ?? 10
    const body: DestinationSpec = webhook
      ? { type: 'webhook', enabled: spec.enabled, filter: spec.filter, webhook_url: spec.webhook_url?.trim(), allow_private_network: spec.allow_private_network, timeout_seconds: timeout, headers: spec.headers ?? {}, max_attempts: spec.max_attempts, max_age_hours: spec.max_age_hours }
      : { type: 'pulse', enabled: spec.enabled, filter: spec.filter, pulse_url: spec.pulse_url?.trim(), pulse_source: spec.pulse_source, creds_ref: credsMode === 'file' ? spec.creds_ref : '', ca_ref: spec.ca_ref, timeout_seconds: timeout, max_attempts: spec.max_attempts, max_age_hours: spec.max_age_hours }
    const secrets = secretChanges(webhook
      ? [{ name: 'signing_secret', value: signing, clear: clearSigning }, { name: 'previous_signing_secret', value: previous, clear: clearPrevious }]
      : [{ name: 'creds', value: credsMode === 'paste' ? creds : '', clear: clearCreds || (credsMode === 'file' && sec.creds?.source === 'stored') }])
    save.mutate({ existing: destination ?? undefined, body: { name, spec: body, secrets } }, { onSuccess: onClose })
  }

  return (
    <Dialog open wide title={destination ? `Edit ${title('eventRelay').toLowerCase()} ${destination.name}` : `Add ${title('eventRelay').toLowerCase()}`} onClose={onClose}>
      <TerraformEditWarning r={destination} />
      <Group justify="space-between" align="flex-start" gap="md" wrap="wrap">
        <div style={{ flex: '1 1 320px', minWidth: 0 }}>
          <NameField value={name} onChange={setName} locked={!!destination} autoFocus={!destination} error={show(!name ? 'Enter a short name.' : undefined, false) ?? errs['name']} />
        </div>
        <Switch mt={{ base: 0, sm: 30 }} label="Relay on" checked={spec.enabled} onChange={(ev) => set('enabled', ev.currentTarget.checked)} />
      </Group>
      {!destination && (
        <SegmentedControl
          aria-label="Relay type"
          value={spec.type}
          onChange={(v) => set('type', v as DestinationSpec['type'])}
          data={[
            { value: 'webhook', label: 'Signed webhook' },
            { value: 'pulse', label: 'Pulse (NATS)' },
          ]}
          style={{ alignSelf: 'flex-start' }}
        />
      )}
      <Text size="sm" c="dimmed">
        {webhook ? 'Each matching event is POSTed as JSON to your endpoint, signed with HMAC-SHA256.' : 'Each matching event is published as a CloudEvent to a Pulse (NATS JetStream) server.'}
      </Text>

      {webhook ? (
        <Stack gap={6}>
          <TextField label="Webhook URL" value={spec.webhook_url ?? ''} onChange={(v) => set('webhook_url', v)} placeholder="https://hooks.example.com/harbor" required
            error={show(urlProblem, !!url) ?? err('webhook_url')} />
          <Checkbox label="Receiver is on a private network" checked={!!spec.allow_private_network} onChange={(v) => set('allow_private_network', v)}
            description="Allows plain http and loopback or LAN addresses. Cloud metadata addresses stay blocked." />
        </Stack>
      ) : (
        <Group gap="md" align="flex-start" wrap="wrap">
          <div style={{ flex: '2 1 320px' }}>
            <TextField label="Pulse server URL" value={spec.pulse_url ?? ''} onChange={(v) => set('pulse_url', v)} placeholder="tls://pulse.example.com:4222" required
              error={show(urlProblem, !!url) ?? err('pulse_url')} />
          </div>
          <div style={{ flex: '1 1 200px' }}>
            <TextField label="Source name" value={spec.pulse_source ?? ''} onChange={(v) => set('pulse_source', v)} placeholder="harbor-pilot" hint="Sent as ce-source." />
          </div>
        </Group>
      )}

      <Stack gap="sm">
        <Text size="sm" fw={600}>
          Which events
        </Text>
        <FilterEditor value={spec.filter ?? { events: [], min_severity: 'info' }} errors={{ events: err('filter.events') }}
          onChange={(fl, ok) => { set('filter', fl); setFilterValid(ok) }} />
      </Stack>

      {webhook ? (
        <Stack gap={6}>
          <SecretInput label="Signing secret" info={sec.signing_secret} value={signing} onChange={setSigning} clear={clearSigning} onClear={setClearSigning}
            hint="Receivers verify the X-Harbor-Signature header with this secret." error={err('signing_secret')} />
          {rotating ? (
            <SecretInput label="Previous signing secret" info={sec.previous_signing_secret} value={previous} onChange={setPrevious} clear={clearPrevious} onClear={setClearPrevious}
              hint="Requests are signed with both secrets until you remove this one, so receivers can switch without downtime." />
          ) : (
            <Anchor component="button" type="button" size="xs" onClick={() => setRotating(true)} style={{ alignSelf: 'flex-start' }}>
              Rotate the signing secret…
            </Anchor>
          )}
        </Stack>
      ) : (
        <Stack gap="sm">
          <Stack gap={4}>
            <Text size="sm" fw={500}>
              Credentials
            </Text>
            <SegmentedControl
              aria-label="Credentials source"
              value={credsMode}
              onChange={(v) => setCredsMode(v as 'paste' | 'file')}
              data={[
                { value: 'paste', label: 'Paste the .creds file' },
                { value: 'file', label: 'File on the server' },
              ]}
              style={{ alignSelf: 'flex-start' }}
            />
          </Stack>
          {credsMode === 'paste' ? (
            <SecretInput label="Credentials file content" multiline info={sec.creds} value={creds} onChange={setCreds} clear={clearCreds} onClear={setClearCreds}
              hint="Stored encrypted; never shown again." />
          ) : (
            <TextField label="Credentials file" value={spec.creds_ref ?? ''} onChange={(v) => set('creds_ref', v)} placeholder="file:/etc/harbor/pulse.creds"
              hint="A file under the server's secret roots." error={show(credsRefProblem, !!spec.creds_ref) ?? err('creds_ref')} />
          )}
          <TextField label="CA bundle" value={spec.ca_ref ?? ''} onChange={(v) => set('ca_ref', v)} placeholder="file:/etc/harbor/pulse-ca.pem"
            hint="Optional, for servers with a private certificate authority." error={caProblem ?? err('ca_ref')} />
        </Stack>
      )}

      <Advanced label="Retries and headers">
        <Group gap="md" align="flex-start" wrap="wrap">
          <NumberField label="Timeout" unit="s" value={spec.timeout_seconds ?? 10} onChange={(v) => set('timeout_seconds', v ?? 10)} min={1} max={60} w={160} error={err('timeout_seconds')} />
          <NumberField label="Give up after" unit="attempts" value={spec.max_attempts ?? 50} onChange={(v) => set('max_attempts', v ?? 50)} min={1} max={1000} w={200} error={err('max_attempts')} />
          <NumberField label="or after" unit="hours" value={spec.max_age_hours ?? 24} onChange={(v) => set('max_age_hours', v ?? 24)} min={1} max={720} w={180} error={err('max_age_hours')} />
        </Group>
        <Text size="xs" c="dimmed">
          Failed deliveries are retried with growing pauses, then marked failed. Failed deliveries can be retried from the relay's page.
        </Text>
        {webhook && (
          <KeyValueEditor label="Extra headers" value={spec.headers ?? {}} hint="Non-secret, one Name=value per line. Authorization and X-Harbor-* are reserved."
            error={badHeaders.length > 0 ? `Remove ${badHeaders.join(', ')}: credentials belong in the signing secret, not headers.` : err('headers')}
            onChange={(v, ok) => { set('headers', v); setHeadersValid(ok) }} />
        )}
      </Advanced>
      <MutationError error={save.error} />
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} onClick={submit}>
          {destination ? 'Save relay' : 'Add relay'}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
