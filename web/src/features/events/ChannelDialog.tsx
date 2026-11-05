import { useState } from 'react'
import { Anchor, Group, Select, SimpleGrid, Stack, Switch, Text, UnstyledButton } from '@mantine/core'
import { TimeInput } from '@mantine/dates'
import { ArrowLeft, MessageCircle, MessageSquare, Send, Webhook } from 'lucide-react'
import { Button } from '@/components/ui'
import {
  Advanced,
  Checkbox,
  Dialog,
  DialogActions,
  MutationError,
  NameField,
  NumberField,
  SecretRefField,
  TextField,
  checkSecretRef,
  localTimeZone,
  useFieldErrors,
} from '@/components/form'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { useSaveResource } from '@/lib/api/resources'
import type { ChannelRes, ChannelSpec } from '@/lib/api/types'
import { channelTypes, defaultChannel, deliverySummary, hhmmRE, secretShapeError } from './events'
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

const typeIcon = { slack: MessageSquare, discord: MessageCircle, telegram: Send, webhook: Webhook } as const

/** Step one of adding a channel: pick where messages go. */
function TypePicker({ onPick }: { onPick: (t: ChannelSpec['type']) => void }) {
  return (
    <Stack gap="sm">
      <Text size="sm" c="dimmed">
        Where should alerts go?
      </Text>
      <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="sm" role="list" aria-label="Channel types">
        {channelTypes.map((t) => {
          const Icon = typeIcon[t.value]
          return (
            <UnstyledButton key={t.value} className={e.typeCard} onClick={() => onPick(t.value)} role="listitem" aria-label={t.label}>
              <Icon size={22} aria-hidden="true" />
              <Stack gap={2}>
                <Text size="sm" fw={600}>
                  {t.label}
                </Text>
                <Text size="xs" c="dimmed">
                  {t.blurb}
                </Text>
              </Stack>
            </UnstyledButton>
          )
        })}
      </SimpleGrid>
    </Stack>
  )
}

