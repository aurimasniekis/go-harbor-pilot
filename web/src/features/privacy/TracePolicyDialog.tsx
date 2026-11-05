import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { SimpleGrid, Stack, Text, Title } from '@mantine/core'
import { AlertTriangle } from 'lucide-react'
import { Banner, Button } from '@/components/ui'
import {
  Checkbox, DateTimeField, Dialog, DialogActions, MutationError, NameField, NumberField, SelectField, formStyles as f, nameError, useFieldErrors,
} from '@/components/form'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { resourceListQuery } from '@/lib/api/queries'
import { useSaveResource } from '@/lib/api/resources'
import type { PrivacyPolicyRes, TracePolicyRes, TracePolicySpec } from '@/lib/api/types'
import { toggle } from '@/features/config/constants'
import { defaultTraceSpec, MAX_UNREDACTED_MS, traceModes, traceSections } from './privacy'

const MB = 1 << 20
const MAX_TRACE_MB = 16

/** Create or edit a content capture rule. */
export function TracePolicyDialog({ policy, onClose }: { policy: TracePolicyRes | null; onClose: () => void }) {
  const init: TracePolicySpec = policy?.spec ?? defaultTraceSpec()
  const privacy = useQuery(resourceListQuery<PrivacyPolicyRes>('privacy-policies'))
  const [name, setName] = useState(policy?.name ?? '')
  const [mode, setMode] = useState<TracePolicySpec['mode']>(init.mode)
  const [retention, setRetention] = useState<TracePolicySpec['retention']>(init.retention)
  const [customHours, setCustomHours] = useState<number | null>(Math.round((init.custom_ttl_seconds ?? 0) / 3600) || 24)
  const [sections, setSections] = useState<string[]>(init.sections ?? traceSections.map((s) => s.value))
  const [traceMB, setTraceMB] = useState<number | null>(Math.round(((init.max_trace_bytes ?? MB) / MB) * 10) / 10)
  const [totalMB, setTotalMB] = useState<number | null>(Math.round((init.max_total_bytes ?? 512 * MB) / MB))
  const [pct, setPct] = useState<number | null>(Math.round((init.sample_rate ?? 0) * 100) || 100)
  const [allowExport, setAllowExport] = useState(init.allow_export ?? false)
  const [until, setUntil] = useState(init.unredacted_until ?? '')
  const [redaction, setRedaction] = useState(init.redaction_policy_id ?? '')
  const save = useSaveResource<TracePolicySpec>('trace-policies')
  const errs = useFieldErrors(save.error)
  const err = (loc: string) => errs[`body.spec.${loc}`]

  const hours = customHours ?? 0
  const customError = retention !== 'custom' || (hours >= 1 && hours <= 720) ? err('custom_ttl_seconds') : 'Between 1 hour and 30 days (720 hours).'
  const untilMs = until ? new Date(until).getTime() : NaN
  const untilError =
    mode !== 'unredacted'
      ? undefined
      : !until
        ? 'Choose when unredacted capture ends.'
        : !(untilMs > Date.now() && untilMs - Date.now() <= MAX_UNREDACTED_MS)
          ? 'Choose a time in the future, at most seven days ahead.'
          : err('unredacted_until')
  // Each size gets its own message on its own field.
  const traceError = traceMB === null || traceMB <= 0 || traceMB > MAX_TRACE_MB ? `Between 0.1 and ${MAX_TRACE_MB} MB.` : err('max_trace_bytes')
  const totalError = totalMB === null || totalMB < 1 ? 'At least 1 MB.' : err('max_total_bytes')
  const pctError = pct === null || pct < 1 || pct > 100 ? 'Between 1 and 100.' : err('sample_rate')
  const nameProblem = policy ? undefined : name ? nameError(name) : undefined

  const buildSpec = (): TracePolicySpec => ({
    mode,
    retention,
    custom_ttl_seconds: retention === 'custom' ? Math.round(hours * 3600) : 0,
    sections,
    max_trace_bytes: Math.round((traceMB ?? 0) * MB),
    max_total_bytes: Math.round((totalMB ?? 0) * MB),
    sample_rate: (pct ?? 100) >= 100 ? 0 : (pct ?? 100) / 100,
    allow_export: allowExport,
    unredacted_until: mode === 'unredacted' ? until : '',
    redaction_policy_id: redaction,
  })
  const initial = useMemo(() => JSON.stringify(policy?.spec ?? defaultTraceSpec()), [policy])
  const dirty = name !== (policy?.name ?? '') || JSON.stringify(buildSpec()) !== initial

  const submit = () => save.mutate({ existing: policy ?? undefined, body: { name, spec: buildSpec() } }, { onSuccess: onClose })
  const modeInfo = traceModes.find((m) => m.value === mode)
  const blocked = !name || !!nameProblem || !!customError || !!untilError || !!traceError || !!totalError || !!pctError || sections.length === 0

  return (
    <Dialog
      open
      wide
      title={policy ? `Edit capture rule ${policy.name}` : 'Add capture rule'}
      onClose={onClose}
      dirty={dirty}
      footer={
        <DialogActions>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" busy={save.isPending} disabled={blocked} onClick={submit}>
            {policy ? 'Save capture rule' : 'Create capture rule'}
          </Button>
        </DialogActions>
      }
    >
      <Stack gap="lg">
        <TerraformEditWarning r={policy} />
        <div className={f.grid}>
          <NameField value={name} onChange={setName} locked={!!policy} autoFocus={!policy} error={errs['body.name'] ?? nameProblem} />
          <SelectField label="What to keep" value={mode} onChange={setMode} options={traceModes.map((m) => ({ value: m.value, label: m.label }))} hint={modeInfo?.hint} />
          <SelectField<TracePolicySpec['retention']> label="Keep for" value={retention} onChange={setRetention}
            options={[{ value: '24h', label: '24 hours' }, { value: '3d', label: '3 days (recommended)' }, { value: '7d', label: '7 days' }, { value: 'custom', label: 'Custom' }]} />
          {retention === 'custom' && <NumberField label="Keep for" unit="hours" value={customHours} onChange={setCustomHours} min={1} max={720} error={customError} />}
          <SelectField label="Mask captured content with" value={redaction} onChange={setRedaction}
            hint="Also applies to the search index."
            options={[{ value: '', label: 'Built-in credential detectors' }, ...(privacy.data?.items ?? []).map((x) => ({ value: x.id, label: x.name }))]} />
        </div>
        {mode === 'off' && <Banner tone="info">Content capture is off. Request details are still recorded in Requests.</Banner>}
        {mode === 'unredacted' && (
          <>
            <Banner tone="warning" icon={AlertTriangle}>
              <strong>Unredacted capture stores original prompts and responses</strong>, including any secrets the client sent. It runs only until the end
              time below (at most seven days ahead); afterwards capture falls back to masked full content. Reading it needs the traces.content.unredacted
              grant, and every access is audited.
            </Banner>
            <div className={f.grid}>
              <DateTimeField label="Unredacted capture ends at" value={until} onChange={setUntil} error={untilError} required />
            </div>
          </>
        )}

        <Stack gap="sm">
          <Title order={4}>Limits</Title>
          <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="md">
            <NumberField label="Largest capture" unit="MB" integer={false} min={0.1} max={MAX_TRACE_MB} value={traceMB} onChange={setTraceMB} error={traceError}
              hint={`Bigger captures are shortened, bulkiest parts first. At most ${MAX_TRACE_MB} MB.`} />
            <NumberField label="Space per plan" unit="MB" min={1} value={totalMB} onChange={setTotalMB} error={totalError} hint="Captures beyond this are skipped and counted." />
            <NumberField label="Capture" unit="% of requests" min={1} max={100} value={pct} onChange={setPct} error={pctError} />
          </SimpleGrid>
        </Stack>

        <Stack gap="sm">
          <Title order={4}>What is captured</Title>
          <SimpleGrid cols={{ base: 1, sm: 2 }} spacing={6}>
            {traceSections.map((s) => (
              <Checkbox key={s.value} label={s.label} checked={sections.includes(s.value)} onChange={(on) => setSections(toggle(sections, s.value, on))} />
            ))}
          </SimpleGrid>
          {sections.length === 0 && <Text size="sm" c="var(--error-text)">Choose at least one part.</Text>}
          <Text size="xs" c="dimmed">
            Images and documents are recorded as metadata only. Opaque provider state is never captured.
          </Text>
        </Stack>
        <Checkbox label="Allow downloads" description="People with the traces.export grant can download captured content." checked={allowExport} onChange={setAllowExport} />
        <MutationError error={save.error} />
      </Stack>
    </Dialog>
  )
}
