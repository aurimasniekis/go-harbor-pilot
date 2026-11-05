import type { ReactNode } from 'react'
import { Paper, SimpleGrid, Stack, Text } from '@mantine/core'
import { Badge, Banner, KeyValues, Panel, TableScroll } from '@/components/ui'
import { CandidateTable, DecisionSummary, ExclusionTable } from '@/features/routing/DecisionParts'
import { stepWords } from '@/features/routing/names'
import { useCompact } from '@/ui/kit'
import type { Decision } from '@/lib/api/types'
import c from '@/features/config/config.module.css'
import p from '@/features/privacy/privacy.module.css'

/** Shapes written by internal/traces (view()). */
interface TracePart {
  kind: string
  text?: string
  tool_name?: string
  tool_id?: string
  arguments?: string
  media?: { mime_type?: string; bytes?: number; remote?: boolean }
  sealed?: string
}

interface TraceRequest {
  model?: string
  system?: TracePart[]
  messages?: { role: string; parts: TracePart[] }[]
  tools?: string[]
  stream?: boolean
}

interface StreamEvent {
  kind: string
  index: number
  bytes?: number
  stop_reason?: string
}

interface TraceAttempt {
  account_id: string
  provider_id: string
  model: string
  step: string
  error?: string
}

const isTruncated = (v: unknown) => !!v && typeof v === 'object' && (v as { truncated?: boolean }).truncated === true

function Part({ part }: { part: TracePart }) {
  if (part.media) {
    return (
      <div className="muted">
        <Badge>{part.kind}</Badge> {part.media.mime_type || 'attachment'} · {part.media.remote ? 'remote reference' : `${part.media.bytes ?? 0} bytes`} (metadata only)
      </div>
    )
  }
  if (part.sealed) {
    return (
      <div className="muted">
        <Badge>{part.kind}</Badge> {part.sealed}
      </div>
    )
  }
  if (part.tool_name) {
    return (
      <div>
        <Badge tone="purple">tool call</Badge> <span className="mono">{part.tool_name}</span> <span className="mono muted">{part.tool_id}</span>
        <pre className={p.partText}>{part.arguments}</pre>
      </div>
    )
  }
  return (
    <div>
      <Badge>{part.kind.replaceAll('_', ' ')}</Badge>
      {part.tool_id && <span className="mono muted"> for {part.tool_id}</span>}
      <pre className={p.partText}>{part.text}</pre>
    </div>
  )
}

function RequestSection({ title, subtitle, value }: { title: string; subtitle: string; value: unknown }) {
  if (isTruncated(value)) {
    return (
      <Panel title={title}>
        <Banner tone="warning">This section was dropped because the trace exceeded its size limit.</Banner>
      </Panel>
    )
  }
  const r = (value ?? {}) as TraceRequest
  return (
    <Panel title={title} subtitle={subtitle}>
      <div className={p.sections}>
        {(r.model || (r.tools ?? []).length > 0) && (
          <KeyValues
            items={[
              ...(r.model ? ([['Model', <span className="mono">{r.model}</span>]] as [ReactNode, ReactNode][]) : []),
              ...((r.tools ?? []).length ? ([['Tools offered', (r.tools ?? []).map((t) => <Badge key={t}>{t}</Badge>)]] as [ReactNode, ReactNode][]) : []),
            ]}
          />
        )}
        {(r.system ?? []).length > 0 && (
          <div className={p.message}>
            <strong>system</strong>
            {(r.system ?? []).map((pt, i) => (
              <Part key={i} part={pt} />
            ))}
          </div>
        )}
        {(r.messages ?? []).map((m, i) => (
          <div key={i} className={p.message}>
            <strong>{m.role}</strong>
            {m.parts.map((pt, j) => (
              <Part key={j} part={pt} />
            ))}
          </div>
        ))}
      </div>
    </Panel>
  )
}

/** "input_tokens" → "Input tokens"; booleans as Yes/No. */
function usageRows(usage: Record<string, unknown>): [string, ReactNode][] {
  return Object.entries(usage).map(([k, v]) => {
    const label = k.replaceAll('_', ' ')
    const value = typeof v === 'boolean' ? (v ? 'Yes' : 'No') : typeof v === 'number' ? v.toLocaleString('en-GB') : String(v)
    return [label.charAt(0).toUpperCase() + label.slice(1), value]
  })
}

