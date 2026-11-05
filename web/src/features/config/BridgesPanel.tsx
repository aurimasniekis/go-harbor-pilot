import { useQuery } from '@tanstack/react-query'
import { Badge, Panel, TableScroll } from '@/components/ui'
import { QueryError, TableSkeleton } from '@/components/states'
import { bridgesQuery } from '@/lib/api/queries'

const labels: Record<string, string> = { claude_code: 'Claude Code', codex: 'Codex' }

/** Availability of the CLI bridges that serve subscription accounts. */
export function BridgesPanel() {
  const q = useQuery(bridgesQuery)
  return (
    <Panel title="CLI bridges" subtitle="Subscription accounts of the claude_code and codex provider types run through these vendor CLIs on this server.">
      {q.isError ? (
        <QueryError error={q.error} retry={() => void q.refetch()} />
      ) : !q.data ? (
        <TableSkeleton rows={2} />
      ) : (
        <TableScroll label="CLI bridge status">
          <table className="data-table" style={{ width: '100%' }}>
            <thead>
              <tr>
                <th>Bridge</th>
                <th>Status</th>
                <th>Version</th>
                <th>Binary</th>
              </tr>
            </thead>
            <tbody>
              {(q.data.items ?? []).map((b) => (
                <tr key={b.kind}>
                  <td>{labels[b.kind] ?? b.kind}</td>
                  <td>
                    {b.enabled && !b.last_error ? (
                      <Badge tone="success">Available</Badge>
                    ) : b.enabled ? (
                      <Badge tone="warning" title={b.last_error}>Self-test failed</Badge>
                    ) : (
                      <Badge title={b.last_error}>Unavailable</Badge>
                    )}
                    {b.last_error && <div className="muted" style={{ fontSize: 'var(--text-xs)', marginTop: 4 }}>{b.last_error}</div>}
                  </td>
                  <td className="mono">{b.version || <span className="muted">—</span>}</td>
                  <td className="mono" style={{ overflowWrap: 'anywhere' }}>{b.binary || <span className="muted">—</span>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableScroll>
      )}
    </Panel>
  )
}
