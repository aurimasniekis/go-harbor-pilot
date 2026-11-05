import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Banner, Button } from '@/components/ui'
import { Checkbox, MutationError, SelectField, TextField, formStyles as f } from '@/components/form'
import { KeyValueEditor, LinesEditor } from '@/components/KeyValueEditor'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { principalsQuery, resourceListQuery, usersQuery } from '@/lib/api/queries'
import { useSaveResource } from '@/lib/api/resources'
import type { BindingRes, BindingSpec, PlanRes, ProjectRes } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { harnesses, toInt, toggle } from './constants'
import c from './config.module.css'

export const emptyBinding = (): BindingSpec => ({
  display_name: '',
  owner_kind: 'none',
  owner_id: '',
  plan_id: '',
  project_id: '',
  allowed_project_ids: [],
  expected_harness: '',
  allowed_models: [],
  limits: { requests_per_minute: 0, max_concurrency: 0, max_request_bytes: 0 },
  trust: { session_hints: true, project_hints: false, intent_hints: false },
  external_refs: {},
  enabled: true,
})

/** Create or edit a client binding. */
export function BindingForm({ binding, onSaved, onCancel }: { binding: BindingRes | null; onSaved: (b: BindingRes) => void; onCancel?: () => void }) {
  const plans = useQuery(resourceListQuery<PlanRes>('routing-plans'))
  const projects = useQuery(resourceListQuery<ProjectRes>('projects'))
  const canUsers = useCan('users.manage')
  const users = useQuery({ ...usersQuery, enabled: canUsers })
  const principals = useQuery({ ...principalsQuery, enabled: canUsers })
  const canWrite = useCan('config.write')
  const [name, setName] = useState(binding?.name ?? '')
  const [s, setS] = useState<BindingSpec>(() => structuredClone(binding?.spec ?? emptyBinding()))
  const [refsValid, setRefsValid] = useState(true)
  const save = useSaveResource<BindingSpec, BindingRes>('client-bindings')
  const set = (p: Partial<BindingSpec>) => setS({ ...s, ...p })
  const projectItems = projects.data?.items ?? []

  const ownerOptions =
    s.owner_kind === 'user'
      ? (users.data?.items ?? []).map((u) => ({ value: u.id, label: u.email }))
      : (principals.data?.items ?? []).map((p) => ({ value: p.id, label: p.name }))

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-4)' }}>
      <TerraformEditWarning r={binding} />
      <div className={f.grid}>
        <TextField label="Name" value={name} onChange={setName} disabled={!!binding} required />
        <TextField label="Display name" value={s.display_name} onChange={(v) => set({ display_name: v })} maxLength={120} />
        <SelectField label="Routing plan" value={s.plan_id} onChange={(v) => set({ plan_id: v })}
          options={[{ value: '', label: 'Select a plan' }, ...(plans.data?.items ?? []).map((p) => ({ value: p.id, label: p.name }))]} />
        <SelectField label="Expected harness" value={s.expected_harness} onChange={(v) => set({ expected_harness: v as BindingSpec['expected_harness'] })}
          options={harnesses.map((h) => ({ value: h.value, label: h.label }))} hint="Informational; mismatches are recorded, never treated as identity." />
        <SelectField label="Owner" value={s.owner_kind || 'none'} onChange={(v) => set({ owner_kind: v as BindingSpec['owner_kind'], owner_id: '' })}
          options={[{ value: 'none', label: 'No owner' }, { value: 'user', label: 'User' }, { value: 'service', label: 'Service principal' }]} />
        {s.owner_kind === 'user' || s.owner_kind === 'service' ? (
          canUsers ? (
            <SelectField label={s.owner_kind === 'user' ? 'User' : 'Service principal'} value={s.owner_id} onChange={(v) => set({ owner_id: v })}
              options={[{ value: '', label: 'Select…' }, ...ownerOptions]} />
          ) : (
            <TextField label="Owner id" value={s.owner_id} onChange={(v) => set({ owner_id: v })} hint={s.owner_kind === 'user' ? 'usr_…' : 'pri_…'} />
          )
        ) : null}
        <SelectField label="Default project" value={s.project_id} onChange={(v) => set({ project_id: v })}
          options={[{ value: '', label: 'None' }, ...projectItems.map((p) => ({ value: p.id, label: p.name }))]} />
      </div>
      {projectItems.length > 0 && (
        <fieldset className={c.checks}>
          <legend className={c.legend}>Projects a trusted project hint may select</legend>
          {projectItems.map((p) => (
            <Checkbox key={p.id} label={p.name} checked={s.allowed_project_ids.includes(p.id)}
              onChange={(on) => set({ allowed_project_ids: toggle(s.allowed_project_ids, p.id, on) })} />
          ))}
        </fieldset>
      )}
      <LinesEditor label="Allowed models" value={s.allowed_models} onChange={(v) => set({ allowed_models: v })}
        hint="Requested model names (aliases or plan mappings) this client may use. Empty = anything the plan maps." />
      <div className={f.grid}>
        <TextField label="Requests per minute (0 = unlimited)" type="number" value={String(s.limits.requests_per_minute)}
          onChange={(v) => set({ limits: { ...s.limits, requests_per_minute: toInt(v) } })} />
        <TextField label="Max concurrency (0 = unlimited)" type="number" value={String(s.limits.max_concurrency)}
          onChange={(v) => set({ limits: { ...s.limits, max_concurrency: toInt(v) } })} />
        <TextField label="Max request bytes (0 = default)" type="number" value={String(s.limits.max_request_bytes)}
          onChange={(v) => set({ limits: { ...s.limits, max_request_bytes: toInt(v) } })} />
      </div>
      <fieldset className={c.checks} style={{ flexDirection: 'column' }}>
        <legend className={c.legend}>Client hints</legend>
        <Banner tone="info">
          Hints from clients (session, project and intent headers) are always recorded as untrusted claims. Trusting them only affects
          attribution, never authorization.
        </Banner>
        <Checkbox label="Group requests into sessions using harness session headers" checked={s.trust.session_hints}
          onChange={(v) => set({ trust: { ...s.trust, session_hints: v } })} />
        <Checkbox label="Allow X-HP-Project to select an allowed project" checked={s.trust.project_hints}
          onChange={(v) => set({ trust: { ...s.trust, project_hints: v } })} />
        <Checkbox label="Accept declared intent (X-HP-Intent) as trusted" checked={s.trust.intent_hints}
          onChange={(v) => set({ trust: { ...s.trust, intent_hints: v } })} />
      </fieldset>
      <KeyValueEditor label="External references" value={s.external_refs} onChange={(v, ok) => { set({ external_refs: v }); setRefsValid(ok) }}
        hint="Operator-configured trusted references, e.g. git.repo=github.com/org/repo." />
      <Checkbox label="Enabled" checked={s.enabled} onChange={(v) => set({ enabled: v })} />
      <MutationError error={save.error} />
      {canWrite && (
        <div className={f.actions}>
          {onCancel && <Button onClick={onCancel}>Cancel</Button>}
          <Button variant="primary" busy={save.isPending} disabled={!name || !s.plan_id || !refsValid}
            onClick={() => save.mutate({ existing: binding ?? undefined, body: { name, spec: s } }, { onSuccess: onSaved })}>
            Save client
          </Button>
        </div>
      )}
    </div>
  )
}
