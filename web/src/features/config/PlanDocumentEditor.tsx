import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { MultiSelect, SegmentedControl, Select, SimpleGrid, Stack, Table, Text, TextInput, Title, Group } from '@mantine/core'
import { ArrowDown, ArrowUp, Plus, Trash2 } from 'lucide-react'
import { Banner, Button, Tabs } from '@/components/ui'
import { Checkbox, Dialog, DialogActions, MutationError, NumberField, SelectField, useFieldErrors, formStyles as f } from '@/components/form'
import { resourceListQuery } from '@/lib/api/queries'
import type { AliasRes, FieldErrorDTO, ModelMapping, PlanDocument, PrivacyPolicyRes, ProviderRes, TracePolicyRes } from '@/lib/api/types'
import { Link } from '@tanstack/react-router'
import { traceModes } from '@/features/privacy/privacy'
import { EngineEditor } from '@/features/routing/EngineEditor'
import { CompatEditor } from '@/features/compat/CompatEditor'
import { isDefault } from '@/features/compat/compat'
import { fieldErrors } from '@/lib/api/client'
import { move, parseEngine, serializeEngine } from '@/features/routing/engine'
import { billingClasses, capabilities, toggle } from './constants'
import c from './config.module.css'

/** The default document for a plan's first draft. */
export function defaultDocument(): PlanDocument {
  return {
    schema_version: 1,
    model_mappings: [],
    default_alias_id: '',
    restrictions: { allowed_provider_ids: [], denied_provider_ids: [], allowed_billing: [], allowed_classifications: [], required_capabilities: [] },
    fallback: { steps: ['alternate_account', 'alternate_model', 'stop'], max_attempts: 3, allow_paid_fallback: false, wait_max_seconds: 0 },
  }
}

function Checks({ legend, options, value, onChange }: {
  legend: string
  options: readonly { value: string; label: string; hint?: string }[]
  value: string[]
  onChange: (v: string[]) => void
}) {
  return (
    <Stack gap={6}>
      <Text size="sm" fw={600}>
        {legend}
      </Text>
      <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="xs" verticalSpacing={6}>
        {options.map((o) => (
          <Checkbox key={o.value} label={o.label} description={o.hint} checked={value.includes(o.value)} onChange={(on) => onChange(toggle(value, o.value, on))} />
        ))}
      </SimpleGrid>
    </Stack>
  )
}

/** Plain labels for fallback steps (Stop is the implicit end and not listed). */
const stepLabels: Record<string, string> = {
  retry: 'Retry the same account',
  alternate_account: 'Try another account with the same model',
  alternate_model: 'Try another model in the fallback group',
  alternate_provider: 'Try another provider',
  wait: 'Wait for capacity, then retry',
}

const classificationOptions = [
  { value: 'official', label: 'Official', hint: 'Vendor APIs used as documented.' },
  { value: 'partial', label: 'Partial', hint: 'Compatible APIs with known gaps.' },
  { value: 'unofficial', label: 'Unofficial', hint: 'Undocumented or community integrations.' },
  { value: 'local', label: 'Local', hint: 'Models running on your own hardware.' },
]

const capabilityLabels: Record<string, string> = {
  streaming: 'Streaming',
  tools: 'Tool use',
  parallel_tools: 'Parallel tool calls',
  tool_streaming: 'Streamed tool arguments',
  vision: 'Images',
  reasoning: 'Reasoning',
  structured_output: 'Structured output',
  prompt_cache: 'Prompt caching',
  pdf: 'PDF input',
}

type ProviderMode = 'any' | 'only' | 'except'

