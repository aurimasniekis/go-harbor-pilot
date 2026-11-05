import { Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { AlertTriangle } from 'lucide-react'
import { Badge, Banner, KeyValues, Panel, Skeleton, TableScroll } from '@/components/ui'
import { QueryError } from '@/components/states'
import { activitySummaryQuery, requestsQuery } from '@/lib/api/queries'
import { fmtDateTime, fmtRelative } from '@/lib/format'
import { CostKindLabel, HarnessBadge, Ms, StatusBadge, TokenValue } from './bits'

function Counts({ m }: { m: Record<string, number> }) {
  const entries = Object.entries(m).sort((a, b) => b[1] - a[1])
  if (entries.length === 0) return <span className="muted">—</span>
  return (
    <span style={{ display: 'inline-flex', gap: 4, flexWrap: 'wrap' }}>
      {entries.map(([k, v]) => (
        <Badge key={k}>
          {k} · {v}
        </Badge>
      ))}
    </span>
  )
}

/** Overview panel summarising the last 24 hours of gateway activity. */
export function ActivitySummaryPanel() {
  const q = useQuery(activitySummaryQuery(24))
  return (
    <Panel title="Activity (24h)" subtitle="Gateway requests in the last 24 hours." actions={<Link to="/activity">All activity</Link>}>
      {q.isError ? (
        <QueryError error={q.error} retry={() => void q.refetch()} />
      ) : !q.data ? (
        <Skeleton height={100} />
      ) : (
        <>
          {q.data.telemetry_dropped > 0 && (
            <Banner tone="warning" icon={AlertTriangle}>
              Coverage reduced: {q.data.telemetry_dropped} activity records were dropped under load since the server started.
            </Banner>
          )}
          <KeyValues
            items={[
              ['Requests', <span className="num">{q.data.requests}</span>],
              ['By status', <Counts m={q.data.by_status} />],
              ['By harness', <Counts m={q.data.by_harness} />],
              [
                'Tokens in / out',
                <span>
                  <TokenValue v={q.data.tokens.input} /> / <TokenValue v={q.data.tokens.output} />
                  {q.data.tokens.cache_read !== null && (
                    <span className="muted">
                      {' '}
                      · cache read <TokenValue v={q.data.tokens.cache_read} />
                    </span>
                  )}
                </span>,
              ],
              [
                'Costs',
                q.data.costs.length === 0 ? (
                  <span className="muted">—</span>
                ) : (
                  <span style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                    {q.data.costs.map((c) => (
                      <span key={c.currency + c.kind}>
                        <span className="num">
                          {c.currency} {c.amount}
                        </span>{' '}
                        <CostKindLabel kind={c.kind} />
                      </span>
                    ))}
                  </span>
                ),
              ],
              ['Avg. time to first token', <Ms v={q.data.avg_ttft_ms === null ? null : Math.round(q.data.avg_ttft_ms)} />],
              ['Needed failover', <span className="num">{q.data.fallbacks}</span>],
              ['Usage not reported', <span className="num">{q.data.unknown_usage}</span>],
            ]}
          />
        </>
      )}
    </Panel>
  )
}

/** Most recent requests of one client binding. */
export function RecentRequestsPanel({ bindingId }: { bindingId: string }) {
  const q = useQuery(requestsQuery({ binding_id: bindingId, limit: 10 }))
  return (
    <Panel
      title="Recent requests"
      subtitle="The latest requests made with this client's credentials."
      actions={<Link to="/activity" search={{ binding_id: bindingId }}>All requests</Link>}
    >
      {q.isError ? (
        <QueryError error={q.error} retry={() => void q.refetch()} />
      ) : !q.data ? (
        <Skeleton height={80} />
      ) : (q.data.items ?? []).length === 0 ? (
        <p className="secondary">No requests yet.</p>
      ) : (
        <TableScroll label="Recent requests">
          <table className="data-table" style={{ width: '100%' }}>
            <thead>
              <tr>
                <th>Time</th>
                <th>Harness</th>
                <th>Model</th>
                <th>Status</th>
                <th>Total</th>
              </tr>
            </thead>
            <tbody>
              {(q.data.items ?? []).map((r) => (
                <tr key={r.id}>
                  <td title={fmtDateTime(r.created_at, { seconds: true })}>
                    <Link to="/activity/$requestId" params={{ requestId: r.id }}>
                      {fmtRelative(r.created_at)}
                    </Link>
                  </td>
                  <td>
                    <HarnessBadge h={r.harness} />
                  </td>
                  <td className="mono">
                    {r.requested_model}
                    {r.final_model && <span className="muted"> → {r.final_model}</span>}
                  </td>
                  <td>
                    <StatusBadge status={r.status} />
                  </td>
                  <td>
                    <Ms v={r.duration_ms} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableScroll>
      )}
    </Panel>
  )
}
