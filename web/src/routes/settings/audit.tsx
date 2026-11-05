import { useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ShieldAlert, ShieldCheck } from 'lucide-react'
import { Badge, Banner, Button, EmptyState, Panel, SearchInput, TableScroll } from '@/components/ui'
import { MutationError } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { apiSend } from '@/lib/api/client'
import { auditQuery, qk, securityAlertsQuery } from '@/lib/api/queries'
import { useCan } from '@/lib/session'
import { fmtDateTime } from '@/lib/format'

export const Route = createFileRoute('/settings/audit')({
  component: Audit,
})

function Audit() {
  const qc = useQueryClient()
  const canAck = useCan('system.manage')
  const alerts = useQuery({ ...securityAlertsQuery, enabled: canAck })
  const [action, setAction] = useState('')
  const [cursors, setCursors] = useState<string[]>([])
  const cursor = cursors[cursors.length - 1]
  const log = useQuery(auditQuery({ action: action || undefined, cursor }))
  const ack = useMutation({
    mutationFn: (id: string) => apiSend<void>('POST', `/security-alerts/${id}/acknowledge`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.alerts })
      void qc.invalidateQueries({ queryKey: qk.session })
    },
  })
  return (
    <>
      {(alerts.data?.items ?? []).map((a) => (
        <Banner key={a.id} tone="error" icon={ShieldAlert}>
          <strong>{a.kind}</strong> · {fmtDateTime(a.created_at)} — {a.message}{' '}
          <Button size="small" icon={ShieldCheck} onClick={() => ack.mutate(a.id)}>Acknowledge</Button>
        </Banner>
      ))}
      <MutationError error={ack.error} />
      <Panel
        title="Audit log"
        subtitle="Security, configuration and content-access operations. Entries are append-only."
        actions={<SearchInput value={action} onChange={(v) => { setAction(v); setCursors([]) }} placeholder="Action prefix, e.g. auth." label="Filter by action" />}
      >
        {log.isError ? <QueryError error={log.error} retry={() => void log.refetch()} /> : !log.data ? <TableSkeleton /> : (log.data.items ?? []).length === 0 ? (
          <EmptyState title="No entries">Nothing matches this filter.</EmptyState>
        ) : (
          <>
            <TableScroll label="Audit entries">
              <table className="data-table" style={{ width: '100%' }}>
                <thead><tr><th>Time</th><th>Actor</th><th>Action</th><th>Target</th><th>Outcome</th><th>Details</th></tr></thead>
                <tbody>
                  {(log.data.items ?? []).map((e) => (
                    <tr key={e.id}>
                      <td style={{ whiteSpace: 'nowrap' }}>{fmtDateTime(e.at, { seconds: true })}</td>
                      <td><span className="mono">{e.actor_id}</span> <span className="muted">{e.actor_kind}</span></td>
                      <td className="mono">{e.action}</td>
                      <td className="mono">{e.target_kind ? `${e.target_kind}:${e.target_id ?? ''}` : '—'}</td>
                      <td><Badge tone={e.outcome === 'success' ? 'success' : e.outcome === 'denied' ? 'warning' : 'error'}>{e.outcome}</Badge></td>
                      <td className="mono muted" style={{ maxWidth: 360, overflowWrap: 'anywhere', fontSize: 'var(--text-xs)' }}>{JSON.stringify(e.details)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </TableScroll>
            <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 12 }}>
              <Button size="small" disabled={cursors.length === 0} onClick={() => setCursors(cursors.slice(0, -1))}>Newer</Button>
              <Button size="small" disabled={!log.data.next_cursor} onClick={() => log.data?.next_cursor && setCursors([...cursors, log.data.next_cursor])}>Older</Button>
            </div>
          </>
        )}
      </Panel>
    </>
  )
}
