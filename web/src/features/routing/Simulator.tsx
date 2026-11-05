import {
  Accordion, Alert, Autocomplete, Button as MButton, Checkbox as MCheckbox, Collapse, Group, NumberInput, Paper, SegmentedControl, Select, SimpleGrid, Stack, Text, Title, Tooltip,
} from '@mantine/core'
import { useRef, useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { CheckCircle2, ChevronDown, FlaskConical, Play, XCircle } from 'lucide-react'
import { Button, TableScroll } from '@/components/ui'
import { Checkbox, DateTimeField, MutationError, SelectField, TextField } from '@/components/form'
import { useCompact } from '@/ui/kit'
import { BillingBadge } from '@/components/ResourceBadges'
import { harnesses } from '@/features/config/constants'
import { apiSend } from '@/lib/api/client'
import type { HealthOverride, PlanVersion, SimulateRequest, SimulationResult } from '@/lib/api/types'
import { CandidateTable, DecisionSummary, ExclusionTable, RuleTraceTable, candidateName, errorKindWords } from './DecisionParts'
import type { LaneMember } from './graph'
import r from './routing.module.css'

const protocols: { value: SimulateRequest['protocol']; label: string }[] = [
  { value: 'anthropic_messages', label: 'Anthropic Messages' },
  { value: 'openai_chat', label: 'OpenAI Chat Completions' },
  { value: 'openai_responses', label: 'OpenAI Responses' },
  { value: 'gemini', label: 'Gemini generateContent' },
]

type HealthChoice = 'current' | 'available' | 'unavailable'

interface HealthRow {
  choice: HealthChoice
  remaining: string
  stale: boolean
}

/** Builds the API request from the form state (exported for tests). */
export function buildSimulateRequest(f: {
  versionId: string
  model: string
  protocol: SimulateRequest['protocol']
  harness: string
  stream: boolean
  tools: string
  images: boolean
  intent: string
  sessionConfidence: string
  healthMode: 'current' | 'assume_available'
  at: string
  health: Record<string, HealthRow>
}): SimulateRequest {
  const req: SimulateRequest = { model: f.model.trim(), protocol: f.protocol, stream: f.stream, health_mode: f.healthMode }
  if (f.versionId) req.version_id = f.versionId
  if (f.harness) req.harness = f.harness
  const tools = f.tools.split(/[\s,]+/).map((t) => t.trim()).filter(Boolean)
  if (tools.length) req.tool_names = tools
  if (f.images) req.images = true
  if (f.intent.trim()) req.intent = f.intent.trim()
  if (f.sessionConfidence) req.session_confidence = f.sessionConfidence
  if (f.at) {
    const d = new Date(f.at)
    if (!Number.isNaN(d.getTime())) req.at = d.toISOString()
  }
  const overrides: Record<string, HealthOverride> = {}
  for (const [id, h] of Object.entries(f.health)) {
    if (h.choice === 'current') continue
    const o: HealthOverride = { available: h.choice === 'available' }
    if (h.choice === 'available' && h.remaining.trim() !== '') {
      const pct = Number(h.remaining)
      if (Number.isFinite(pct)) o.remaining = Math.min(Math.max(pct, 0), 100) / 100
    }
    if (h.stale) o.stale = true
    if (h.choice === 'unavailable') o.reason = 'simulated unavailable'
    overrides[id] = o
  }
  if (Object.keys(overrides).length) req.health_overrides = overrides
  return req
}

/**
 * Routes a synthetic request through a plan version. Nothing is sent
 * upstream; the result is labelled as a simulation everywhere it appears.
 */
export function Simulator({ planId, versions, accounts, defaultModel, accountName, modelSuggestions = [] }: {
  planId: string
  versions: PlanVersion[]
  accounts: LaneMember[]
  defaultModel: string
  accountName: (id: string) => string
  /** Mapping patterns and model names offered while typing. */
  modelSuggestions?: string[]
}) {
  const compact = useCompact()
  const published = versions.find((v) => v.status === 'published')
  const draft = versions.find((v) => v.status === 'draft')
  const [versionId, setVersionId] = useState(draft?.id ?? '')
  const [model, setModel] = useState(defaultModel)
  const [protocol, setProtocol] = useState<SimulateRequest['protocol']>('anthropic_messages')
  const [harness, setHarness] = useState('claude_code')
  const [stream, setStream] = useState(true)
  const [tools, setTools] = useState('')
  const [images, setImages] = useState(false)
  const [intent, setIntent] = useState('')
  const [sessionConfidence, setSessionConfidence] = useState('')
  const [healthMode, setHealthMode] = useState<'current' | 'assume_available'>('current')
  const [at, setAt] = useState('')
  const [health, setHealth] = useState<Record<string, HealthRow>>({})
  const [more, setMore] = useState(false)
  // Kept across runs so "Why" stays open while trying variations; opened for refusals.
  const [why, setWhy] = useState<string | null>(null)
  const resultRef = useRef<HTMLElement>(null)
  const row = (id: string): HealthRow => health[id] ?? { choice: 'current', remaining: '', stale: false }
  const setRow = (id: string, p: Partial<HealthRow>) => setHealth({ ...health, [id]: { ...row(id), ...p } })

  const run = useMutation({
    mutationFn: (req: SimulateRequest) => apiSend<SimulationResult>('POST', `/routing-plans/${encodeURIComponent(planId)}/simulate`, req),
    onSuccess: (result) => {
      if (result.error) setWhy('why')
      requestAnimationFrame(() => resultRef.current?.scrollIntoView?.({ block: 'start', behavior: 'smooth' }))
    },
  })
  // Live and the pending draft as a switch; older versions behind it.
  const versionData = [
    ...(draft ? [{ value: draft.id, label: `Draft v${draft.version}` }] : []),
    ...(published ? [{ value: '', label: `Live v${published.version}` }] : []),
  ]
  const others = versions.filter((v) => v.status !== 'published' && v.status !== 'draft')
  const res = run.data
  const d = res?.decision ?? null
  const overrides = Object.values(health).filter((h) => h.choice !== 'current').length
  const missingModel = !model.trim()
  const harnessOptions = harnesses.filter((h) => h.value !== '').map((h) => ({ value: h.value, label: h.label }))
  const first = d?.candidates?.[0]

  return (
    <div className={r.section}>
      <form
        className={r.section}
        onSubmit={(e) => {
          e.preventDefault()
          run.mutate(
            buildSimulateRequest({ versionId, model, protocol, harness, stream, tools, images, intent, sessionConfidence, healthMode, at, health }),
          )
        }}
      >
        <Group gap="sm" align="flex-end" wrap="wrap">
          <Autocomplete label="Ask for model" value={model} onChange={setModel} data={modelSuggestions.filter(Boolean)} maxLength={200} w={compact ? '100%' : 280}
            placeholder="e.g. claude-sonnet-4" />
          {versionData.length > 1 ? (
            <Stack gap={4}>
              <Text size="sm" fw={500}>
                On
              </Text>
              <SegmentedControl value={versionId} onChange={setVersionId} data={versionData} aria-label="Plan version" />
            </Stack>
          ) : (
            versionData[0] && (
              <Text size="sm" c="dimmed" pb={8}>
                on {versionData[0].label}
              </Text>
            )
          )}
          <Tooltip label="Enter a model to test." disabled={!missingModel} withinPortal>
            <span>
              <Button type="submit" variant="primary" icon={Play} busy={run.isPending} disabled={missingModel}>
                Test
              </Button>
            </span>
          </Tooltip>
        </Group>
        {others.length > 0 && (
          <Select label="Or an older version" size="xs" w={240} clearable placeholder="Choose a replaced version"
            value={others.some((v) => v.id === versionId) ? versionId : null} onChange={(v) => setVersionId(v ?? (draft?.id ?? ''))}
            data={others.map((v) => ({ value: v.id, label: `v${v.version} (replaced)` }))} />
        )}
        <div>
          <MButton variant="subtle" size="compact-sm" onClick={() => setMore((m) => !m)} aria-expanded={more}
            rightSection={<ChevronDown size={14} style={{ transform: more ? 'rotate(180deg)' : undefined }} />}>
            More conditions{overrides ? ` (${overrides} account ${overrides === 1 ? 'override' : 'overrides'})` : ''}
          </MButton>
        </div>
        <Collapse expanded={more}>
          <Stack gap="sm">
            <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="sm" verticalSpacing="sm" className={r.simGrid}>
              <SelectField label="Tool" value={harness} onChange={setHarness} options={harnessOptions} />
              <SelectField label="Client protocol" value={protocol} onChange={setProtocol} options={protocols} />
              <TextField label="Tool names the client offers" value={tools} onChange={setTools} placeholder="e.g. Read, Edit, Bash" />
              <TextField label="Declared intent" value={intent} onChange={setIntent} maxLength={64} placeholder="e.g. review" />
              <SelectField label="Session confidence" value={sessionConfidence} onChange={setSessionConfidence}
                options={[{ value: '', label: 'Unknown' }, { value: 'medium', label: 'Medium' }, { value: 'high', label: 'High' }]} />
              <SelectField label="Account health" value={healthMode} onChange={setHealthMode}
                options={[{ value: 'current', label: 'Current live health' }, { value: 'assume_available', label: 'Assume every account is available' }]} />
              <DateTimeField label="Evaluation time" value={at} onChange={setAt} placeholder="Now" />
            </SimpleGrid>
            <Group gap="lg">
              <Checkbox label="Streaming" checked={stream} onChange={setStream} />
              <Checkbox label="Contains images" checked={images} onChange={setImages} />
            </Group>
            {accounts.length > 0 && (
              <Stack gap={6}>
                <Text size="sm" fw={600}>
                  Account health overrides
                </Text>
                {compact ? (
                  accounts.map((m) => (
                    <Paper key={m.accountId} withBorder p="xs" radius="sm">
                      <HealthControls name={accountName(m.accountId)} h={row(m.accountId)} healthMode={healthMode} onChange={(p) => setRow(m.accountId, p)} />
                    </Paper>
                  ))
                ) : (
                  <TableScroll label="Account health overrides">
                    <table className="data-table" style={{ width: '100%' }}>
                      <thead>
                        <tr>
                          <th>Account</th>
                          <th>Billing</th>
                          <th>Health</th>
                          <th>Quota left</th>
                          <th>Stale quota</th>
                        </tr>
                      </thead>
                      <tbody>
                        {accounts.map((m) => {
                          const h = row(m.accountId)
                          const name = accountName(m.accountId)
                          return (
                            <tr key={m.accountId}>
                              <td>{name}</td>
                              <td>{m.account && <BillingBadge value={m.account.spec.billing} />}</td>
                              <td>
                                <HealthSelect name={name} h={h} healthMode={healthMode} onChange={(p) => setRow(m.accountId, p)} />
                              </td>
                              <td>
                                <NumberInput size="xs" w={110} min={0} max={100} suffix="%" allowDecimal={false} placeholder="unknown" hideControls
                                  aria-label={`Quota left for ${name}`} disabled={h.choice !== 'available'} value={h.remaining}
                                  onChange={(v) => setRow(m.accountId, { remaining: v === '' ? '' : String(v) })} />
                              </td>
                              <td>
                                <MCheckbox size="xs" aria-label={`Stale quota for ${name}`} disabled={h.choice === 'current'} checked={h.stale}
                                  onChange={(e) => setRow(m.accountId, { stale: e.currentTarget.checked })} />
                              </td>
                            </tr>
                          )
                        })}
                      </tbody>
                    </table>
                  </TableScroll>
                )}
              </Stack>
            )}
          </Stack>
        </Collapse>
      </form>
      <MutationError error={run.error} />
      {res && (
        <section ref={resultRef} className={r.section} aria-label="Simulation result" aria-live="polite">
          {res.error ? (
            <Alert color="red" icon={<XCircle size={18} />} title={errorKindWords[res.error_kind ?? ''] ?? 'The request would be refused'}>
              {res.error}
            </Alert>
          ) : first ? (
            <Alert color="teal" icon={<CheckCircle2 size={18} />} title={`Goes to ${accountName(first.account_id) || candidateName(first)} · ${first.model}`}>
              {(d?.candidates?.length ?? 0) > 1
                ? `If that fails: ${d!.candidates!.slice(1).map((c) => accountName(c.account_id) || candidateName(c)).join(', then ')}.`
                : 'No other account would be tried.'}
            </Alert>
          ) : (
            <Alert color="yellow" title="No account could serve this request" />
          )}
          <Text size="xs" c="dimmed">
            <FlaskConical size={12} style={{ verticalAlign: '-1px' }} /> Simulation on plan v{res.version}; no provider was contacted.
          </Text>
          {d && (
            <Accordion variant="separated" radius="md" value={why} onChange={setWhy}>
              <Accordion.Item value="why">
                <Accordion.Control>Why</Accordion.Control>
                <Accordion.Panel>
                  <Stack gap="sm">
                    <DecisionSummary d={d} />
                    {(d.notes?.length ?? 0) > 0 && (
                      <ul style={{ margin: 0, paddingLeft: 18 }}>
                        {d.notes!.map((n) => (
                          <li key={n} className="secondary">
                            {n}
                          </li>
                        ))}
                      </ul>
                    )}
                    <Title order={5}>Accounts in the order they would be tried</Title>
                    <CandidateTable candidates={d.candidates ?? []} label="Simulated candidates" showScore accountName={accountName} />
                    <Title order={5}>Skipped</Title>
                    <ExclusionTable excluded={d.excluded ?? []} label="Simulated exclusions" accountName={accountName} />
                    <Title order={5}>Rules</Title>
                    <RuleTraceTable traces={d.rules ?? []} accountName={accountName} label="Simulated rule evaluations" />
                  </Stack>
                </Accordion.Panel>
              </Accordion.Item>
            </Accordion>
          )}
        </section>
      )}
    </div>
  )
}

function HealthSelect({ name, h, healthMode, onChange }: { name: string; h: HealthRow; healthMode: string; onChange: (p: Partial<HealthRow>) => void }) {
  return (
    <Select size="xs" w={170} aria-label={`Health for ${name}`} value={h.choice} allowDeselect={false} onChange={(v) => v && onChange({ choice: v as HealthChoice })}
      data={[
        { value: 'current', label: healthMode === 'current' ? 'Current' : 'Assumed available' },
        { value: 'available', label: 'Available' },
        { value: 'unavailable', label: 'Unavailable' },
      ]} />
  )
}

/** Stacked health controls for one account (phones). */
function HealthControls({ name, h, healthMode, onChange }: { name: string; h: HealthRow; healthMode: string; onChange: (p: Partial<HealthRow>) => void }) {
  return (
    <Stack gap={6}>
      <Text size="sm" fw={600}>
        {name}
      </Text>
      <HealthSelect name={name} h={h} healthMode={healthMode} onChange={onChange} />
      <Group gap="sm">
        <NumberInput size="xs" w={110} min={0} max={100} suffix="%" allowDecimal={false} placeholder="unknown" hideControls aria-label={`Quota left for ${name}`}
          disabled={h.choice !== 'available'} value={h.remaining} onChange={(v) => onChange({ remaining: v === '' ? '' : String(v) })} />
        <MCheckbox size="xs" label="Stale quota" disabled={h.choice === 'current'} checked={h.stale} onChange={(e) => onChange({ stale: e.currentTarget.checked })} />
      </Group>
    </Stack>
  )
}
