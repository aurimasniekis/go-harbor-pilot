import { useId, useState } from 'react'
import { Button, uiStyles } from '@/components/ui'
import { Checkbox, Dialog, Field, MutationError, SelectField, TextField, formStyles as f, useFieldErrors } from '@/components/form'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { useSaveResource } from '@/lib/api/resources'
import type { ChannelRes, ChannelSpec } from '@/lib/api/types'
import { channelTypes, defaultChannel, hhmmRE } from './events'
import { SecretInput, secretChanges } from './SecretInput'
import e from './events.module.css'

function timezones(): string[] {
  try {
    return (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf?.('timeZone') ?? []
  } catch {
    return []
  }
}

const tzNames = timezones()

/** Create or edit a notification channel. */
export function ChannelDialog({ channel, onClose }: { channel: ChannelRes | null; onClose: () => void }) {
  const [name, setName] = useState(channel?.name ?? '')
  const [spec, setSpec] = useState<ChannelSpec>(() => {
    const d = defaultChannel()
    return { ...d, ...channel?.spec, quiet_hours: { ...d.quiet_hours!, ...channel?.spec.quiet_hours } }
  })
  const [secret, setSecret] = useState('')
  const [clearSecret, setClearSecret] = useState(false)
  const tzList = useId()
  const save = useSaveResource<ChannelSpec>('notification-channels')
  const errs = useFieldErrors(save.error)
  const err = (loc: string) => errs[`body.spec.${loc}`]
  const set = <K extends keyof ChannelSpec>(k: K, v: ChannelSpec[K]) => setSpec((s) => ({ ...s, [k]: v }))
  const qh = spec.quiet_hours!
  const setQH = (p: Partial<typeof qh>) => setSpec((s) => ({ ...s, quiet_hours: { ...qh, ...p } }))
  const info = channelTypes.find((t) => t.value === spec.type)!
  const stored = channel?.secrets?.secret?.source === 'stored'

  const webhookOK = spec.type !== 'webhook' || /^https:\/\/\S+/.test(spec.webhook_url ?? '') || (!!spec.allow_private_network && /^http:\/\/\S+/.test(spec.webhook_url ?? ''))
  const chatOK = spec.type !== 'telegram' || !!spec.telegram_chat_id?.trim()
  const qhOK = !qh.enabled || (hhmmRE.test(qh.start ?? '') && hhmmRE.test(qh.end ?? ''))
  const digest = spec.digest_minutes ?? 0
  const rate = spec.rate_per_minute ?? 20
  const numbersOK = digest >= 0 && digest <= 1440 && rate >= 1 && rate <= 600
  const refOK = !spec.secret_ref || /^(env|file):.+/.test(spec.secret_ref)
  const secretOK = spec.type === 'webhook' || stored || !!secret || !!spec.secret_ref
  const valid = !!name && webhookOK && chatOK && qhOK && numbersOK && refOK && secretOK

  const submit = () => {
    const body: ChannelSpec = {
      type: spec.type,
      enabled: spec.enabled,
      timezone: spec.timezone ?? '',
      quiet_hours: qh,
      digest_minutes: digest,
      rate_per_minute: rate,
      secret_ref: spec.secret_ref ?? '',
      api_base_url: spec.api_base_url ?? '',
      allow_private_network: !!spec.allow_private_network,
      ...(spec.type === 'telegram' ? { telegram_chat_id: spec.telegram_chat_id } : {}),
      ...(spec.type === 'webhook' ? { webhook_url: spec.webhook_url } : {}),
    }
    save.mutate({ existing: channel ?? undefined, body: { name, spec: body, secrets: secretChanges([{ name: 'secret', value: secret, clear: clearSecret }]) } }, { onSuccess: onClose })
  }

  return (
    <Dialog open wide title={channel ? `Edit channel ${channel.name}` : 'Add notification channel'} onClose={onClose}>
      <TerraformEditWarning r={channel} />
      <div className={f.grid}>
        <TextField label="Name" value={name} onChange={setName} disabled={!!channel} required error={errs['body.name']} />
        <SelectField<ChannelSpec['type']> label="Type" value={spec.type} onChange={(v) => set('type', v)} disabled={!!channel}
          options={channelTypes.map((t) => ({ value: t.value, label: t.label }))} />
        {spec.type === 'telegram' && (
          <TextField label="Chat ID" value={spec.telegram_chat_id ?? ''} onChange={(v) => set('telegram_chat_id', v)} placeholder="-1001234567890" required
            hint="The chat, group or channel the bot posts to." error={chatOK ? err('telegram_chat_id') : 'A chat ID is required.'} />
        )}
        {spec.type === 'webhook' && (
          <TextField label="Webhook URL" value={spec.webhook_url ?? ''} onChange={(v) => set('webhook_url', v)} placeholder="https://alerts.example.com/harbor" required
            error={webhookOK || !spec.webhook_url ? err('webhook_url') : 'Use https:// (plain http needs “Allow private network”).'} />
        )}
      </div>
      <div className={f.grid}>
        <SecretInput label={info.secret} info={channel?.secrets?.secret} value={secret} onChange={setSecret} clear={clearSecret} onClear={setClearSecret} hint={info.secretHint} />
        <TextField label="Or a secret reference" value={spec.secret_ref ?? ''} onChange={(v) => set('secret_ref', v)} placeholder="env:HARBOR_SLACK_WEBHOOK"
          hint="env:NAME or file:/path, resolved at send time." error={refOK ? err('secret_ref') : 'Use env:NAME or file:/path.'} />
      </div>
      {!secretOK && <span className={e.preview}>Set the {info.secret.toLowerCase()} or a secret reference.</span>}
      <Checkbox label="Enabled" checked={spec.enabled} onChange={(v) => set('enabled', v)} />

      <fieldset className={e.fieldset}>
        <legend className={e.legend}>Delivery</legend>
        <div className={f.grid}>
          <Field label="Timezone (IANA)" hint="For quiet hours and digests. Empty means UTC." error={err('timezone')}>
            {(p) => (
              <>
                <input {...p} className={uiStyles.input} list={tzList} value={spec.timezone ?? ''} placeholder="UTC" onChange={(ev) => set('timezone', ev.target.value)} />
                <datalist id={tzList}>
                  {tzNames.map((z) => <option key={z} value={z} />)}
                </datalist>
              </>
            )}
          </Field>
          <TextField label="Digest every (minutes, 0–1440)" type="number" value={String(digest)} onChange={(v) => set('digest_minutes', Number(v))}
            hint="Batches deferred messages. 0 sends one digest when quiet hours end." />
          <TextField label="Rate limit (messages per minute)" type="number" value={String(rate)} onChange={(v) => set('rate_per_minute', Number(v))}
            hint="Excess messages are deferred to the next digest." error={numbersOK ? undefined : 'Digest 0–1440 minutes; rate 1–600 per minute.'} />
        </div>
        <Checkbox label="Quiet hours (defer non-critical messages to a digest)" checked={qh.enabled} onChange={(v) => setQH({ enabled: v })} />
        {qh.enabled && (
          <div className={f.grid}>
            <TextField label="Quiet from" type="time" value={qh.start ?? ''} onChange={(v) => setQH({ start: v })} error={hhmmRE.test(qh.start ?? '') ? err('quiet_hours.start') : 'Use HH:MM.'} />
            <TextField label="Quiet until" type="time" value={qh.end ?? ''} onChange={(v) => setQH({ end: v })} error={hhmmRE.test(qh.end ?? '') ? err('quiet_hours.end') : 'Use HH:MM.'} />
            <div style={{ alignSelf: 'end' }}>
              <Checkbox label="Critical messages bypass quiet hours" checked={!!qh.critical_bypass} onChange={(v) => setQH({ critical_bypass: v })} />
            </div>
          </div>
        )}
      </fieldset>

      <details>
        <summary className="muted" style={{ cursor: 'pointer', fontSize: 'var(--text-sm)' }}>Advanced</summary>
        <div className={f.grid} style={{ marginTop: 12 }}>
          <TextField label="API base URL override" value={spec.api_base_url ?? ''} onChange={(v) => set('api_base_url', v)} placeholder="https://api.telegram.org"
            hint="For self-hosted relays. Leave empty for the provider default." error={err('api_base_url')} />
          <div style={{ alignSelf: 'end' }}>
            <Checkbox label="Allow private network" checked={!!spec.allow_private_network} onChange={(v) => set('allow_private_network', v)} />
          </div>
        </div>
      </details>
      <MutationError error={save.error} />
      <div className={f.actions}>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} disabled={!valid} onClick={submit}>Save channel</Button>
      </div>
    </Dialog>
  )
}
