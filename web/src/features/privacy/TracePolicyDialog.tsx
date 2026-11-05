import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { AlertTriangle } from 'lucide-react'
import { Banner, Button } from '@/components/ui'
import { Checkbox, Dialog, MutationError, SelectField, TextField, formStyles as f, useFieldErrors } from '@/components/form'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { resourceListQuery } from '@/lib/api/queries'
import { useSaveResource } from '@/lib/api/resources'
import type { PrivacyPolicyRes, TracePolicyRes, TracePolicySpec } from '@/lib/api/types'
import { toggle } from '@/features/config/constants'
import c from '@/features/config/config.module.css'
import { defaultTraceSpec, fromLocalInput, MAX_UNREDACTED_MS, toLocalInput, traceModes, traceSections } from './privacy'
import p from './privacy.module.css'

const MB = 1 << 20

/** Create or edit a short-lived trace policy. */
export function TracePolicyDialog({ policy, onClose }: { policy: TracePolicyRes | null; onClose: () => void }) {
  const init: TracePolicySpec = policy?.spec ?? defaultTraceSpec()
  const privacy = useQuery(resourceListQuery<PrivacyPolicyRes>('privacy-policies'))
  const [name, setName] = useState(policy?.name ?? '')
  const [mode, setMode] = useState<TracePolicySpec['mode']>(init.mode)
  const [retention, setRetention] = useState<TracePolicySpec['retention']>(init.retention)
  const [customHours, setCustomHours] = useState(String(Math.round((init.custom_ttl_seconds ?? 0) / 3600) || 24))
  const [sections, setSections] = useState<string[]>(init.sections ?? traceSections.map((s) => s.value))
  const [maxTraceMB, setMaxTraceMB] = useState(String(Math.round(((init.max_trace_bytes ?? MB) / MB) * 10) / 10))
  const [maxTotalMB, setMaxTotalMB] = useState(String(Math.round((init.max_total_bytes ?? 512 * MB) / MB)))
  const [samplePct, setSamplePct] = useState(String(Math.round((init.sample_rate ?? 0) * 100) || 100))
  const [allowExport, setAllowExport] = useState(init.allow_export ?? false)
  const [until, setUntil] = useState(toLocalInput(init.unredacted_until))
  const [redaction, setRedaction] = useState(init.redaction_policy_id ?? '')
  const save = useSaveResource<TracePolicySpec>('trace-policies')
  const errs = useFieldErrors(save.error)
  const err = (loc: string) => errs[`body.spec.${loc}`]

  const hours = Number(customHours)
  const customValid = retention !== 'custom' || (Number.isFinite(hours) && hours >= 1 && hours <= 720)
  const untilMs = until ? new Date(until).getTime() : NaN
  const untilValid = mode !== 'unredacted' || (Number.isFinite(untilMs) && untilMs > Date.now() && untilMs - Date.now() <= MAX_UNREDACTED_MS)
  const traceMB = Number(maxTraceMB)
  const totalMB = Number(maxTotalMB)
  const sizesValid = traceMB > 0 && traceMB <= 16 && totalMB > 0
  const pct = Number(samplePct)
  const pctValid = pct > 0 && pct <= 100

  const submit = () => {
    const spec: TracePolicySpec = {
      mode,
      retention,
      custom_ttl_seconds: retention === 'custom' ? Math.round(hours * 3600) : 0,
      sections,
      max_trace_bytes: Math.round(traceMB * MB),
      max_total_bytes: Math.round(totalMB * MB),
      sample_rate: pct >= 100 ? 0 : pct / 100,
      allow_export: allowExport,
      unredacted_until: mode === 'unredacted' ? fromLocalInput(until) : '',
      redaction_policy_id: redaction,
    }
    save.mutate({ existing: policy ?? undefined, body: { name, spec } }, { onSuccess: onClose })
  }

  const modeInfo = traceModes.find((m) => m.value === mode)
  return (
    <Dialog open wide title={policy ? `Edit trace policy ${policy.name}` : 'Add trace policy'} onClose={onClose}>
      <TerraformEditWarning r={policy} />
      <div className={f.grid}>
        <TextField label="Name" value={name} onChange={setName} disabled={!!policy} required error={errs['body.name']} />
        <SelectField label="Content mode" value={mode} onChange={setMode} options={traceModes.map((m) => ({ value: m.value, label: m.label }))}
          hint={modeInfo?.hint} />
        <SelectField<TracePolicySpec['retention']> label="Retention" value={retention} onChange={setRetention}
          options={[{ value: '24h', label: '24 hours' }, { value: '3d', label: '3 days (recommended)' }, { value: '7d', label: '7 days' }, { value: 'custom', label: 'Custom' }]} />
        {retention === 'custom' && (
          <TextField label="Custom retention (hours, 1–720)" type="number" value={customHours} onChange={setCustomHours}
            error={customValid ? err('custom_ttl_seconds') : 'Between 1 hour and 30 days.'} />
        )}
        <SelectField label="Redaction policy" value={redaction} onChange={setRedaction}
          hint="Redacts masked traces and the search index. Default: the built-in credential detectors."
          options={[{ value: '', label: 'Built-in credential detectors' }, ...(privacy.data?.items ?? []).map((x) => ({ value: x.id, label: x.name }))]} />
        <TextField label="Maximum per trace (MB, ≤ 16)" type="number" value={maxTraceMB} onChange={setMaxTraceMB}
          hint="Larger traces are truncated, bulkiest sections first, and flagged." />
        <TextField label="Quota per plan (MB of live traces)" type="number" value={maxTotalMB} onChange={setMaxTotalMB}
          hint="Captures beyond the quota are skipped and counted." error={sizesValid ? undefined : 'Sizes must be positive (per trace at most 16 MB).'} />
        <TextField label="Sample (% of requests)" type="number" value={samplePct} onChange={setSamplePct} error={pctValid ? undefined : 'Between 1 and 100.'} />
      </div>
      {mode === 'off' && <Banner tone="info">Content capture is off. Request metadata is still recorded in Activity.</Banner>}
      {mode === 'unredacted' && (
        <>
          <Banner tone="warning" icon={AlertTriangle}>
            <strong>Unredacted capture stores original prompts and responses</strong>, including any secrets the client sent. It runs only until the end
            time below (at most seven days ahead); afterwards capture falls back to masked full traces. Reading unredacted traces needs the
            traces.content.unredacted grant, and every access is audited.
          </Banner>
          <div className={f.grid}>
            <TextField label="Unredacted capture ends at" type="datetime-local" value={until} onChange={setUntil}
              error={untilValid ? err('unredacted_until') : 'Choose a time in the future, at most seven days ahead.'} />
          </div>
        </>
      )}
      <fieldset className={c.checks}>
        <legend className={c.legend}>Sections</legend>
        {traceSections.map((s) => (
          <Checkbox key={s.value} label={s.label} checked={sections.includes(s.value)} onChange={(on) => setSections(toggle(sections, s.value, on))} />
        ))}
      </fieldset>
      <span className={p.hint} style={{ marginLeft: 0 }}>Images and documents are recorded as metadata only. Opaque provider state is never captured.</span>
      <Checkbox label="Allow export (users with the traces.export grant can download content)" checked={allowExport} onChange={setAllowExport} />
      <MutationError error={save.error} />
      <div className={f.actions}>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} disabled={!name || !customValid || !untilValid || !sizesValid || !pctValid || sections.length === 0} onClick={submit}>
          Save trace policy
        </Button>
      </div>
    </Dialog>
  )
}