/** Renders a decrypted trace's sections. */
export function TraceSections({ sections, accountName = (id) => id }: { sections: Record<string, unknown>; accountName?: (id: string) => string }) {
  const compact = useCompact()
  const events = sections.stream_events as StreamEvent[] | undefined
  const attempts = sections.attempts as TraceAttempt[] | undefined
  const routing = sections.routing as Decision | undefined
  const usage = sections.usage as Record<string, unknown> | undefined
  return (
    <>
      {'inbound_request' in sections && (
        <RequestSection title="Client request" subtitle="What the client sent, as captured by this trace's mode." value={sections.inbound_request} />
      )}
      {'upstream_request' in sections && (
        <RequestSection title="Request as sent upstream" subtitle="After outbound masking; placeholders stand in for masked values." value={sections.upstream_request} />
      )}
      {'response' in sections && <RequestSection title="Response" subtitle="What the model produced (before placeholders were restored for the client)." value={sections.response} />}
      {Array.isArray(attempts) && (
        <Panel title="Attempts">
          {compact ? (
            <Stack gap={6} aria-label="Attempts">
              {attempts.map((a, i) => (
                <Paper key={i} withBorder p="xs" radius="sm">
                  <Text size="sm" fw={600}>
                    {i + 1}. {accountName(a.account_id)}
                  </Text>
                  <Text size="xs" c="dimmed" style={{ overflowWrap: 'anywhere' }}>
                    {a.model} · {stepWords[a.step] ?? a.step}
                  </Text>
                  {a.error && <Badge tone="error">{a.error}</Badge>}
                </Paper>
              ))}
            </Stack>
          ) : (
            <TableScroll label="Attempts">
              <table className="data-table" style={{ width: '100%' }}>
                <thead>
                  <tr>
                    <th>#</th>
                    <th>Account</th>
                    <th>Model</th>
                    <th>Why this attempt</th>
                    <th>Result</th>
                  </tr>
                </thead>
                <tbody>
                  {attempts.map((a, i) => (
                    <tr key={i}>
                      <td className="num">{i + 1}</td>
                      <td>{accountName(a.account_id)}</td>
                      <td className="mono">{a.model}</td>
                      <td>{stepWords[a.step] ?? a.step}</td>
                      <td>{a.error ? <Badge tone="error">{a.error}</Badge> : <Badge tone="success">OK</Badge>}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </TableScroll>
          )}
        </Panel>
      )}
      {routing && !isTruncated(routing) && (routing.candidates || routing.excluded) && (
        <Panel title="Routing decision">
          <Stack gap="sm">
            <DecisionSummary d={routing} />
            <CandidateTable candidates={routing.candidates ?? []} accountName={accountName} />
            {(routing.excluded ?? []).length > 0 && <ExclusionTable excluded={routing.excluded ?? []} accountName={accountName} />}
          </Stack>
        </Panel>
      )}
      {usage && !isTruncated(usage) && (
        <Panel title="Usage">
          <SimpleGrid cols={{ base: 1, sm: 2 }} spacing={4} verticalSpacing={4}>
            {usageRows(usage).map(([k, v]) => (
              <Text key={k} size="sm">
                <Text span c="dimmed">
                  {k}:
                </Text>{' '}
                <span className="num">{v}</span>
              </Text>
            ))}
          </SimpleGrid>
        </Panel>
      )}
      {Array.isArray(events) && (
        <Panel title="Stream events" subtitle="Event kinds and sizes; content is in the response section.">
          <TableScroll label="Stream events">
            <table className="data-table" style={{ width: '100%' }}>
              <thead>
                <tr>
                  <th>#</th>
                  <th>Kind</th>
                  <th>Part</th>
                  <th>Bytes</th>
                  <th>Stop reason</th>
                </tr>
              </thead>
              <tbody>
                {events.slice(0, 500).map((e, i) => (
                  <tr key={i}>
                    <td className="num">{i + 1}</td>
                    <td>{e.kind}</td>
                    <td className="num">{e.index}</td>
                    <td className="num">{e.bytes ?? ''}</td>
                    <td>{e.stop_reason ?? ''}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </TableScroll>
          {events.length > 500 && <p className="muted">{events.length - 500} more events not shown.</p>}
        </Panel>
      )}
      {Object.keys(sections).length === 0 && <p className={c.legend}>This trace has no sections.</p>}
    </>
  )
}
