import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Wallet } from 'lucide-react'
import { Badge, EmptyState, Panel } from '@/components/ui'
import { QueryError, TableSkeleton } from '@/components/states'
import { ResourceList } from '@/features/config/ResourceList'
import { BudgetDialog } from '@/features/budgets/BudgetDialog'
import { BudgetCard } from '@/features/budgets/BudgetStatus'
import { budgetStatusQuery, resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { AccountRes, BindingRes, BudgetRes, ProjectRes, WorkspaceRes } from '@/lib/api/types'
import q from '@/features/quota/quota.module.css'

export const Route = createFileRoute('/usage/budgets')({
  component: Budgets,
})

function useScopeNames() {
  const ws = useQuery(resourceListQuery<WorkspaceRes>('workspaces'))
  const pr = useQuery(resourceListQuery<ProjectRes>('projects'))
  const bd = useQuery(resourceListQuery<BindingRes>('client-bindings'))
  const ac = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const all = { ...byId(ws.data?.items), ...byId(pr.data?.items), ...byId(bd.data?.items), ...byId(ac.data?.items) }
  return (id: string) => (id ? (all[id]?.name ?? id) : '')
}

function Budgets() {
  const status = useQuery(budgetStatusQuery)
  const scopeName = useScopeNames()
  return (
    <div className={q.stack}>
      <Panel title="Current period" subtitle="Spend is settled ledger cost plus the estimate of in-flight attempts. Blocking budgets stop new attempts before the limit is crossed.">
        {status.isError ? (
          <QueryError error={status.error} retry={() => void status.refetch()} />
        ) : !status.data ? (
          <TableSkeleton rows={3} />
        ) : (status.data.items ?? []).length === 0 ? (
          <EmptyState icon={Wallet} title="No budgets">Add a budget below to cap spend for a workspace, project, client or account.</EmptyState>
        ) : (
          <div className={q.cards}>
            {(status.data.items ?? []).map((u) => (
              <BudgetCard key={u.budget_id} u={u} scopeName={scopeName(u.scope_id)} />
            ))}
          </div>
        )}
      </Panel>
      <ResourceList<BudgetRes>
        plural="budgets"
        title="Budgets"
        subtitle="Spend limits per scope and period in one currency."
        noun="budget"
        empty="Budgets cap estimated and actual API spend; subscription list-price equivalents count only when selected."
        columns={[
          { header: 'Scope', cell: (r) => (r.spec.scope === 'global' ? 'Everything' : <span>{r.spec.scope} <span className="mono">{scopeName(r.spec.scope_id ?? '')}</span></span>) },
          { header: 'Limit', cell: (r) => <span className="num">{r.spec.limit} {r.spec.currency} / {r.spec.period}</span> },
          { header: 'Enforcement', cell: (r) => (r.spec.enforcement === 'warn' ? <Badge>Warn only</Badge> : <Badge tone="warning">Block</Badge>) },
          { header: 'State', cell: (r) => (r.spec.enabled ? <Badge tone="success">Enabled</Badge> : <Badge>Disabled</Badge>) },
        ]}
        dialog={(r, close) => <BudgetDialog budget={r} onClose={close} />}
      />
    </div>
  )
}
