import { Paper, Stack, Text } from '@mantine/core'
import { Badge, TableScroll, type Tone } from '@/components/ui'
import { BillingBadge } from '@/components/ResourceBadges'
import { ProtocolBadge } from '@/features/activity/bits'
import { useCompact } from '@/ui/kit'
import type { Decision, DecisionCandidate, DecisionExclusion, RuleTrace } from '@/lib/api/types'

const stageTone: Record<string, Tone> = { restriction: 'neutral', capability: 'info', health: 'warning', policy: 'error', rule: 'purple' }
const stageWords: Record<string, string> = {
  restriction: 'Hard restriction',
  capability: 'Missing capability',
  health: 'Account health',
  policy: 'Rule',
  rule: 'Rule',
  request: 'Before choosing an account',
  candidate: 'For each account',
}

/** "Team key 1 (Anthropic)". */
export function candidateName(c: { provider?: string; account?: string; account_id?: string }): string {
  const acc = c.account || c.account_id || ''
  return c.provider ? `${acc} (${c.provider})` : acc
}

/** How the requested model was resolved, in words. */
export function resolutionWords(res: string | undefined): string {
  if (!res) return ''
  if (res === 'alias_name') return 'exact model name'
  if (res === 'default_alias') return 'anything else'
  if (res.startsWith('mapping:')) return `matched ${res.slice('mapping:'.length)}`
  return res.replaceAll('_', ' ')
}

/** Why a request was refused, in words. */
export const errorKindWords: Record<string, string> = {
  policy_blocked: 'Blocked by a rule',
  not_found: 'No route for this model',
  permission: 'Not allowed for this client',
  quota_exhausted: 'No account has quota left',
  rate_limited: 'Every account is rate limited',
  budget_exceeded: 'Budget used up',
  unsupported: 'No account supports this request',
  invalid_request: 'Invalid request',
  internal: 'Configuration problem',
}

function fmtScore(v: number): string {
  return Number.isInteger(v) ? String(v) : v.toFixed(2)
}

/** Eligible candidates in the order they would be tried. */
export function CandidateTable({ candidates, label = 'Candidates', showScore }: { candidates: DecisionCandidate[]; label?: string; showScore?: boolean }) {
  const compact = useCompact()
  if (candidates.length === 0) return <p className="secondary">No account could serve this request.</p>
  // Score and reasons only matter when something set them.
  const scored = (showScore ?? true) && candidates.some((c) => c.score !== 0)
  const reasoned = candidates.some((c) => (c.reasons ?? []).length > 0)
  if (compact) {
    return (
      <Stack gap={6} aria-label={label}>
        {candidates.map((c, i) => (
          <Paper key={c.account_id + c.model} withBorder p="xs" radius="sm">
            <Text size="sm" fw={600}>
              {i + 1}. {candidateName(c)}
            </Text>
            <Text size="xs" c="dimmed">
              {c.model} · {c.native ? 'direct' : 'translated'}
              {c.health?.Available === false ? ' · unavailable' : ''}
              {scored ? ` · score ${fmtScore(c.score)}` : ''}
            </Text>
          </Paper>
        ))}
      </Stack>
    )
  }
  return (
    <TableScroll label={label}>
      <table className="data-table" style={{ width: '100%' }}>
        <thead>
          <tr>
            <th>#</th>
            <th>Account</th>
            <th>Model</th>
            <th>Billing</th>
            <th>Sent as</th>
            {scored && <th className="num">Score</th>}
            {reasoned && <th>Reasons</th>}
            <th>Health</th>
          </tr>
        </thead>
        <tbody>
          {candidates.map((c, i) => (
            <tr key={c.account_id + c.model}>
              <td className="num">{i + 1}</td>
              <td>{candidateName(c)}</td>
              <td className="mono">{c.model}</td>
              <td>
                <BillingBadge value={c.billing} />
              </td>
              <td>
                <ProtocolBadge p={c.upstream_protocol} /> {c.native ? <Badge tone="success">direct</Badge> : <Badge tone="info">translated</Badge>}
              </td>
              {scored && <td className="num">{fmtScore(c.score)}</td>}
              {reasoned && (
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
              )}
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
  const compact = useCompact()
  if (excluded.length === 0) return <p className="secondary">No account in the fallback group was skipped.</p>
  if (compact) {
    return (
      <Stack gap={6} aria-label={label}>
        {excluded.map((x, i) => (
          <Paper key={i} withBorder p="xs" radius="sm">
            <Text size="sm" fw={600}>
              {x.account || x.account_id} · {x.model}
            </Text>
            <Text size="xs" c="dimmed">
              {stageWords[x.stage] ?? x.stage}: {x.detail || x.reason.replaceAll('_', ' ')}
            </Text>
          </Paper>
        ))}
      </Stack>
    )
  }
  return (
    <TableScroll label={label}>
      <table className="data-table" style={{ width: '100%' }}>
        <thead>
          <tr>
            <th>Account</th>
            <th>Model</th>
            <th>Skipped because</th>
            <th>Detail</th>
          </tr>
        </thead>
        <tbody>
          {excluded.map((x, i) => (
            <tr key={i}>
              <td>{x.account || <span className="mono">{x.account_id}</span>}</td>
              <td className="mono">{x.model}</td>
              <td>
                <Badge tone={stageTone[x.stage] ?? 'neutral'}>{stageWords[x.stage] ?? x.stage}</Badge> {x.reason.replaceAll('_', ' ')}
              </td>
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
            <th>Rule or table</th>
            <th>When</th>
            <th>Account</th>
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
                <Badge tone={t.stage === 'request' ? 'info' : 'purple'}>{stageWords[t.stage] ?? t.stage}</Badge>
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
              <td>{t.error ? <span className="secondary mono">{t.error}</span> : t.effect ? t.effect.replace(/: ?$/, '') : <span className="muted">—</span>}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </TableScroll>
  )
}

/** One line: requested model → model name (how it matched) → fallback group. */
export function DecisionSummary({ d }: { d: Decision }) {
  return (
    <p className="secondary" style={{ margin: 0 }}>
      {d.requested_model && (
        <>
          Asked for <span className="mono">{d.requested_model}</span>
        </>
      )}
      {d.alias && (
        <>
          {' '}
          → model name <span className="mono">{d.alias}</span>
          {d.resolution && <> ({resolutionWords(d.resolution)})</>}
        </>
      )}
      {d.pool && (
        <>
          {' '}
          → fallback group <span className="mono">{d.pool}</span>
        </>
      )}
      {d.intent && (
        <>
          {' '}
          · intent <span className="mono">{d.intent}</span>
          {d.intent_source ? ` (${d.intent_source})` : ''}
        </>
      )}
      {(d.required_capabilities?.length ?? 0) > 0 && <> · needs {d.required_capabilities!.join(', ')}</>}
    </p>
  )
}
