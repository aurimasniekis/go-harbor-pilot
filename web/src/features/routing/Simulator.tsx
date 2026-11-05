import { Select } from '@mantine/core'
import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { FlaskConical, Play } from 'lucide-react'
import { Banner, Button, TableScroll } from '@/components/ui'
import { Checkbox, MutationError, SelectField, TextField } from '@/components/form'
import { BillingBadge } from '@/components/ResourceBadges'
import { harnesses } from '@/features/config/constants'
import { apiSend } from '@/lib/api/client'
import type { HealthOverride, PlanVersion, SimulateRequest, SimulationResult } from '@/lib/api/types'
import { CandidateTable, DecisionSummary, ExclusionTable, RuleTraceTable } from './DecisionParts'
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
export function Simulator({ planId, versions, accounts, defaultModel, accountName }: {
  planId: string
  versions: PlanVersion[]
  accounts: LaneMember[]
  defaultModel: string
  accountName: (id: string) => string
}) {
  const published = versions.find((v) => v.status === 'published')
  const draft = versions.find((v) => v.status === 'draft')
  const [versionId, setVersionId] = useState(published ? '' : (draft?.id ?? ''))
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
  const row = (id: string): HealthRow => health[id] ?? { choice: 'current', remaining: '', stale: false }
  const setRow = (id: string, p: Partial<HealthRow>) => setHealth({ ...health, [id]: { ...row(id), ...p } })

  const run = useMutation({
    mutationFn: (req: SimulateRequest) => apiSend<SimulationResult>('POST', `/routing-plans/${encodeURIComponent(planId)}/simulate`, req),
  })
  const versionOptions = [
    ...(published ? [{ value: '', label: `Published (v${published.version})` }] : []),
    ...versions.filter((v) => v.status !== 'published').map((v) => ({ value: v.id, label: `v${v.version} (${v.status})` })),
  ]
  const res = run.data
  const d = res?.decision ?? null

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
        <div className={r.simForm}>
          {versionOptions.length > 0 && (
            <SelectField label="Plan version" value={versionId} onChange={setVersionId} options={versionOptions} />
          )}
          <TextField label="Requested model" value={model} onChange={setModel} maxLength={200} placeholder="claude-sonnet-4" />
          <SelectField label="Client protocol" value={protocol} onChange={setProtocol} options={protocols} />
          <SelectField label="Harness" value={harness} onChange={setHarness}
            options={harnesses.map((h) => ({ value: h.value, label: h.value === '' ? 'Generic (not detected)' : h.label }))} />
          <TextField label="Tool names" value={tools} onChange={setTools} placeholder="Read, Edit, Bash" hint="Comma-separated; definitions only." />
          <TextField label="Declared intent" value={intent} onChange={setIntent} maxLength={64} placeholder="review" />
          <SelectField label="Session confidence" value={sessionConfidence} onChange={setSessionConfidence}
            options={[{ value: '', label: 'unknown' }, { value: 'medium', label: 'medium' }, { value: 'high', label: 'high' }]} />
          <SelectField label="Account health" value={healthMode} onChange={setHealthMode}
            options={[{ value: 'current', label: 'Current live health' }, { value: 'assume_available', label: 'Assume all available' }]} />
          <TextField label="Evaluation time (local)" type="datetime-local" value={at} onChange={setAt} hint="Empty = now. Rules see UTC." />
        </div>
        <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap' }}>
          <Checkbox label="Streaming" checked={stream} onChange={setStream} />
          <Checkbox label="Contains images" checked={images} onChange={setImages} />
        </div>
        {accounts.length > 0 && (
          <details>
            <summary>Account health overrides ({Object.values(health).filter((h) => h.choice !== 'current').length} set)</summary>
            <TableScroll label="Account health overrides">
              <table className="data-table" style={{ width: '100%' }}>
                <thead>
                  <tr>
                    <th>Account</th>
                    <th>Billing</th>
                    <th>Health</th>
                    <th>Remaining %</th>
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
                          <Select size="xs" w={170} aria-label={`Health for ${name}`} value={h.choice} allowDeselect={false}
                            onChange={(v) => v && setRow(m.accountId, { choice: v as HealthChoice })}
                            data={[
                              { value: 'current', label: healthMode === 'current' ? 'Current' : 'Assumed available' },
                              { value: 'available', label: 'Available' },
                              { value: 'unavailable', label: 'Unavailable' },
                            ]} />
                        </td>
                        <td>
                          <input className={r.inlineInput} type="number" min={0} max={100} step={1} aria-label={`Remaining quota percent for ${name}`}
                            disabled={h.choice !== 'available'} value={h.remaining} placeholder="unknown"
                            onChange={(e) => setRow(m.accountId, { remaining: e.target.value })} />
                        </td>
                        <td>
                          <input type="checkbox" aria-label={`Stale quota for ${name}`} disabled={h.choice === 'current'} checked={h.stale}
                            onChange={(e) => setRow(m.accountId, { stale: e.target.checked })} />
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </TableScroll>
          </details>
        )}
        <div>
          <Button type="submit" variant="primary" icon={Play} busy={run.isPending} disabled={!model.trim()}>
            Run simulation
          </Button>
        </div>
      </form>
      <MutationError error={run.error} />
      {res && (
        <section className={r.section} aria-label="Simulation result" aria-live="polite">
          <div className={r.simLabel} role="status">
            <FlaskConical size={16} aria-hidden="true" />
            <strong>Simulation — no upstream was contacted.</strong>
            <span>
              Plan v{res.version}
              {d?.config_revision ? `, configuration revision ${d.config_revision}` : ''}
            </span>
          </div>
          {res.error ? (
            <Banner tone="error" role="status">
              <strong>The request would be refused{res.error_kind ? ` (${res.error_kind})` : ''}:</strong> {res.error}
            </Banner>
          ) : (
            d &&
            (d.candidates?.length ?? 0) > 0 && (
              <Banner tone="info">
                First choice: <strong>{d.candidates![0]!.provider} / {d.candidates![0]!.account}</strong> · <span className="mono">{d.candidates![0]!.model}</span>
              </Banner>
            )
          )}
          {d && (
            <>
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
              <h3 style={{ fontSize: 'var(--text-sm)', margin: 0 }}>Candidates in order</h3>
              <CandidateTable candidates={d.candidates ?? []} label="Simulated candidates" showScore />
              <h3 style={{ fontSize: 'var(--text-sm)', margin: 0 }}>Excluded</h3>
              <ExclusionTable excluded={d.excluded ?? []} label="Simulated exclusions" />
              <h3 style={{ fontSize: 'var(--text-sm)', margin: 0 }}>Rule evaluations</h3>
              <RuleTraceTable traces={d.rules ?? []} accountName={accountName} label="Simulated rule evaluations" />
            </>
          )}
        </section>
      )}
    </div>
  )
}