/** Create or edit a notification channel. */
export function ChannelDialog({ channel, onClose }: { channel: ChannelRes | null; onClose: () => void }) {
  const [picked, setPicked] = useState(!!channel)
  const [name, setName] = useState(channel?.name ?? '')
  const [spec, setSpec] = useState<ChannelSpec>(() => {
    const d = defaultChannel()
    return { ...d, ...channel?.spec, quiet_hours: { ...d.quiet_hours!, ...channel?.spec.quiet_hours } }
  })
  const [secret, setSecret] = useState('')
  const [clearSecret, setClearSecret] = useState(false)
  const [useRef, setUseRef] = useState(!!channel?.spec.secret_ref)
  const [submitted, setSubmitted] = useState(false)
  const save = useSaveResource<ChannelSpec>('notification-channels')
  const errs = useFieldErrors(save.error)
  const err = (loc: string) => errs[`body.spec.${loc}`]
  const set = <K extends keyof ChannelSpec>(k: K, v: ChannelSpec[K]) => setSpec((s) => ({ ...s, [k]: v }))
  const qh = spec.quiet_hours!
  const setQH = (p: Partial<typeof qh>) => setSpec((s) => ({ ...s, quiet_hours: { ...qh, ...p } }))
  const info = channelTypes.find((t) => t.value === spec.type)!
  const stored = channel?.secrets?.secret?.source === 'stored'
  const digest = spec.digest_minutes ?? 0
  const rate = spec.rate_per_minute ?? 20

  // Problems, shown once the field has a value or after a save attempt.
  const webhookURL = spec.webhook_url ?? ''
  const urlProblem =
    spec.type !== 'webhook'
      ? undefined
      : !webhookURL
        ? 'Enter the URL that receives the alerts.'
        : /^https:\/\/\S+/.test(webhookURL) || (spec.allow_private_network && /^http:\/\/\S+/.test(webhookURL))
          ? undefined
          : /^http:\/\//.test(webhookURL)
            ? 'Plain http works only with “Receiver is on a private network” turned on.'
            : 'Use a full https:// URL.'
  const chatProblem = spec.type === 'telegram' && !spec.telegram_chat_id?.trim() ? 'Enter the chat ID the bot posts to.' : undefined
  const refProblem = useRef ? (spec.secret_ref ? checkSecretRef(spec.secret_ref) : `Enter a secret reference, or paste the ${info.secret.toLowerCase()} instead.`) : undefined
  const secretProblem = useRef
    ? undefined
    : spec.type === 'webhook'
      ? undefined
      : !secret && !stored
        ? `Enter the ${info.secret.toLowerCase()}.`
        : secret
          ? secretShapeError(spec.type, secret, spec.type === 'telegram' ? !!spec.api_base_url : !!spec.allow_private_network)
          : undefined
  const qhProblem = qh.enabled && !(hhmmRE.test(qh.start ?? '') && hhmmRE.test(qh.end ?? '')) ? 'Use HH:MM for both times.' : undefined
  const nameProblem = !name ? 'Enter a short name.' : undefined
  const problems = [nameProblem, urlProblem, chatProblem, refProblem, secretProblem, qhProblem].filter(Boolean)
  const show = (problem: string | undefined, hasValue: boolean) => (submitted || hasValue ? problem : undefined)

  const submit = () => {
    setSubmitted(true)
    if (problems.length > 0) return
    const body: ChannelSpec = {
      type: spec.type,
      enabled: spec.enabled,
      timezone: spec.timezone ?? '',
      quiet_hours: qh,
      digest_minutes: digest,
      rate_per_minute: rate,
      secret_ref: useRef ? spec.secret_ref ?? '' : '',
      api_base_url: spec.type === 'webhook' ? '' : spec.api_base_url ?? '',
      allow_private_network: !!spec.allow_private_network,
      ...(spec.type === 'telegram' ? { telegram_chat_id: spec.telegram_chat_id?.trim() } : {}),
      ...(spec.type === 'webhook' ? { webhook_url: webhookURL.trim() } : {}),
    }
    const secrets = useRef && spec.type !== 'webhook' ? undefined : secretChanges([{ name: 'secret', value: secret, clear: clearSecret }])
    save.mutate({ existing: channel ?? undefined, body: { name, spec: body, secrets } }, { onSuccess: onClose })
  }

  if (!picked) {
    return (
      <Dialog open title="Add alert channel" onClose={onClose}>
        <TypePicker
          onPick={(t) => {
            set('type', t)
            setPicked(true)
          }}
        />
        <DialogActions>
          <Button onClick={onClose}>Cancel</Button>
        </DialogActions>
      </Dialog>
    )
  }

  const Icon = typeIcon[spec.type]
  return (
    <Dialog
      open
      wide
      title={
        <Group gap={8} wrap="nowrap">
          <Icon size={18} aria-hidden="true" />
          <span>{channel ? `Edit ${channel.name}` : `Add ${info.label} channel`}</span>
        </Group>
      }
      onClose={onClose}
    >
      <TerraformEditWarning r={channel} />
      <Group justify="space-between" align="flex-start" gap="md" wrap="wrap">
        <div style={{ flex: '1 1 320px', minWidth: 0 }}>
          <NameField value={name} onChange={setName} locked={!!channel} autoFocus={!channel} error={show(nameProblem, false) ?? errs['name']} />
        </div>
        <Switch mt={{ base: 0, sm: 30 }} label="Send messages" checked={spec.enabled} onChange={(ev) => set('enabled', ev.currentTarget.checked)} />
      </Group>

      {spec.type === 'telegram' && (
        <TextField label="Chat ID" value={spec.telegram_chat_id ?? ''} onChange={(v) => set('telegram_chat_id', v)} placeholder="-1001234567890" required
          hint="The chat, group or channel the bot posts to." error={show(chatProblem, false) ?? err('telegram_chat_id')} />
      )}
      {spec.type === 'webhook' && (
        <Stack gap={6}>
          <TextField label="Webhook URL" value={webhookURL} onChange={(v) => set('webhook_url', v)} placeholder="https://alerts.example.com/harbor" required
            error={show(urlProblem, !!webhookURL) ?? err('webhook_url')} />
          <Checkbox label="Receiver is on a private network" checked={!!spec.allow_private_network} onChange={(v) => set('allow_private_network', v)}
            description="Allows plain http and loopback or LAN addresses. Cloud metadata addresses stay blocked." />
        </Stack>
      )}

      {useRef && spec.type !== 'webhook' ? (
        <Stack gap={4}>
          <SecretRefField label={`${info.secret} (server secret)`} value={spec.secret_ref ?? ''} onChange={(v) => set('secret_ref', v)} example={info.refExample}
            error={show(refProblem, !!spec.secret_ref) ?? err('secret_ref')} />
          <Anchor component="button" type="button" size="xs" onClick={() => setUseRef(false)} style={{ alignSelf: 'flex-start' }}>
            Paste the {info.secret.toLowerCase()} instead
          </Anchor>
        </Stack>
      ) : (
        <Stack gap={4}>
          <SecretInput label={info.secret} info={channel?.secrets?.secret} value={secret} onChange={setSecret} clear={clearSecret} onClear={setClearSecret}
            hint={info.secretHint} required={spec.type !== 'webhook' && !stored} error={show(secretProblem, !!secret) ?? err('secret')} />
          {spec.type !== 'webhook' && (
            <Anchor component="button" type="button" size="xs" onClick={() => setUseRef(true)} style={{ alignSelf: 'flex-start' }}>
              Use a secret kept on the server instead
            </Anchor>
          )}
        </Stack>
      )}

      <Advanced label={`Delivery: ${deliverySummary(spec)}`} defaultOpen={!!channel && (qh.enabled || digest > 0)}>
        <Select
          label="Time zone"
          description="For quiet hours and digests."
          data={tzNames}
          searchable
          clearable
          placeholder="UTC"
          value={spec.timezone || null}
          onChange={(v) => set('timezone', v ?? '')}
          comboboxProps={{ withinPortal: true }}
          error={err('timezone')}
          maw={360}
        />
        <Switch
          label="Quiet hours"
          description="Hold non-critical messages and send them as one digest when quiet hours end."
          checked={qh.enabled}
          onChange={(ev) => {
            const on = ev.currentTarget.checked
            setQH({ enabled: on })
            if (on && !spec.timezone) set('timezone', localTimeZone())
          }}
        />
        {qh.enabled && (
          <Group gap="md" align="flex-start" wrap="wrap">
            <TimeInput label="Quiet from" value={qh.start ?? ''} onChange={(ev) => setQH({ start: ev.currentTarget.value })} w={140} error={err('quiet_hours.start')} />
            <TimeInput label="Until" value={qh.end ?? ''} onChange={(ev) => setQH({ end: ev.currentTarget.value })} w={140} error={qhProblem ?? err('quiet_hours.end')} />
            <Checkbox label="Critical messages still go out" checked={!!qh.critical_bypass} onChange={(v) => setQH({ critical_bypass: v })} />
          </Group>
        )}
        <Group gap="md" align="flex-start" wrap="wrap">
          <NumberField label="Digest every" unit="min" value={digest} onChange={(v) => set('digest_minutes', v ?? 0)} max={1440} w={200}
            hint="0 sends one digest when quiet hours end." error={err('digest_minutes')} />
          <NumberField label="At most" unit="per min" value={rate} onChange={(v) => set('rate_per_minute', v ?? 20)} min={1} max={600} w={200}
            hint="Extra messages wait for the next digest." error={err('rate_per_minute')} />
        </Group>
        {spec.type === 'telegram' && (
          <>
            <TextField label="API base URL override" value={spec.api_base_url ?? ''} onChange={(v) => set('api_base_url', v)} placeholder={info.apiBase}
              hint="Only for a self-hosted Bot API server. Leave empty for the provider's address." error={err('api_base_url')} />
            <Checkbox label="Relay is on a private network" checked={!!spec.allow_private_network} onChange={(v) => set('allow_private_network', v)}
              description="Allows plain http and loopback or LAN addresses for the override." />
          </>
        )}
        {(spec.type === 'slack' || spec.type === 'discord') && (
          <Checkbox label="Webhook is on a private network" checked={!!spec.allow_private_network} onChange={(v) => set('allow_private_network', v)}
            description={`For a self-hosted or test receiver: allows plain http and loopback or LAN addresses instead of ${info.apiBase}.`} />
        )}
      </Advanced>

      <MutationError error={save.error} />
      {submitted && problems.length > 0 && (
        <Text size="sm" c="var(--error-text)" role="alert">
          Fix the highlighted fields to save.
        </Text>
      )}
      <DialogActions>
        {!channel && (
          <Button icon={ArrowLeft} onClick={() => setPicked(false)} style={{ marginRight: 'auto' }}>
            Change type
          </Button>
        )}
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} onClick={submit}>
          {channel ? 'Save channel' : 'Add channel'}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