/** Structured editor for a routing plan document (draft versions only). */
export function PlanDocumentEditor({ title, initial, note: initialNote, busy, error, onSave, onClose, validationErrors = [] }: {
  title: string
  initial: PlanDocument
  note: string
  busy: boolean
  error: unknown
  onSave: (doc: PlanDocument, note: string) => void
  onClose: () => void
  /** Errors from this draft's last validation, shown next to what they refer to. */
  validationErrors?: FieldErrorDTO[]
}) {
  const aliases = useQuery(resourceListQuery<AliasRes>('model-aliases'))
  const providers = useQuery(resourceListQuery<ProviderRes>('providers'))
  const [doc, setDoc] = useState<PlanDocument>(() => structuredClone(initial))
  const [note, setNote] = useState(initialNote)
  const [engine, setEngine] = useState(() => parseEngine(initial.engine))
  const engineErrors = validationErrors.filter((e) => e.location.startsWith('body.document.engine'))
  const privacyErrors = validationErrors.filter((e) => e.location.startsWith('body.document.privacy'))
  const saveErrors = Object.entries(fieldErrors(error)).map(([location, message]) => ({ location, message }))
  const compatErrors = [...validationErrors, ...saveErrors].filter((e) => e.location.startsWith('body.document.compatibility'))
  const [tab, setTab] = useState<'routing' | 'rules' | 'privacy' | 'compat'>(
    engineErrors.length > 0 ? 'rules' : privacyErrors.length > 0 ? 'privacy' : compatErrors.length > 0 ? 'compat' : 'routing')
  const compatErrorKey = compatErrors.map((e) => e.location + e.message).join('|')
  useEffect(() => {
    if (compatErrorKey) setTab('compat')
  }, [compatErrorKey])
  const privacyPolicies = useQuery(resourceListQuery<PrivacyPolicyRes>('privacy-policies'))
  const tracePolicies = useQuery(resourceListQuery<TracePolicyRes>('trace-policies'))
  const priv = doc.privacy ?? { policy_id: '', trace_policy_id: '' }
  const selectedTrace = (tracePolicies.data?.items ?? []).find((t) => t.id === priv.trace_policy_id)
  const save = () => {
    const out: PlanDocument = { ...doc }
    if (!out.privacy?.policy_id && !out.privacy?.trace_policy_id) delete out.privacy
    if (isDefault(out.compatibility)) delete out.compatibility
    const e = serializeEngine(engine)
    if (e) out.engine = e
    else delete out.engine
    onSave(out, note)
  }
  const aliasOptions = [{ value: '', label: 'Choose a model name' }, ...(aliases.data?.items ?? []).map((a) => ({ value: a.id, label: a.name }))]
  const providerOptions = (providers.data?.items ?? []).map((p) => ({ value: p.id, label: p.spec.display_name || p.name }))
  const r = doc.restrictions
  const fb = doc.fallback
  // Stop is the end of the list: edit the other steps and keep it last.
  const steps = fb.steps.filter((x) => x !== 'stop')
  const setSteps = (next: string[]) => setDoc({ ...doc, fallback: { ...fb, steps: [...next, 'stop'] } })
  const available = Object.keys(stepLabels).filter((x) => !steps.includes(x))
  const [addStep, setAddStep] = useState('')
  const stepToAdd = available.includes(addStep) ? addStep : (available[0] ?? '')
  const providerMode: ProviderMode = r.allowed_provider_ids.length > 0 ? 'only' : r.denied_provider_ids.length > 0 ? 'except' : 'any'
  const setProviderMode = (m: ProviderMode) =>
    setDoc({ ...doc, restrictions: { ...r, allowed_provider_ids: m === 'only' ? r.allowed_provider_ids : [], denied_provider_ids: m === 'except' ? r.denied_provider_ids : [] } })
  const setMapping = (i: number, p: Partial<ModelMapping>) =>
    setDoc({ ...doc, model_mappings: doc.model_mappings.map((m, j) => (j === i ? { ...m, ...p } : m)) })
  const errs = useFieldErrors(error)
  const mappingError = (i: number, field: string) => errs[`body.document.model_mappings[${i}].${field}`] ?? errs[`body.document.model_mappings[${i}]`]
  const stepsError = Object.entries(fieldErrors(error)).find(([k]) => k.startsWith('body.document.fallback'))?.[1]
  const dirty = note !== initialNote || JSON.stringify(doc) !== JSON.stringify(initial) || JSON.stringify(serializeEngine(engine) ?? null) !== JSON.stringify(serializeEngine(parseEngine(initial.engine)) ?? null)
  const incomplete = doc.model_mappings.some((m) => !m.match.trim() || !m.alias_id)

  return (
    <Dialog open wide title={title} onClose={onClose} dirty={dirty}>
      <Tabs label="Plan sections" value={tab} onChange={setTab}
        items={[
          { value: 'routing', label: 'Models and fallback' },
          { value: 'rules', label: 'Rules', count: engine.rules.length + engine.tables.length || undefined },
          { value: 'privacy', label: 'Privacy' },
          { value: 'compat', label: 'Compatibility' },
        ]} />
      {tab === 'compat' ? (
        <CompatEditor value={doc.compatibility} onChange={(v) => setDoc({ ...doc, compatibility: v })} errors={compatErrors} />
      ) : tab === 'rules' ? (
        <EngineEditor value={engine} onChange={setEngine} errors={engineErrors} />
      ) : tab === 'privacy' ? (
        <>
          <p className="secondary">
            Masking and content capture are independent. Manage policies under <Link to="/privacy" style={{ textDecoration: 'underline' }}>Privacy</Link>.
          </p>
          {privacyErrors.map((e) => (
            <Banner key={e.location} tone="error">{e.message}</Banner>
          ))}
          <div className={f.grid}>
            <SelectField label="Masking policy" value={priv.policy_id ?? ''} onChange={(v) => setDoc({ ...doc, privacy: { ...priv, policy_id: v } })}
              hint="Masks credentials and other sensitive values before requests leave the gateway."
              options={[{ value: '', label: 'None — no outbound masking' }, ...(privacyPolicies.data?.items ?? []).map((x) => ({ value: x.id, label: x.name }))]} />
            <SelectField label="Content capture rule" value={priv.trace_policy_id ?? ''} onChange={(v) => setDoc({ ...doc, privacy: { ...priv, trace_policy_id: v } })}
              hint="Captures short-lived, encrypted content. Off unless selected."
              options={[{ value: '', label: 'None — no content capture' }, ...(tracePolicies.data?.items ?? []).map((x) => ({ value: x.id, label: x.name }))]} />
          </div>
          {selectedTrace && (
            <Banner tone={selectedTrace.spec.mode === 'unredacted' ? 'warning' : 'info'}>
              {traceModes.find((m) => m.value === selectedTrace.spec.mode)?.hint}
            </Banner>
          )}
        </>
      ) : (
        <Stack gap="lg">
          <Stack gap="xs">
            <Text size="sm" fw={600}>
              Where requests go
            </Text>
            <Text size="xs" c="dimmed">
              The first matching row wins. A client asking for a model name directly (for example coding/default) always gets it.
            </Text>
            <Table className={c.mapTable} verticalSpacing={6} withRowBorders={false}>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>If the client asks for</Table.Th>
                  <Table.Th>Use model name</Table.Th>
                  <Table.Th>Note</Table.Th>
                  <Table.Th>
                    <span className="visually-hidden">Order and remove</span>
                  </Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {doc.model_mappings.map((m, i) => (
                  <Table.Tr key={i}>
                    <Table.Td>
                      <TextInput aria-label={`Row ${i + 1}: requested model`} value={m.match} placeholder="e.g. claude-sonnet-*" error={mappingError(i, 'match')}
                        onChange={(e) => setMapping(i, { match: e.currentTarget.value })} />
                    </Table.Td>
                    <Table.Td>
                      <Select aria-label={`Row ${i + 1}: model name`} data={aliasOptions.slice(1)} value={m.alias_id || null} placeholder="Choose a model name"
                        error={mappingError(i, 'alias_id')} allowDeselect={false} onChange={(v) => setMapping(i, { alias_id: v ?? '' })} />
                    </Table.Td>
                    <Table.Td>
                      <TextInput aria-label={`Row ${i + 1}: note`} value={m.note ?? ''} maxLength={200} onChange={(e) => setMapping(i, { note: e.currentTarget.value })} />
                    </Table.Td>
                    <Table.Td className={c.mapActions}>
                      <Button size="small" variant="ghost" icon={ArrowUp} aria-label={`Move row ${i + 1} up`} disabled={i === 0}
                        className={i === 0 ? c.hiddenAction : undefined} onClick={() => setDoc({ ...doc, model_mappings: move(doc.model_mappings, i, -1) })} />
                      <Button size="small" variant="ghost" icon={ArrowDown} aria-label={`Move row ${i + 1} down`} disabled={i === doc.model_mappings.length - 1}
                        className={i === doc.model_mappings.length - 1 ? c.hiddenAction : undefined}
                        onClick={() => setDoc({ ...doc, model_mappings: move(doc.model_mappings, i, 1) })} />
                      <Button size="small" variant="ghost" icon={Trash2} aria-label={`Remove row ${i + 1}`}
                        onClick={() => setDoc({ ...doc, model_mappings: doc.model_mappings.filter((_, j) => j !== i) })} />
                    </Table.Td>
                  </Table.Tr>
                ))}
                <Table.Tr>
                  <Table.Td>
                    <Text size="sm" fw={500}>
                      Anything else
                    </Text>
                  </Table.Td>
                  <Table.Td colSpan={3}>
                    <Select aria-label="Anything else goes to" value={doc.default_alias_id} allowDeselect={false}
                      data={[{ value: '', label: 'Reject the request' }, ...aliasOptions.slice(1)]} onChange={(v) => setDoc({ ...doc, default_alias_id: v ?? '' })} />
                  </Table.Td>
                </Table.Tr>
              </Table.Tbody>
            </Table>
            <div>
              <Button size="small" icon={Plus} onClick={() => setDoc({ ...doc, model_mappings: [...doc.model_mappings, { match: '', alias_id: '', note: '' }] })}>
                Add a row
              </Button>
            </div>
            {!doc.default_alias_id && doc.model_mappings.length === 0 && (
              <Banner tone="warning">No rows and nothing for other models: every request on this plan would be rejected.</Banner>
            )}
          </Stack>

          <Stack gap="xs">
            <Title order={4}>If an attempt fails</Title>
            <Text size="xs" c="dimmed">
              Steps run in order, only before anything reaches the client and only when the failed attempt provably did not run. After the last step the request fails.
            </Text>
            {steps.length === 0 ? (
              <Text size="sm">No fallback: the request fails on the first error.</Text>
            ) : (
              <ol className={c.steps} aria-label="Fallback steps in order">
                {steps.map((st, i) => (
                  <li key={`${st}-${i}`} className={c.step}>
                    <span>
                      {i + 1}. {stepLabels[st] ?? st}
                    </span>
                    <Button size="small" variant="ghost" icon={ArrowUp} aria-label={`Move ${stepLabels[st] ?? st} up`} disabled={i === 0}
                      className={i === 0 ? c.hiddenAction : undefined} onClick={() => setSteps(move(steps, i, -1))} />
                    <Button size="small" variant="ghost" icon={ArrowDown} aria-label={`Move ${stepLabels[st] ?? st} down`} disabled={i === steps.length - 1}
                      className={i === steps.length - 1 ? c.hiddenAction : undefined} onClick={() => setSteps(move(steps, i, 1))} />
                    <Button size="small" variant="ghost" icon={Trash2} aria-label={`Remove ${stepLabels[st] ?? st}`} onClick={() => setSteps(steps.filter((_, j) => j !== i))} />
                  </li>
                ))}
              </ol>
            )}
            {stepsError && <Banner tone="error">{stepsError}</Banner>}
            {available.length > 0 && (
              <Group gap="xs" align="flex-end">
                <Select label="Add a step" value={stepToAdd} allowDeselect={false} w={320} onChange={(v) => setAddStep(v ?? '')}
                  data={available.map((v) => ({ value: v, label: stepLabels[v] ?? v }))} />
                <Button icon={Plus} onClick={() => stepToAdd && setSteps([...steps, stepToAdd])}>
                  Add
                </Button>
              </Group>
            )}
            <Group gap="md" align="flex-start">
              <NumberField label="Give up after" unit="attempts" min={1} max={10} value={fb.max_attempts || null}
                onChange={(v) => setDoc({ ...doc, fallback: { ...fb, max_attempts: v ?? 0 } })} error={errs['body.document.fallback.max_attempts']} />
              {steps.includes('wait') && (
                <NumberField label="Wait at most" unit="s" min={0} max={600} value={fb.wait_max_seconds}
                  onChange={(v) => setDoc({ ...doc, fallback: { ...fb, wait_max_seconds: v ?? 0 } })} error={errs['body.document.fallback.wait_max_seconds']} />
              )}
            </Group>
            <Checkbox label="Allow paid fallback (from subscription, free or local accounts to pay-per-use API accounts)" checked={fb.allow_paid_fallback}
              onChange={(v) => setDoc({ ...doc, fallback: { ...fb, allow_paid_fallback: v } })} />
            {fb.allow_paid_fallback && <Banner tone="warning">Requests may incur metered API charges when subscription or local capacity is unavailable.</Banner>}
          </Stack>

          <Stack gap="sm">
            <Title order={4}>Hard restrictions</Title>
            <Text size="xs" c="dimmed">
              Restrictions always apply first; scores and fallback can never get around them.
            </Text>
            <Stack gap={6}>
              <Text size="sm" fw={600}>
                Providers
              </Text>
              <SegmentedControl value={providerMode} onChange={(v) => setProviderMode(v as ProviderMode)} w="fit-content"
                data={[{ value: 'any', label: 'Any' }, { value: 'only', label: 'Only these' }, { value: 'except', label: 'All except these' }]} />
              {providerMode !== 'any' && (
                <MultiSelect aria-label={providerMode === 'only' ? 'Allowed providers' : 'Denied providers'} data={providerOptions} searchable
                  value={providerMode === 'only' ? r.allowed_provider_ids : r.denied_provider_ids} placeholder="Choose providers"
                  onChange={(v) => setDoc({ ...doc, restrictions: { ...r, allowed_provider_ids: providerMode === 'only' ? v : [], denied_provider_ids: providerMode === 'except' ? v : [] } })} />
              )}
            </Stack>
            <Checks legend="Billing (none ticked = any)" options={billingClasses} value={r.allowed_billing}
              onChange={(v) => setDoc({ ...doc, restrictions: { ...r, allowed_billing: v } })} />
            <Checks legend="Provider kind (none ticked = any)" options={classificationOptions} value={r.allowed_classifications}
              onChange={(v) => setDoc({ ...doc, restrictions: { ...r, allowed_classifications: v } })} />
            <Checks legend="Every account must support" options={capabilities.map((x) => ({ value: x, label: capabilityLabels[x] ?? x }))} value={r.required_capabilities}
              onChange={(v) => setDoc({ ...doc, restrictions: { ...r, required_capabilities: v } })} />
          </Stack>
        </Stack>
      )}

      <MutationError error={error} />
      <DialogActions>
        <TextInput aria-label="Describe this change" placeholder="Describe this change (optional)" value={note} maxLength={500}
          onChange={(e) => setNote(e.currentTarget.value)} className={c.noteInput} />
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={busy} disabled={incomplete} title={incomplete ? 'Every row needs a requested model and a model name.' : undefined} onClick={save}>
          Save draft
        </Button>
      </DialogActions>
    </Dialog>
  )
}
