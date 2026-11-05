import { Badge, type Tone } from '@/components/ui'
import type { BudgetUsage } from '@/lib/api/types'
import { fmtDateTime, fmtMoney, fmtPercent } from '@/lib/format'
import { CostKindLabel } from '@/features/activity/bits'
import { e10 } from '@/features/quota/quota'
import q from '@/features/quota/quota.module.css'

const stateTone: Record<BudgetUsage['state'], Tone> = { ok: 'success', warning: 'warning', exceeded: 'error', disabled: 'neutral' }

export function BudgetStateBadge({ state }: { state: BudgetUsage['state'] }) {
  return <Badge tone={stateTone[state]}>{state === 'ok' ? 'Within budget' : state === 'warning' ? 'Warning' : state === 'exceeded' ? 'Exceeded' : 'Disabled'}</Badge>
}

/** Spend progress with the warning threshold marked. */
export function BudgetProgress({ u }: { u: BudgetUsage }) {
  const pct = Math.max(0, Math.min(100, u.fraction * 100))
  return (
    <div
      className={q.progress}
      role="meter"
      aria-label={`${u.name} spend`}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(pct)}
      aria-valuetext={`${fmtPercent(u.fraction * 100, 1)} of limit used`}
    >
      <div className={q.progressFill} data-state={u.state} style={{ width: `${pct}%` }} />
      <div className={q.warnMark} style={{ left: `${Math.min(100, u.warn_at * 100)}%` }} title={`Warning at ${fmtPercent(u.warn_at * 100)}`} />
    </div>
  )
}

/** One budget's current period. */
export function BudgetCard({ u, scopeName }: { u: BudgetUsage; scopeName: string }) {
  const cur = u.currency
  const projected = e10(u.projected_spend_e10)
  const byKind = Object.entries(u.by_kind_e10)
  return (
    <article className={q.card} aria-labelledby={`bdg-${u.budget_id}`}>
      <div className={q.cardHeader}>
        <div className={q.cardTitle}>
          <h3 id={`bdg-${u.budget_id}`} className="mono">{u.name}</h3>
          <div className={q.badges}>
            <BudgetStateBadge state={u.state} />
            <Badge>{u.enforcement === 'block' ? 'Blocks at limit' : 'Warn only'}</Badge>
            <span className="muted" style={{ fontSize: 'var(--text-xs)' }}>
              {u.scope === 'global' ? 'Everything' : `${u.scope} ${scopeName}`} · per {u.period}
            </span>
          </div>
        </div>
      </div>
      <BudgetProgress u={u} />
      <div className={q.small}>
        <span className="num">{fmtMoney(e10(u.spent_e10), cur)}</span> spent
        {u.reserved_e10 > 0 && <> + <span className="num">{fmtMoney(e10(u.reserved_e10), cur)}</span> in flight</>} of{' '}
        <span className="num">{fmtMoney(e10(u.limit_e10), cur)}</span> ({fmtPercent(u.fraction * 100, 1)})
      </div>
      <div className={q.small}>
        <span className="muted">Projected for the period: </span>
        {projected ? <span className="num">{fmtMoney(projected, cur)}</span> : <span className="muted">too early to project</span>}
      </div>
      <div className={q.small}>
        <span className="muted">Period: </span>
        {fmtDateTime(u.period_start)} → {fmtDateTime(u.period_end)}
      </div>
      <div className={q.small}>
        <span className="muted">Counts: </span>
        {u.cost_kinds.map((k, i) => (
          <span key={k}>
            {i > 0 && ', '}
            <CostKindLabel kind={k} />
          </span>
        ))}
      </div>
      {byKind.length > 0 && (
        <div className={q.small}>
          <span className="muted">Spend in {cur} by category this period:</span>
          <ul className={q.list}>
            {byKind.map(([k, v]) => (
              <li key={k}>
                <span className="num">{fmtMoney(e10(v), cur)}</span> <CostKindLabel kind={k} />
                {!u.cost_kinds.includes(k) && <span className="muted"> (not counted)</span>}
              </li>
            ))}
          </ul>
        </div>
      )}
    </article>
  )
}
