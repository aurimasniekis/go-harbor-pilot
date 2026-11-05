import { useState } from 'react'
import { createFileRoute, Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { MessagesSquare } from 'lucide-react'
import { Button, EmptyState, Panel, TableScroll } from '@/components/ui'
import { QueryError, TableSkeleton } from '@/components/states'
import { resourceListQuery, sessionsQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { BindingRes } from '@/lib/api/types'
import { fmtDateTime, fmtRelative } from '@/lib/format'
import { ConfidenceBadge, HarnessBadge } from '@/features/activity/bits'

export const Route = createFileRoute('/activity/sessions')({
  component: Sessions,
})

function Sessions() {
  const [cursors, setCursors] = useState<string[]>([])
  const list = useQuery(sessionsQuery({ cursor: cursors[cursors.length - 1] }))
  const bindings = useQuery(resourceListQuery<BindingRes>('client-bindings'))
  const bindingById = byId(bindings.data?.items)
  const items = list.data?.items ?? []
  return (
    <Panel
      title="Sessions"
      subtitle="Conversations grouped by a trusted reference or a trusted harness session header. Requests without that evidence are never merged into a session just because they share a credential."
    >
      {list.isError ? (
        <QueryError error={list.error} retry={() => void list.refetch()} />
      ) : !list.data ? (
        <TableSkeleton />
      ) : items.length === 0 ? (
        <EmptyState icon={MessagesSquare} title="No sessions observed">
          Sessions appear when a client sends a session header and its binding trusts session hints, or when a credential carries a bound session reference.
        </EmptyState>
      ) : (
        <>
          <TableScroll label="Sessions">
            <table className="data-table" style={{ width: '100%' }}>
              <thead>
                <tr>
                  <th>Session</th>
                  <th>Client</th>
                  <th>Source</th>
                  <th>Confidence</th>
                  <th>Harness</th>
                  <th>First seen</th>
                  <th>Last seen</th>
                  <th>Requests</th>
                </tr>
              </thead>
              <tbody>
                {items.map((s) => (
                  <tr key={s.id}>
                    <td className="mono">
                      <Link to="/activity" search={{ session_id: s.id }}>
                        {s.id}
                      </Link>
                    </td>
                    <td>{bindingById[s.binding_id]?.name ?? <span className="mono muted">{s.binding_id}</span>}</td>
                    <td>{s.source.replace('_', ' ')}</td>
                    <td>
                      <ConfidenceBadge c={s.confidence} />
                    </td>
                    <td>
                      <HarnessBadge h={s.harness} />
                    </td>
                    <td title={fmtDateTime(s.first_seen, { seconds: true })}>{fmtRelative(s.first_seen)}</td>
                    <td title={fmtDateTime(s.last_seen, { seconds: true })}>{fmtRelative(s.last_seen)}</td>
                    <td className="num">{s.requests}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </TableScroll>
          <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 12 }}>
            <Button size="small" disabled={cursors.length === 0} onClick={() => setCursors(cursors.slice(0, -1))}>
              Newer
            </Button>
            <Button size="small" disabled={!list.data.next_cursor} onClick={() => list.data?.next_cursor && setCursors([...cursors, list.data.next_cursor])}>
              Older
            </Button>
          </div>
        </>
      )}
    </Panel>
  )
}
