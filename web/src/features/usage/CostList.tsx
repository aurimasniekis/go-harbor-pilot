import { fmtMoney } from '@/lib/format'
import { CostKindLabel } from '@/features/activity/bits'
import q from '@/features/quota/quota.module.css'

/** Costs per category and currency. Categories are never added together. */
export function CostList({ costs, empty = '—' }: { costs: { kind: string; currency: string; amount: string }[] | null | undefined; empty?: string }) {
  if (!costs || costs.length === 0) return <span className="muted">{empty}</span>
  return (
    <span className={q.costList}>
      {costs.map((c) => (
        <span key={c.kind + c.currency}>
          <span className="num">{fmtMoney(c.amount, c.currency)}</span> <CostKindLabel kind={c.kind} />
        </span>
      ))}
    </span>
  )
}
