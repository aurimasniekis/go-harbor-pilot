import { useState } from 'react'
import { createFileRoute, Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { ArrowRight, Check, Compass, Copy, OctagonAlert } from 'lucide-react'
import { PageHeader } from '@/components/PageHeader'
import { Badge, Banner, Button, EmptyState, KeyValues, Panel, Skeleton, TableScroll, type Tone } from '@/components/ui'
import { QueryError } from '@/components/states'
import { BillingBadge } from '@/components/ResourceBadges'
import { ApiError } from '@/lib/api/client'
import { requestQuery, resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type {
  AccountRes,
  Attempt,
  BindingRes,
  Decision,
  LedgerEntry,
  PlanRes,
  ProjectRes,
  ProviderRes,
  RequestDetail,
  TranslationLoss,
  WorkspaceRes,
} from '@/lib/api/types'
import { fmtDateTime, fmtRelative } from '@/lib/format'
import {
  ConfidenceBadge,
  CostKindLabel,
  CostValue,
  HarnessBadge,
  MapTable,
  ModeBadge,
  Ms,
  ProtocolBadge,
  StatusBadge,
  TokenValue,
} from '@/features/activity/bits'

export const Route = createFileRoute('/activity/$requestId')({
  component: RequestView,
})

function asDecision(v: unknown): Decision {
  return v && typeof v === 'object' ? (v as Decision) : {}
}

function asLosses(v: unknown): TranslationLoss[] {
  return Array.isArray(v) ? (v as TranslationLoss[]) : []
}

function CopyId({ id }: { id: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
      <span className="mono">{id}</span>
      <Button
        size="small"
        variant="ghost"
        icon={copied ? Check : Copy}
        aria-label="Copy request id"
        onClick={() => void navigator.clipboard?.writeText(id).then(() => setCopied(true))}
      />
    </span>
  )
}

function Names() {
  const providers = useQuery(resourceListQuery<ProviderRes>('providers'))
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const bindings = useQuery(resourceListQuery<BindingRes>('client-bindings'))
  const projects = useQuery(resourceListQuery<ProjectRes>('projects'))
  const workspaces = useQuery(resourceListQuery<WorkspaceRes>('workspaces'))
  const plans = useQuery(resourceListQuery<PlanRes>('routing-plans'))
  return {
    provider: byId(providers.data?.items),
    account: byId(accounts.data?.items),
    binding: byId(bindings.data?.items),
    project: byId(projects.data?.items),
    workspace: byId(workspaces.data?.items),
    plan: byId(plans.data?.items),
  }
}

function name(m: Record<string, { name: string }>, id: string | undefined) {
  if (!id) return <span className="muted">—</span>
  return m[id]?.name ?? <span className="mono">{id}</span>
}

function RequestView() {
  const { requestId } = Route.useParams()
  const q = useQuery({ ...requestQuery(requestId), retry: false })
  const names = Names()
  if (q.isError) {
    if (q.error instanceof ApiError && q.error.status === 404) {
      return (
        <Panel>
          <EmptyState icon={Compass} title="Request not found" action={<Link to="/activity">Back to activity</Link>}>
            This request id is unknown or its activity record has expired under the retention policy.
          </EmptyState>
        </Panel>
      )
    }
    return <QueryError error={q.error} retry={() => void q.refetch()} />
  }
  if (!q.data) return <Skeleton height={240} />
  const r = q.data
  const d = asDecision(r.decision)
  const losses = asLosses(r.losses)
  return (
    <>
      <p>
        <Link to="/activity">← All requests</Link>
      </p>
      <PageHeader
        eyebrow="Why this route?"
        title={
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
            <StatusBadge status={r.status} /> <span className="mono">{r.requested_model || 'request'}</span>
          </span>
        }
        subtitle={
          <span style={{ display: 'inline-flex', gap: 16, flexWrap: 'wrap' }}>
            <CopyId id={r.id} />
            <span title={fmtDateTime(r.created_at, { seconds: true })}>{fmtRelative(r.created_at)}</span>
            <span>
              TTFT <Ms v={r.ttft_ms} />
            </span>
            <span>
              Total <Ms v={r.duration_ms} />
            </span>
          </span>
        }
      />
      {(r.status === 'blocked' || r.status === 'error') && r.error_message && (
        <Banner tone={r.status === 'blocked' ? 'warning' : 'error'} icon={OctagonAlert} role="status">
          <strong>{r.status === 'blocked' ? 'Blocked by policy' : 'Failed'}{r.error_kind ? ` (${r.error_kind})` : ''}:</strong> {r.error_message}
          {r.status === 'blocked' && (d.excluded?.length ?? 0) > 0 && ' — see Excluded candidates below for the reasons.'}
        </Banner>
      )}
      <div style={{ display: 'grid', gap: 'var(--space-4)', gridTemplateColumns: 'repeat(auto-fit, minmax(340px, 1fr))' }}>
        <TrustedContext r={r} names={names} />
        <Panel title="Client claims (untrusted)" subtitle="Hints sent by the client. They are recorded for context and never authorize anything, raise limits or select accounts.">
          <MapTable m={r.claims} label="Client claims" empty="The client sent no hints." />
        </Panel>
      </div>
      <Resolution r={r} d={d} names={names} />
      <Candidates d={d} />
      <Attempts attempts={r.attempt_list} names={names} />
      <div style={{ display: 'grid', gap: 'var(--space-4)', gridTemplateColumns: 'repeat(auto-fit, minmax(340px, 1fr))' }}>
        <Panel title="Tools" subtitle="A generated tool call is not proof the tool executed; results are only seen when the client sends them back.">
          <KeyValues
            items={[
              ['Tools offered', <span className="num">{r.tools_offered}</span>],
              ['Tool calls generated', <span className="num">{r.tool_calls}</span>],
              ['Tool results seen', <span className="num">{r.tool_results_seen}</span>],
              ['Tool results flagged as errors', <span className="num">{r.tool_result_errors}</span>],
              ['Tool names', r.tool_names.length ? r.tool_names.map((t) => <Badge key={t}>{t}</Badge>) : <span className="muted">—</span>],
            ]}
          />
        </Panel>
        <Panel title="Translation losses" subtitle="Features that could not be carried to the upstream protocol, and how they were handled.">
          {losses.length === 0 ? (
            <p className="secondary">No features were lost.</p>
          ) : (
            <TableScroll label="Translation losses">
              <table className="data-table" style={{ width: '100%' }}>
                <thead>
                  <tr>
                    <th>Feature</th>
                    <th>Severity</th>
                    <th>Detail</th>
                  </tr>
                </thead>
                <tbody>
                  {losses.map((l) => (
                    <tr key={l.feature}>
                      <td className="mono">{l.feature}</td>
                      <td>
                        <Badge tone={l.severity === 'reject' ? 'error' : l.severity === 'drop' ? 'warning' : 'info'}>{l.severity}</Badge>
                      </td>
                      <td>{l.detail}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </TableScroll>
          )}
        </Panel>
      </div>
      <Accounting ledger={r.ledger} names={names} />
    </>
  )
}

type NameMaps = ReturnType<typeof Names>

function TrustedContext({ r, names }: { r: RequestDetail; names: NameMaps }) {
  return (
    <Panel title="Trusted context" subtitle="Identity derived from the gateway credential, its client binding and operator-configured references.">
      <KeyValues
        items={[
          ['Client', r.binding_id ? <Link to="/clients/$bindingId" params={{ bindingId: r.binding_id }}>{name(names.binding, r.binding_id)}</Link> : <span className="muted">—</span>],
          ['Credential', r.credential_id ? <span className="mono">{r.credential_id}</span> : <span className="muted">—</span>],
          ['Project', name(names.project, r.project_id)],
          ['Workspace', name(names.workspace, r.workspace_id)],
          [
            'Session',
            r.session_id ? (
              <span>
                <Link to="/activity" search={{ session_id: r.session_id }} className="mono">
                  {r.session_id}
                </Link>{' '}
                <ConfidenceBadge c={r.session_confidence} />
              </span>
            ) : (
              <span>
                <ConfidenceBadge c={r.session_confidence} /> <span className="muted">no session evidence</span>
              </span>
            ),
          ],
          [
            'Harness',
            <span>
              <HarnessBadge h={r.harness} /> {r.harness_version && <span className="mono">{r.harness_version}</span>}{' '}
              <ConfidenceBadge c={r.harness_confidence} />
              <div className="muted">Heuristic, not proof of identity.</div>
            </span>,
          ],
          ['Protocol', <span><ProtocolBadge p={r.inbound_protocol} /> {r.stream ? <Badge tone="info">stream</Badge> : <Badge>non-stream</Badge>}</span>],
        ]}
      />
      <h3 style={{ fontSize: 'var(--text-sm)', margin: '12px 0 6px' }}>Trusted references</h3>
      <MapTable m={r.trusted_refs} label="Trusted references" empty="None." />
    </Panel>
  )
}

function Step({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 2, minWidth: 120 }}>
      <span className="muted" style={{ fontSize: 'var(--text-xs)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>
        {label}
      </span>
      <span>{children}</span>
    </div>
  )
}

function Arrow() {
  return <ArrowRight size={16} color="var(--text-muted)" aria-hidden="true" style={{ alignSelf: 'center' }} />
}

function Resolution({ r, d, names }: { r: RequestDetail; d: Decision; names: NameMaps }) {
  const resolution = r.resolution || d.resolution || ''
  const resolutionLabel = resolution === 'alias_name' ? 'alias name' : resolution === 'default_alias' ? 'plan default alias' : resolution.startsWith('mapping:') ? `mapping ${resolution.slice(8)}` : resolution || '—'
  const planId = r.plan_id || d.plan_id
  return (
    <Panel title="Model resolution" subtitle="How the requested model name became an upstream account and model.">
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12, alignItems: 'flex-start' }}>
        <Step label="Requested">
          <span className="mono">{r.requested_model || '—'}</span>
        </Step>
        <Arrow />
        <Step label="Matched by">{resolutionLabel}</Step>
        <Arrow />
        <Step label="Alias">
          <span className="mono">{d.alias || '—'}</span>
          {(d.alias_chain?.length ?? 0) > 1 && <div className="muted mono">{d.alias_chain!.join(' → ')}</div>}
        </Step>
        <Arrow />
        <Step label="Pool">
          <span className="mono">{d.pool || '—'}</span>
          {d.strategy && <div className="muted">{d.strategy}</div>}
        </Step>
        <Arrow />
        <Step label="Plan">
          {planId ? (
            <Link to="/routing/plans/$planId" params={{ planId }}>
              {name(names.plan, planId)}
            </Link>
          ) : (
            <span className="muted">—</span>
          )}
          {(r.plan_version ?? d.plan_version) ? <div className="muted">version {r.plan_version ?? d.plan_version}</div> : null}
        </Step>
        <Arrow />
        <Step label="Served by">
          {r.final_account_id ? (
            <>
              {name(names.provider, r.final_provider_id)} / {name(names.account, r.final_account_id)}
              <div className="mono">{r.final_model}</div>
              <ModeBadge mode={r.mode} />
            </>
          ) : (
            <span className="muted">not served</span>
          )}
        </Step>
      </div>
      <p style={{ marginTop: 12 }}>
        <span className="muted">Required capabilities: </span>
        {r.required_capabilities.length ? r.required_capabilities.map((c) => <Badge key={c}>{c}</Badge>) : <span className="muted">none</span>}
        {d.config_revision ? <span className="muted"> · configuration revision {d.config_revision}</span> : null}
      </p>
    </Panel>
  )
}

const stageTone: Record<string, Tone> = { restriction: 'neutral', capability: 'info', health: 'warning', policy: 'error' }

function Candidates({ d }: { d: Decision }) {
  const cands = d.candidates ?? []
  const excl = d.excluded ?? []
  return (
    <Panel title="Candidates" subtitle="Eligible pool members in the order they would be tried, and every member that was excluded before ranking.">
      {(d.notes?.length ?? 0) > 0 && (
        <ul style={{ margin: '0 0 12px', paddingLeft: 18 }}>
          {d.notes!.map((n) => (
            <li key={n} className="secondary">
              {n}
            </li>
          ))}
        </ul>
      )}
      {d.affinity && <p className="secondary">Session affinity: {d.affinity}</p>}
      {cands.length === 0 ? (
        <p className="secondary">No eligible candidates were found.</p>
      ) : (
        <TableScroll label="Candidates">
          <table className="data-table" style={{ width: '100%' }}>
            <thead>
              <tr>
                <th>#</th>
                <th>Provider / account</th>
                <th>Model</th>
                <th>Billing</th>
                <th>Upstream</th>
                <th>Reasons</th>
                <th>Health</th>
              </tr>
            </thead>
            <tbody>
              {cands.map((c, i) => (
                <tr key={c.account_id + c.model}>
                  <td className="num">{i + 1}</td>
                  <td>
                    {c.provider} / {c.account}
                  </td>
                  <td className="mono">{c.model}</td>
                  <td>
                    <BillingBadge value={c.billing} />
                  </td>
                  <td>
                    <ProtocolBadge p={c.upstream_protocol} /> {c.native ? <Badge tone="success">native</Badge> : <Badge tone="info">translated</Badge>}
                  </td>
                  <td>{(c.reasons ?? []).length ? (c.reasons ?? []).map((x) => <div key={x} className="secondary">{x}</div>) : <span className="muted">—</span>}</td>
                  <td>
                    {c.health ? (
                      <span>
                        {c.health.Available === false ? <Badge tone="error">unavailable</Badge> : <Badge tone="success">available</Badge>}{' '}
                        {c.health.Stale && <Badge tone="warning">stale quota</Badge>}
                      </span>
                    ) : (
                      <span className="muted">—</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableScroll>
      )}
      <h3 style={{ fontSize: 'var(--text-sm)', margin: '16px 0 6px' }}>Excluded</h3>
      {excl.length === 0 ? (
        <p className="secondary">No pool member was excluded.</p>
      ) : (
        <TableScroll label="Excluded candidates">
          <table className="data-table" style={{ width: '100%' }}>
            <thead>
              <tr>
                <th>Account</th>
                <th>Model</th>
                <th>Stage</th>
                <th>Reason</th>
                <th>Detail</th>
              </tr>
            </thead>
            <tbody>
              {excl.map((x, i) => (
                <tr key={i}>
                  <td>{x.account || <span className="mono">{x.account_id}</span>}</td>
                  <td className="mono">{x.model}</td>
                  <td>
                    <Badge tone={stageTone[x.stage] ?? 'neutral'}>{x.stage}</Badge>
                  </td>
                  <td className="mono">{x.reason}</td>
                  <td>{x.detail || <span className="muted">—</span>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableScroll>
      )}
    </Panel>
  )
}

function Attempts({ attempts, names }: { attempts: Attempt[]; names: NameMaps }) {
  return (
    <Panel title="Attempts" subtitle="Upstream attempts in order. Failover happens only before any output reaches the client and only as the plan's fallback steps allow.">
      {attempts.length === 0 ? (
        <p className="secondary">No upstream attempt was made.</p>
      ) : (
        <ol style={{ listStyle: 'none', padding: 0, margin: 0, display: 'flex', flexDirection: 'column', gap: 12 }}>
          {attempts.map((a) => (
            <li key={a.id} style={{ borderLeft: `3px solid var(--${a.status === 'ok' ? 'success' : a.status === 'error' ? 'error' : 'border'})`, paddingLeft: 12 }}>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center' }}>
                <strong>#{a.n}</strong>
                <span>
                  {name(names.provider, a.provider_id)} / {name(names.account, a.account_id)} <span className="mono">{a.model}</span>
                </span>
                <ProtocolBadge p={a.upstream_protocol} />
                <ModeBadge mode={a.mode} />
                <StatusBadge status={a.status} />
                {a.failover_reason && <Badge tone="warning" title="Fallback step that permitted this attempt">{a.failover_reason}</Badge>}
              </div>
              <div className="secondary" style={{ display: 'flex', flexWrap: 'wrap', gap: 16, marginTop: 4 }}>
                <span>
                  TTFT <Ms v={a.ttft_ms} />
                </span>
                <span>
                  Duration <Ms v={a.duration_ms} />
                </span>
                {a.http_status ? <span>HTTP {a.http_status}</span> : null}
                {a.retry_after_s ? <span>Retry after {a.retry_after_s}s</span> : null}
                <span>
                  Tokens <TokenValue v={a.tokens.input} /> / <TokenValue v={a.tokens.output} />
                </span>
              </div>
              {(a.error_kind || a.error_message) && (
                <div style={{ marginTop: 4 }}>
                  <Badge tone="error">{a.error_kind || 'error'}</Badge> {a.error_message}
                </div>
              )}
              {a.executed && a.status !== 'ok' && (
                <div className="muted" style={{ marginTop: 4 }}>
                  The upstream may have processed this attempt; it is never replayed automatically.
                </div>
              )}
              {a.unknown_events.length > 0 && (
                <div style={{ marginTop: 4 }}>
                  <span className="muted">Unrecognized upstream events: </span>
                  {a.unknown_events.map((u) => (
                    <Badge key={u}>{u}</Badge>
                  ))}
                </div>
              )}
            </li>
          ))}
        </ol>
      )}
    </Panel>
  )
}

const sourceTone: Record<string, Tone> = { upstream_reported: 'success', provider_reported: 'success', estimated: 'info', unknown: 'warning', client_reported: 'info' }

function Accounting({ ledger, names }: { ledger: LedgerEntry[]; names: NameMaps }) {
  const listEq = ledger.some((l) => l.cost?.kind === 'list_equivalent')
  return (
    <Panel title="Accounting" subtitle="Append-only ledger entries for this request. Corrections are new entries; nothing is overwritten.">
      {listEq && (
        <Banner tone="info">
          List-price equivalents value subscription usage at public list prices for comparison. They are not invoices and are never added to API charges.
        </Banner>
      )}
      {ledger.length === 0 ? (
        <p className="secondary">No ledger entries (the request did not reach an upstream).</p>
      ) : (
        <TableScroll label="Ledger entries">
          <table className="data-table" style={{ width: '100%' }}>
            <thead>
              <tr>
                <th>Kind</th>
                <th>Account / model</th>
                <th>Billing</th>
                <th>Usage source</th>
                <th>In</th>
                <th>Out</th>
                <th>Cache read</th>
                <th>Cache write</th>
                <th>Reasoning</th>
                <th>Cost</th>
              </tr>
            </thead>
            <tbody>
              {ledger.map((l) => (
                <tr key={l.id}>
                  <td>
                    <Badge tone={l.kind === 'unknown_usage' ? 'warning' : 'neutral'}>{l.kind}</Badge>
                    <details>
                      <summary className="muted">provenance</summary>
                      <pre className="mono" style={{ fontSize: 'var(--text-xs)', whiteSpace: 'pre-wrap', margin: 0 }}>
                        {JSON.stringify(l.provenance, null, 2)}
                      </pre>
                    </details>
                  </td>
                  <td>
                    {name(names.account, l.account_id)} <div className="mono">{l.model}</div>
                  </td>
                  <td>{l.billing ? <BillingBadge value={l.billing} /> : <span className="muted">—</span>}</td>
                  <td>
                    <Badge tone={sourceTone[l.usage_source] ?? 'neutral'}>{l.usage_source.replace('_', ' ')}</Badge>
                  </td>
                  <td>
                    <TokenValue v={l.tokens.input} />
                  </td>
                  <td>
                    <TokenValue v={l.tokens.output} />
                  </td>
                  <td>
                    <TokenValue v={l.tokens.cache_read} />
                  </td>
                  <td>
                    <TokenValue v={l.tokens.cache_write} />
                  </td>
                  <td>
                    <TokenValue v={l.tokens.reasoning} />
                  </td>
                  <td>
                    <CostValue cost={l.cost} />
                    {l.cost?.price_version && <div className="muted">prices {l.cost.price_version}</div>}
                    {l.cost && l.cost.kind === 'none' && <CostKindLabel kind="none" />}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableScroll>
      )}
      <p className="muted" style={{ marginTop: 8 }}>
        Recorded {ledger[0] ? fmtDateTime(ledger[0].at, { seconds: true }) : '—'}. Input tokens exclude cache reads and writes so figures are comparable across providers.
      </p>
    </Panel>
  )
}
