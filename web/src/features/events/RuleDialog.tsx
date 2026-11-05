import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Banner, Button } from '@/components/ui'
import { Checkbox, Dialog, Field, MutationError, TextField, formStyles as f, useFieldErrors } from '@/components/form'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { resourceListQuery } from '@/lib/api/queries'
import { useSaveResource } from '@/lib/api/resources'
import type { ChannelRes, NotificationRuleRes, NotificationRuleSpec } from '@/lib/api/types'
import { toggle } from '@/features/config/constants'
import { channelTypes, defaultRule, templateVariables } from './events'
import { FilterEditor } from './FilterEditor'
import e from './events.module.css'

const loops = (p: string) => p.startsWith('notification.') || p.startsWith('event.')

/** Create or edit a notification rule. */
export function RuleDialog({ rule, onClose }: { rule: NotificationRuleRes | null; onClose: () => void }) {
  const channels = useQuery(resourceListQuery<ChannelRes>('notification-channels'))
  const [name, setName] = useState(rule?.name ?? '')
  const [spec, setSpec] = useState<NotificationRuleSpec>(() => ({ ...defaultRule(), ...rule?.spec }))
  const [filterValid, setFilterValid] = useState(true)
  const [groupMin, setGroupMin] = useState(String((rule?.spec.group_seconds ?? 0) / 60))
  const [cooldownMin, setCooldownMin] = useState(String((rule?.spec.cooldown_seconds ?? 900) / 60))
  const save = useSaveResource<NotificationRuleSpec>('notification-rules')
  const errs = useFieldErrors(save.error)
  const err = (loc: string) => errs[`body.spec.${loc}`]
  const set = <K extends keyof NotificationRuleSpec>(k: K, v: NotificationRuleSpec[K]) => setSpec((s) => ({ ...s, [k]: v }))

  const group = Number(groupMin)
  const cooldown = Number(cooldownMin)
  const timingOK = Number.isFinite(group) && group >= 0 && group <= 1440 && Number.isFinite(cooldown) && cooldown >= 0 && cooldown <= 10080
  const looping = (spec.filter.events ?? []).filter(loops)
  const templateLen = spec.template?.length ?? 0
  const channelErr = Object.entries(errs).find(([k]) => k.startsWith('body.spec.channel_ids'))?.[1]
  const valid = !!name && filterValid && spec.channel_ids.length > 0 && timingOK && looping.length === 0 && templateLen <= 2000

  const submit = () => {
    const body: NotificationRuleSpec = { ...spec, group_seconds: Math.round(group * 60), cooldown_seconds: Math.round(cooldown * 60) }
    save.mutate({ existing: rule ?? undefined, body: { name, spec: body } }, { onSuccess: onClose })
  }

  const items = channels.data?.items ?? []
  return (
    <Dialog open wide title={rule ? `Edit rule ${rule.name}` : 'Add notification rule'} onClose={onClose}>
      <TerraformEditWarning r={rule} />
      <div className={f.grid}>
        <TextField label="Name" value={name} onChange={setName} disabled={!!rule} required error={errs['body.name']} />
        <div style={{ alignSelf: 'end' }}>
          <Checkbox label="Enabled" checked={spec.enabled} onChange={(v) => set('enabled', v)} />
        </div>
      </div>
      <FilterEditor value={spec.filter} legend="When these events happen" errors={{ events: err('filter.events') }}
        onChange={(fl, ok) => { set('filter', fl); setFilterValid(ok) }} />
      {looping.length > 0 && (
        <Banner tone="error">
          Remove {looping.join(', ')}: notification and delivery events cannot trigger notifications, which prevents failure loops.
        </Banner>
      )}

      <fieldset className={e.fieldset}>
        <legend className={e.legend}>Send to</legend>
        {channels.data && items.length === 0 ? (
          <span className="muted">No channels yet. <Link to="/notifications">Add a channel</Link> first.</span>
        ) : (
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: '6px 16px' }} role="group" aria-label="Channels">
            {items.map((c) => (
              <Checkbox key={c.id} checked={spec.channel_ids.includes(c.id)} onChange={(on) => set('channel_ids', toggle(spec.channel_ids, c.id, on))}
                label={<><span className="mono">{c.name}</span> <span className="muted">({channelTypes.find((t) => t.value === c.spec.type)?.label ?? c.spec.type}{c.spec.enabled ? '' : ', disabled'})</span></>} />
            ))}
          </div>
        )}
        {(channelErr || (spec.channel_ids.length === 0 && items.length > 0)) && (
          <span className={f.error} role="alert">{channelErr ?? 'Choose at least one channel.'}</span>
        )}
      </fieldset>

      <fieldset className={e.fieldset}>
        <legend className={e.legend}>Noise control</legend>
        <div className={f.grid}>
          <TextField label="Group repeats for (minutes)" type="number" value={groupMin} onChange={setGroupMin}
            hint="After the first message, repeats of the same condition are counted and summarised. 0 sends each one." />
          <TextField label="Cooldown (minutes)" type="number" value={cooldownMin} onChange={setCooldownMin}
            hint="How long the same condition stays silent before it can notify again."
            error={timingOK ? err('cooldown_seconds') ?? err('group_seconds') : 'Group 0–1440 minutes; cooldown 0–10080 minutes.'} />
        </div>
        <Checkbox label="Notify on recovery (quota window recovered, breaker closed, …)" checked={!!spec.recovery} onChange={(v) => set('recovery', v)} />
      </fieldset>

      <fieldset className={e.fieldset}>
        <legend className={e.legend}>Message template (optional)</legend>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 280px), 1fr))', gap: 16 }}>
          <Field label="Body template" error={err('template') ?? (templateLen > 2000 ? 'At most 2000 characters.' : undefined)}
            hint={`Go text/template. Empty lists every data field. Output is escaped per channel. ${templateLen}/2000`}>
            {(p) => (
              <textarea {...p} className={f.textarea} rows={6} spellCheck={false} value={spec.template ?? ''}
                placeholder={'{{.Data.label | upper}} is {{.Severity}}\nProvider: {{.Subjects.provider_id}}'}
                onChange={(ev) => set('template', ev.target.value)} style={{ fontFamily: 'var(--font-mono)' }} />
            )}
          </Field>
          <dl className={e.vars} aria-label="Template variables">
            {templateVariables.map((v) => (
              <div key={v.name} style={{ display: 'contents' }}>
                <dt>{v.name}</dt>
                <dd>{v.description}</dd>
              </div>
            ))}
          </dl>
        </div>
      </fieldset>
      <MutationError error={save.error} />
      <div className={f.actions}>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} disabled={!valid} onClick={submit}>Save rule</Button>
      </div>
    </Dialog>
  )
}
