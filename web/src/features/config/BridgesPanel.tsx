import { useQuery } from '@tanstack/react-query'
import { AlertTriangle, CheckCircle2, CircleSlash } from 'lucide-react'
import { Badge, Panel } from '@/components/ui'
import { QueryError, TableSkeleton } from '@/components/states'
import { bridgesQuery } from '@/lib/api/queries'
import o from '@/features/overview/overview.module.css'

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
        <ul className={o.list} aria-label="CLI bridge status">
          {(q.data.items ?? []).map((b) => (
            <li key={b.kind} className={o.row}>
              <div className={o.rowMain}>
                <span className={o.rowTitle}>{labels[b.kind] ?? b.kind}</span>
                {b.version && <span className={o.rowDetail}>{b.version}</span>}
                {b.binary && (
                  <span className={`${o.rowDetail} mono`} title={b.binary}>
                    {b.binary}
                  </span>
                )}
                {b.last_error && <span className={o.rowDetail}>{b.last_error}</span>}
              </div>
              {b.enabled && !b.last_error ? (
                <Badge tone="success" icon={CheckCircle2}>Available</Badge>
              ) : b.enabled ? (
                <Badge tone="warning" icon={AlertTriangle} title={b.last_error}>Self-test failed</Badge>
              ) : (
                <Badge icon={CircleSlash} title={b.last_error}>Unavailable</Badge>
              )}
            </li>
          ))}
        </ul>
      )}
    </Panel>
  )
}
