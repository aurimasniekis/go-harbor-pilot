import { Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Badge, KeyValues, Panel, Skeleton } from '@/components/ui'
import { QueryError } from '@/components/states'
import { budgetStatusQuery, quotaBoardQuery } from '@/lib/api/queries'
import { BudgetProgress, BudgetStateBadge } from '@/features/budgets/BudgetStatus'

/** Overview panel: quota monitor health, unavailable accounts and budgets. */
export function QuotaBudgetPanel() {
  const board = useQuery(quotaBoardQuery)
  const budgets = useQuery(budgetStatusQuery)
  const counts: Record<string, number> = {}
  for (const m of board.data?.monitors ?? []) counts[m.health] = (counts[m.health] ?? 0) + 1
  const exhausted = (board.data?.monitors ?? []).filter((m) => m.buckets.some((b) => b.exhausted))
  const unavailable = (board.data?.account_health ?? []).filter((h) => !h.available)
  return (
    <Panel title="Quotas and budgets" subtitle="Monitor health, accounts routing avoids, and budget spend this period." actions={<Link to="/quotas">Quota board</Link>}>
      {board.isError ? (
        <QueryError error={board.error} retry={() => void board.refetch()} />
      ) : !board.data ? (
        <Skeleton height={100} />
      ) : (
        <KeyValues
          items={[
            [
              'Monitors',
              Object.keys(counts).length === 0 ? (
                <span className="muted">none</span>
              ) : (
                <span style={{ display: 'inline-flex', gap: 4, flexWrap: 'wrap' }}>
                  {Object.entries(counts).map(([k, v]) => (
                    <Badge key={k} tone={k === 'healthy' ? 'success' : k === 'auth_failed' || k === 'error' ? 'error' : 'warning'}>
                      {k.replaceAll('_', ' ')} · {v}
                    </Badge>
                  ))}
                </span>
              ),
            ],
            ['Exhausted windows', exhausted.length === 0 ? <span className="muted">none</span> : exhausted.map((m) => m.name).join(', ')],
            ['Accounts routing avoids', unavailable.length === 0 ? <span className="muted">none</span> : unavailable.map((h) => h.name).join(', ')],
            ...(budgets.data?.items ?? []).map(
              (u) =>
                [
                  <Link key={u.budget_id} to="/usage/budgets" className="mono">{u.name}</Link>,
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                    <BudgetProgress u={u} />
                    <span>
                      <BudgetStateBadge state={u.state} />
                    </span>
                  </div>,
                ] as [React.ReactNode, React.ReactNode],
            ),
          ]}
        />
      )}
    </Panel>
  )
}
