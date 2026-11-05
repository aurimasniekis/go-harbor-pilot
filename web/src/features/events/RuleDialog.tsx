import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Checkbox as MCheckbox, Group, Select, Stack, Switch, Text } from '@mantine/core'
import { Button } from '@/components/ui'
import { Advanced, Dialog, DialogActions, MutationError, NameField, NumberField, TextAreaField, useFieldErrors } from '@/components/form'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { resourceListQuery } from '@/lib/api/queries'
import { useSaveResource } from '@/lib/api/resources'
import type { ChannelRes, NotificationRuleRes, NotificationRuleSpec } from '@/lib/api/types'
import { channelTypes, defaultRule, loopingPattern, templateVariables } from './events'
import { FilterEditor } from './FilterEditor'
import e from './events.module.css'

const cooldowns = [
  { value: '0', label: 'Every time' },
  { value: '900', label: 'At most every 15 minutes' },
  { value: '3600', label: 'At most every hour' },
  { value: '21600', label: 'At most every 6 hours' },
  { value: '86400', label: 'At most once a day' },
  { value: 'custom', label: 'Custom…' },
]

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <Stack gap="sm">
      <Text size="sm" fw={600}>
        {title}
      </Text>
      {children}
    </Stack>
  )
}

/** Create or edit an alert rule as a sentence: when … at … send to … */
export function RuleDialog({ rule, onClose }: { rule: NotificationRuleRes | null; onClose: () => void }) {
  const channels = useQuery(resourceListQuery<ChannelRes>('notification-channels'))
  const [name, setName] = useState(rule?.name ?? '')
  const [spec, setSpec] = useState<NotificationRuleSpec>(() => ({ ...defaultRule(), ...rule?.spec }))
  const [filterValid, setFilterValid] = useState(true)
  const [submitted, setSubmitted] = useState(false)
  const initialCooldown = String(rule?.spec.cooldown_seconds ?? 900)
  const [cooldownChoice, setCooldownChoice] = useState(cooldowns.some((c) => c.value === initialCooldown) ? initialCooldown : 'custom')
  const save = useSaveResource<NotificationRuleSpec>('notification-rules')
  const errs = useFieldErrors(save.error)
  const err = (loc: string) => errs[`body.spec.${loc}`]
  const set = <K extends keyof NotificationRuleSpec>(k: K, v: NotificationRuleSpec[K]) => setSpec((s) => ({ ...s, [k]: v }))

  const items = channels.data?.items ?? []
  const templateLen = spec.template?.length ?? 0
  const channelErr = Object.entries(errs).find(([k]) => k.startsWith('body.spec.channel_ids'))?.[1]
  const noChannel = spec.channel_ids.length === 0
  const looping = (spec.filter.events ?? []).some(loopingPattern)
  const problems = [!name && 'name', noChannel && 'channel', !filterValid && 'events', looping && 'loop', templateLen > 2000 && 'template'].filter(Boolean)

  const submit = () => {
    setSubmitted(true)
    if (problems.length > 0) return
    save.mutate({ existing: rule ?? undefined, body: { name, spec } }, { onSuccess: onClose })
  }

  return (
    <Dialog open wide title={rule ? `Edit rule ${rule.name}` : 'Add alert rule'} onClose={onClose}>
      <TerraformEditWarning r={rule} />
      <Group justify="space-between" align="flex-start" gap="md" wrap="wrap">
        <div style={{ flex: '1 1 320px', minWidth: 0 }}>
          <NameField value={name} onChange={setName} locked={!!rule} autoFocus={!rule} error={(submitted && !name ? 'Enter a short name.' : undefined) ?? errs['name']} />
        </div>
        <Switch mt={{ base: 0, sm: 30 }} label="Rule on" checked={spec.enabled} onChange={(ev) => set('enabled', ev.currentTarget.checked)} />
      </Group>

      <Section title="When">
        <FilterEditor presets value={spec.filter} errors={{ events: err('filter.events') }}
          onChange={(fl, ok) => { set('filter', fl); setFilterValid(ok) }} />
      </Section>

      <Section title="Send to">
        {channels.data && items.length === 0 ? (
          <Text size="sm" c="dimmed">
            No channels yet. <Link to="/notifications">Add a channel</Link> first.
          </Text>
        ) : (
          <MCheckbox.Group
            value={spec.channel_ids}
            onChange={(v) => set('channel_ids', v)}
            error={channelErr ?? (submitted && noChannel ? 'Choose at least one channel.' : undefined)}
          >
            <Stack gap={8} role="group" aria-label="Channels">
              {items.map((c) => (
                <MCheckbox
                  key={c.id}
                  value={c.id}
                  label={
                    <>
                      <span className="mono">{c.name}</span> <span className="muted">{channelTypes.find((t) => t.value === c.spec.type)?.label ?? c.spec.type}</span>
                    </>
                  }
                  description={c.spec.enabled ? undefined : 'This channel is off, so nothing is sent through it.'}
                />
              ))}
            </Stack>
          </MCheckbox.Group>
        )}
      </Section>

      <Section title="How often">
        <Group gap="md" align="flex-start" wrap="wrap">
          <Select
            label="Repeat alerts for the same problem"
            data={cooldowns}
            value={cooldownChoice}
            onChange={(v) => {
              const c = v ?? '900'
              setCooldownChoice(c)
              if (c !== 'custom') set('cooldown_seconds', Number(c))
            }}
            allowDeselect={false}
            comboboxProps={{ withinPortal: true }}
            w={280}
            error={err('cooldown_seconds')}
          />
          {cooldownChoice === 'custom' && (
            <NumberField label="Wait at least" unit="min" value={Math.round((spec.cooldown_seconds ?? 0) / 60)} max={10080} w={180}
              onChange={(v) => set('cooldown_seconds', (v ?? 0) * 60)} />
          )}
        </Group>
        <Switch label="Tell me when it recovers" description="For example quota available again, or a protocol path restored."
          checked={!!spec.recovery} onChange={(ev) => set('recovery', ev.currentTarget.checked)} />
      </Section>

      <Advanced label="Custom message and grouping" defaultOpen={!!spec.template || !!spec.group_seconds}>
        <NumberField label="Group repeats for" unit="min" value={Math.round((spec.group_seconds ?? 0) / 60)} max={1440} w={200}
          hint="After the first message, repeats of the same problem are counted and sent as one summary. 0 sends each one."
          onChange={(v) => set('group_seconds', (v ?? 0) * 60)} error={err('group_seconds')} />
        <TextAreaField label="Message" mono minRows={4} value={spec.template ?? ''} onChange={(v) => set('template', v)}
          placeholder={'{{.Data.label | upper}} is {{.Severity}}\nProvider: {{.Subjects.provider_id}}'}
          hint={`Optional Go template. Empty sends every data field. ${templateLen}/2000`}
          error={err('template') ?? (templateLen > 2000 ? 'At most 2000 characters.' : undefined)} />
        <dl className={e.vars} aria-label="Template variables">
          {templateVariables.map((v) => (
            <div key={v.name} style={{ display: 'contents' }}>
              <dt>{v.name}</dt>
              <dd>{v.description}</dd>
            </div>
          ))}
        </dl>
      </Advanced>

      <MutationError error={save.error} />
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} onClick={submit}>
          {rule ? 'Save rule' : 'Add rule'}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
