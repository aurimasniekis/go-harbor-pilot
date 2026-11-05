import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Clock3, OctagonAlert, RefreshCw } from 'lucide-react'
import { Badge, Button, HealthBadge, KeyValues, TableScroll, TierBadge } from '@/components/ui'
import { MutationError } from '@/components/form'
import { apiSend } from '@/lib/api/client'
import type { AccountHealthView, AccountQuotaView, BucketView, Health, MonitorView, Tier } from '@/lib/api/types'
import { BucketRow } from './BucketRow'
import { fmtDateTime, fmtPercent, fmtRelative, isKnown } from '@/lib/format'
import { useCan } from '@/lib/session'
import { BucketDialog } from './BucketDialog'
import q from './quota.module.css'

function useRefresh() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => apiSend<{ status: string }>('POST', `/quota/monitors/${encodeURIComponent(id)}/refresh`),
    onSuccess: () => {
      // The poll runs in the background; re-read the board shortly after.
      setTimeout(() => void qc.invalidateQueries({ queryKey: ['quota', 'board'] }), 1500)
    },
  })
}

/** Card for one quota monitor. */
export function MonitorCard({ m, accountName }: { m: MonitorView; accountName?: string }) {
  const canRefresh = useCan('config.write')
  const refresh = useRefresh()
  const [detail, setDetail] = useState<BucketView | null>(null)
  const p = m.poll
  return (
    <article className={q.card} aria-labelledby={`mon-${m.id}`}>
      <div className={q.cardHeader}>
        <div className={q.cardTitle}>
          <h3 id={`mon-${m.id}`} className="mono">{m.name}</h3>
          <div className={q.badges}>
            <HealthBadge health={m.health as Health} reason={p.error_message || undefined} />
            {m.tier && <TierBadge tier={m.tier as Tier} />}
            <span className="muted" style={{ fontSize: 'var(--text-xs)' }}>{m.adapter_name || m.adapter}</span>
          </div>
        </div>
        {canRefresh && (
          <Button size="small" icon={RefreshCw} busy={refresh.isPending} aria-label={`Poll ${m.name} now`} onClick={() => refresh.mutate(m.id)}>
            Refresh
          </Button>
        )}
      </div>
      <KeyValues
        items={[
          ['Routing account', m.account_id ? <span className="mono">{accountName ?? m.account_id}</span> : <span className="muted">Monitor only</span>],
          ['Identity', m.masked_identifier ? <span className="mono">{m.masked_identifier}</span> : <span className="muted">—</span>],
          ['Last success', p.last_success_at ? fmtRelative(p.last_success_at) : <span className="muted">never</span>],
          ['Next poll', p.next_attempt_at ? fmtRelative(p.next_attempt_at) : <span className="muted">not scheduled</span>],
        ]}
      />
      {p.error_kind && (
        <p className={q.error} role="note">
          {p.error_kind.replaceAll('_', ' ')}: {p.error_message}
          {p.consecutive_failures > 1 ? ` (${p.consecutive_failures} failures in a row)` : ''}
          {p.cooldown_until ? ` · retry after ${fmtDateTime(p.cooldown_until)}` : ''}
        </p>
      )}
      {refresh.data && <p className="muted" role="status" style={{ fontSize: 'var(--text-xs)' }}>Refresh: {refresh.data.status.replaceAll('_', ' ')}</p>}
      <MutationError error={refresh.error} />
      <div className={q.buckets}>
        {m.buckets.length === 0 ? (
          <span className="muted">No observations yet.</span>
        ) : (
          m.buckets.map((b) => <BucketRow key={b.key} b={b} onDetails={() => setDetail(b)} />)
        )}
      </div>
      {detail && <BucketDialog owner={m.id} ownerName={m.name} bucket={detail} onClose={() => setDetail(null)} />}
    </article>
  )
}

/** Routing health of every provider account (quota and gateway cooldowns). */
export function AccountHealthTable({ rows }: { rows: AccountHealthView[] }) {
  if (rows.length === 0) return <span className="muted">No provider accounts.</span>
  return (
    <TableScroll label="Routing health by account">
      <table className="data-table" style={{ width: '100%' }}>
        <thead>
          <tr>
            <th>Account</th>
            <th>Billing</th>
            <th>Routing</th>
            <th>Tightest window left</th>
            <th>Reset</th>
            <th>Reason</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((h) => (
            <tr key={h.account_id}>
              <td className="mono">{h.name}</td>
              <td>{h.billing.replaceAll('_', ' ')}</td>
              <td>
                {h.available ? <Badge tone="success">Available</Badge> : <Badge tone="error" icon={OctagonAlert}>Unavailable</Badge>}
                {h.stale && <> <Badge tone="warning" icon={Clock3}>Stale</Badge></>}
              </td>
              <td className="num">{isKnown(h.remaining) ? fmtPercent(h.remaining * 100) : <span className="muted">Unknown</span>}</td>
              <td>{h.until ? `until ${fmtDateTime(h.until)}` : h.reset_at ? fmtRelative(h.reset_at) : <span className="muted">—</span>}</td>
              <td className="secondary">{h.reason || <span className="muted">—</span>}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </TableScroll>
  )
}

/** Rate limits derived from upstream response headers. */
export function ObservedRateLimits({ rows, accountName }: { rows: AccountQuotaView[]; accountName: (id: string) => string }) {
  if (rows.length === 0) return <span className="muted">No rate-limit headers observed yet. They appear after gateway requests to providers that send them.</span>
  return (
    <div className={q.cards}>
      {rows.map((a) => (
        <div key={a.account_id} className={q.card}>
          <h3 className="mono" style={{ fontSize: 'var(--text-md)', margin: 0 }}>{accountName(a.account_id)}</h3>
          <div className={q.buckets}>
            {a.buckets.map((b) => (
              <BucketRow key={b.key} b={b} />
            ))}
          </div>
        </div>
      ))}
    </div>
  )
}
