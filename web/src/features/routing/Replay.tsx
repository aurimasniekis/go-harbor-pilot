import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { History } from 'lucide-react'
import { Badge, Banner, Button, KeyValues, Panel } from '@/components/ui'
import { MutationError, SelectField } from '@/components/form'
import { apiSend } from '@/lib/api/client'
import type { Decision, ReplayResult } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { CandidateTable, ExclusionTable, RuleTraceTable } from './DecisionParts'
import r from './routing.module.css'

function firstChoice(d: Decision | null | undefined, refused?: boolean): string {
  if (refused) return 'refused'
  const c = d?.candidates?.[0]
  return c ? `${c.provider} / ${c.account} · ${c.model}` : 'no eligible candidate'
}

/**
 * Re-evaluates a recorded request's routing with its recorded inputs against
 * the current configuration. Never sends anything upstream.
 */
export function Replay({ requestId, recorded, accountName }: { requestId: string; recorded: Decision; accountName: (id: string) => string }) {
  const canRead = useCan('activity.read')
  const [healthMode, setHealthMode] = useState<'current' | 'assume_available'>('current')
  const run = useMutation({
    mutationFn: () => apiSend<ReplayResult>('POST', `/activity/requests/${encodeURIComponent(requestId)}/replay`, { health_mode: healthMode }),
  })
  if (!canRead) return null
  const res = run.data
  const replay = res?.replay
  const d = replay?.decision ?? null
  return (
    <Panel
      title="Replay routing"
      subtitle="Re-evaluate this request's routing with its recorded model, protocol, harness, capabilities, tool names and time against the current configuration. Prompt content is never stored or re-sent."
      actions={
        <>
          <SelectField label="Account health" value={healthMode} onChange={setHealthMode}
            options={[{ value: 'current', label: 'Current live health' }, { value: 'assume_available', label: 'Assume all available' }]} />
          <Button icon={History} busy={run.isPending} onClick={() => run.mutate()}>
            Replay
          </Button>
        </>
      }
    >
      <MutationError error={run.error} />
      {!res && !run.error && <p className="secondary">Replay shows whether today's plan and health would route this request the same way.</p>}
      {res && replay && (
        <section className={r.section} aria-label="Replay result" aria-live="polite">
          <div className={r.simLabel} role="status">
            <History size={16} aria-hidden="true" />
            <strong>Replay against current configuration — no upstream was contacted.</strong>
            <span>Plan v{replay.version}</span>
          </div>
          <KeyValues
            items={[
              ['Outcome', res.changed ? <Badge tone="warning">routing changed</Badge> : <Badge tone="success">same routing</Badge>],
              ['Original first choice', firstChoice(recorded)],
              ['Replay first choice', firstChoice(d, Boolean(replay.error))],
              ['Original plan version', recorded.plan_version ? `v${recorded.plan_version}` : '—'],
            ]}
          />
          {replay.error && (
            <Banner tone="error" role="status">
              <strong>Would now be refused{replay.error_kind ? ` (${replay.error_kind})` : ''}:</strong> {replay.error}
            </Banner>
          )}
          <ul style={{ margin: 0, paddingLeft: 18 }}>
            {res.notes.map((n) => (
              <li key={n} className="secondary">
                {n}
              </li>
            ))}
          </ul>
          {d && (
            <>
              <h3 style={{ fontSize: 'var(--text-sm)', margin: 0 }}>Replay candidates</h3>
              <CandidateTable candidates={d.candidates ?? []} label="Replay candidates" showScore />
              <h3 style={{ fontSize: 'var(--text-sm)', margin: 0 }}>Replay exclusions</h3>
              <ExclusionTable excluded={d.excluded ?? []} label="Replay exclusions" />
              {(d.rules?.length ?? 0) > 0 && (
                <>
                  <h3 style={{ fontSize: 'var(--text-sm)', margin: 0 }}>Replay rule evaluations</h3>
                  <RuleTraceTable traces={d.rules ?? []} accountName={accountName} label="Replay rule evaluations" />
                </>
              )}
            </>
          )}
        </section>
      )}
    </Panel>
  )
}
