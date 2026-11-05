import { Badge, TableScroll, type Tone } from '@/components/ui'
import { BillingBadge } from '@/components/ResourceBadges'
import { ProtocolBadge } from '@/features/activity/bits'
import type { Decision, DecisionCandidate, DecisionExclusion, RuleTrace } from '@/lib/api/types'

const stageTone: Record<string, Tone> = { restriction: 'neutral', capability: 'info', health: 'warning', policy: 'error', rule: 'purple' }

function fmtScore(v: number): string {
  return Number.isInteger(v) ? String(v) : v.toFixed(2)
}

/** Eligible candidates in the order they would be tried. */
export function CandidateTable({ candidates, label = 'Candidates', showScore }: { candidates: DecisionCandidate[]; label?: string; showScore?: boolean }) {
  if (candidates.length === 0) return <p className="secondary">No eligible candidates were found.</p>
  const scored = showScore ?? candidates.some((c) => c.score !== 0)
  return (
    <TableScroll label={label}>
      <table className="data-table" style={{ width: '100%' }}>
        <thead>
          <tr>
            <th>#</th>
            <th>Provider / account</th>
            <th>Model</th>
            <th>Billing</th>
            <th>Upstream</th>
            {scored && <th className="num">Score</th>}
            <th>Reasons</th>
            <th>Health</th>
          </tr>
        </thead>
        <tbody>
          {candidates.map((c, i) => (
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
              {scored && <td className="num">{fmtScore(c.score)}</td>}
              <td>
                {(c.reasons ?? []).length ? (
                  (c.reasons ?? []).map((x) => (
                    <div key={x} className="secondary">
                      {x}
                    </div>
                  ))
                ) : (
                  <span className="muted">—</span>
                )}
              </td>
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
  )
}

/** Pool members removed before ranking, with the stage and reason. */
export function ExclusionTable({ excluded, label = 'Excluded candidates' }: { excluded: DecisionExclusion[]; label?: string }) {
  if (excluded.length === 0) return <p className="secondary">No pool member was excluded.</p>
  return (
    <TableScroll label={label}>
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
          {excluded.map((x, i) => (
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
  )
}

/** Rule and decision-table evaluations recorded with a decision. */
export function RuleTraceTable({ traces, accountName, label = 'Rule evaluations' }: { traces: RuleTrace[]; accountName?: (id: string) => string; label?: string }) {
  if (traces.length === 0) return <p className="secondary">The plan has no rules or decision tables, or none were evaluated.</p>
  return (
    <TableScroll label={label}>
      <table className="data-table" style={{ width: '100%' }}>
        <thead>
          <tr>
            <th>Rule / table</th>
            <th>Stage</th>
            <th>Candidate</th>
            <th>Result</th>
            <th>Effect</th>
          </tr>
        </thead>
        <tbody>
          {traces.map((t, i) => (
            <tr key={i}>
              <td>
                <span className="mono">{t.name}</span> {t.kind === 'table' && <Badge>table{t.row !== undefined ? ` row ${t.row + 1}` : ''}</Badge>}
              </td>
              <td>
                <Badge tone={t.stage === 'request' ? 'info' : 'purple'}>{t.stage}</Badge>
              </td>
              <td>
                {t.account_id ? (
                  <>
                    {accountName ? accountName(t.account_id) : <span className="mono">{t.account_id}</span>}
                    {t.model && <span className="mono secondary"> · {t.model}</span>}
                  </>
                ) : (
                  <span className="muted">—</span>
                )}
              </td>
              <td>
                {t.error ? (
                  <Badge tone="warning" title={t.error}>
                    not applied (error)
                  </Badge>
                ) : t.matched ? (
                  <Badge tone="success">matched</Badge>
                ) : (
                  <Badge>no match</Badge>
                )}
              </td>
              <td>{t.error ? <span className="secondary mono">{t.error}</span> : t.effect || <span className="muted">—</span>}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </TableScroll>
  )
}

/** Compact summary line for a decision (alias → pool, intent). */
export function DecisionSummary({ d }: { d: Decision }) {
  return (
    <p className="secondary" style={{ margin: 0 }}>
      {d.requested_model && (
        <>
          Requested <span className="mono">{d.requested_model}</span>
        </>
      )}
      {d.alias && (
        <>
          {' '}
          → alias <span className="mono">{d.alias}</span>
        </>
      )}
      {d.pool && (
        <>
          {' '}
          → pool <span className="mono">{d.pool}</span> ({d.strategy || 'ordered'})
        </>
      )}
      {d.resolution && <> · {d.resolution}</>}
      {d.intent && (
        <>
          {' '}
          · intent <span className="mono">{d.intent}</span>
          {d.intent_source ? ` (${d.intent_source})` : ''}
        </>
      )}
      {(d.required_capabilities?.length ?? 0) > 0 && <> · requires {d.required_capabilities!.join(', ')}</>}
    </p>
  )
}
