import { type ReactNode, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Drawer, Group, Progress, Stack, Text, Tooltip } from '@mantine/core'
import { AlertTriangle, CheckCircle2, Clock3, KeyRound, OctagonAlert, PauseCircle, Plus, RefreshCw, ShieldAlert } from 'lucide-react'
import { Badge, Banner, Button } from '@/components/ui'
import { ConfirmDialog, MutationError } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { apiSend } from '@/lib/api/client'
import { budgetStatusQuery, quotaBoardQuery, resourceListQuery } from '@/lib/api/queries'
import { byId, useDeleteResource } from '@/lib/api/resources'
import type { AccountHealthView, AccountRes, BindingRes, BucketView, BudgetRes, BudgetUsage, MonitorView, ProjectRes, WorkspaceRes } from '@/lib/api/types'
import { fmtDuration, fmtRelative, fmtTime, serverNow } from '@/lib/format'
import { useCan } from '@/lib/session'
import { BudgetDialog } from '@/features/budgets/BudgetDialog'
import { BucketRow } from '@/features/quota/BucketRow'
import { BucketDialog } from '@/features/quota/BucketDialog'
import { e10 } from '@/features/quota/quota'
import { RowMenu } from '@/features/config/ResourceList'
import { type Column, DataTable, Empty, Fields, Section, Stat, StatGrid, useCompact } from '@/ui/kit'
import { money } from '@/ui/fmt'

/* ---------- derived state ---------- */

/** A window is used up only while its reported reset is still ahead. */
const usedUp = (b: BucketView) => b.exhausted && !b.reset_passed

type LimitStatus = 'available' | 'limit' | 'paused' | 'unavailable' | 'unknown'

export interface Row {
  key: string
  name: string
  /** Muted second line (billing, adapter). */
  sub: string
  linked: boolean
  status: LimitStatus
  statusDetail?: string
  tightest?: { label: string; left: number; reset: string | null; stale: boolean }
  /** Credential and polling of the trackers feeding this row. */
  tracking: { state: 'ok' | 'failed' | 'stale' | 'none' | 'pending'; checked: string | null; detail?: string }
  monitors: MonitorView[]
  observed: BucketView[]
}

function tightestOf(buckets: BucketView[]): Row['tightest'] {
  let best: Row['tightest']
  for (const b of buckets) {
    if (b.reset_passed || b.remaining_fraction === null || b.remaining_fraction === undefined) continue
    if (!best || b.remaining_fraction < best.left) best = { label: b.label, left: b.remaining_fraction, reset: b.reset_at, stale: b.stale }
  }
  return best
}

export function trackingOf(monitors: MonitorView[]): Row['tracking'] {
  if (monitors.length === 0) return { state: 'none', checked: null }
  const failed = monitors.find((m) => m.health === 'auth_failed' || m.health === 'error')
  const checked = monitors.map((m) => m.poll.last_success_at).filter(Boolean).sort().pop() ?? null
  if (failed) {
    return { state: 'failed', checked, detail: failed.health === 'auth_failed' ? 'Sign-in failed' : failed.poll.error_message || 'Check failing' }
  }
  if (monitors.some((m) => m.health === 'stale')) return { state: 'stale', checked }
  if (monitors.every((m) => m.health === 'pending')) return { state: 'pending', checked }
  return { state: 'ok', checked }
}

export function rowsFrom(health: AccountHealthView[], monitors: MonitorView[], observed: { account_id: string; buckets: BucketView[] }[]): Row[] {
  const rows: Row[] = health.map((h) => {
    const mons = monitors.filter((m) => m.account_id === h.account_id && m.enabled)
    const obs = observed.find((o) => o.account_id === h.account_id)?.buckets ?? []
    const buckets = [...mons.flatMap((m) => m.buckets), ...obs]
    const exhausted = buckets.find(usedUp)
    let status: LimitStatus = h.available ? 'available' : 'unavailable'
    let statusDetail: string | undefined
    if (exhausted) {
      status = 'limit'
      statusDetail = `${exhausted.label} used up`
    } else if (!h.available && h.until) {
      status = 'paused'
      statusDetail = `Paused until ${fmtTime(h.until)}${h.reason ? ` · ${h.reason}` : ''}`
    } else if (!h.available) {
      statusDetail = h.reason || undefined
    }
    return {
      key: h.account_id,
      name: h.name,
      sub: billingName[h.billing] ?? h.billing.replaceAll('_', ' '),
      linked: true,
      status,
      statusDetail,
      tightest: tightestOf(buckets) ?? (h.remaining !== null && h.remaining !== undefined ? { label: 'Tightest window', left: h.remaining, reset: h.reset_at, stale: h.stale } : undefined),
      tracking: trackingOf(mons),
      monitors: mons,
      observed: obs,
    }
  })
  for (const m of monitors.filter((x) => !x.account_id && x.enabled)) {
    const exhausted = m.buckets.find(usedUp)
    rows.push({
      key: m.id,
      name: m.name,
      sub: m.adapter_name || m.adapter,
      linked: false,
      status: exhausted ? 'limit' : m.buckets.length ? 'available' : 'unknown',
      statusDetail: exhausted ? `${exhausted.label} used up` : undefined,
      tightest: tightestOf(m.buckets),
      tracking: trackingOf([m]),
      monitors: [m],
      observed: [],
    })
  }
  return rows
}

export const billingName: Record<string, string> = { metered_api: 'API key', subscription: 'Subscription', free: 'Free', local: 'Local' }

const statusMeta: Record<LimitStatus, { label: string; tone: 'success' | 'error' | 'warning' | 'neutral'; icon: typeof CheckCircle2 }> = {
  available: { label: 'Available', tone: 'success', icon: CheckCircle2 },
  limit: { label: 'Limit reached', tone: 'error', icon: OctagonAlert },
  paused: { label: 'Paused', tone: 'warning', icon: PauseCircle },
  unavailable: { label: 'Unavailable', tone: 'error', icon: AlertTriangle },
  unknown: { label: 'No data yet', tone: 'neutral', icon: Clock3 },
}

export function resetsIn(iso: string | null | undefined): string {
  if (!iso) return ''
  const left = new Date(iso).getTime() - serverNow()
  return left > 0 ? `resets in ${fmtDuration(left)}` : 'reset due'
}

export function leftColor(left: number): string {
  if (left <= 0.1) return 'red'
  if (left < 0.25) return 'yellow'
  return 'green'
}

export function LimitStatusBadge({ row, detail }: { row: Row; detail?: boolean }) {
  const m = statusMeta[row.status]
  const badge = (
    <Badge tone={m.tone} icon={m.icon} title={row.statusDetail}>
      {m.label}
    </Badge>
  )
  if (!detail || row.status !== 'paused' || !row.statusDetail) return badge
  return (
    <Stack gap={2} align="flex-start">
      {badge}
      <Text size="xs" c="dimmed">
        {row.statusDetail.split(' · ')[0]}
      </Text>
    </Stack>
  )
}

/** Tracking (credential and polling) status, deliberately quiet unless broken. */
export function TrackingLine({ t }: { t: Row['tracking'] }) {
  if (t.state === 'none') return <Text size="xs" c="dimmed">Not tracked</Text>
  if (t.state === 'failed') {
    return (
      <Group gap={4} wrap="nowrap" c="var(--error-text)">
        <KeyRound size={12} aria-hidden="true" />
        <Text size="xs" c="inherit" truncate>
          {t.detail}
        </Text>
      </Group>
    )
  }
  if (t.state === 'pending') return <Text size="xs" c="dimmed">Not checked yet</Text>
  return (
    <Text size="xs" c={t.state === 'stale' ? 'yellow' : 'dimmed'}>
      {t.checked ? `Checked ${fmtRelative(t.checked)}` : 'Checked'}
      {t.state === 'stale' ? ' · out of date' : ''}
    </Text>
  )
}

export function WindowCell({ t }: { t: Row['tightest'] }) {
  if (!t) return <Text size="xs" c="dimmed">No quota data</Text>
  return (
    <Stack gap={4} miw={160}>
      <Group justify="space-between" gap="xs" wrap="nowrap">
        <Text size="xs" truncate>
          {t.label}
        </Text>
        <Text size="xs" c="dimmed" className="num" style={{ whiteSpace: 'nowrap' }}>
          {t.left <= 0 ? 'Used up' : `${Math.round(t.left * 100)}% left`}
          {t.stale ? ' · stale' : ''}
        </Text>
      </Group>
      <Progress value={Math.max(1, t.left * 100)} color={leftColor(t.left)} size={6} radius="xl" aria-label={`${t.label} remaining`} />
    </Stack>
  )
}

/* ---------- budgets ---------- */

function useScopeNames() {
  const ws = useQuery(resourceListQuery<WorkspaceRes>('workspaces'))
  const pr = useQuery(resourceListQuery<ProjectRes>('projects'))
  const bd = useQuery(resourceListQuery<BindingRes>('client-bindings'))
  const ac = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const all = { ...byId(ws.data?.items), ...byId(pr.data?.items), ...byId(bd.data?.items), ...byId(ac.data?.items) }
  return (id: string) => (id ? (all[id]?.name ?? id) : '')
}

const scopeNoun: Record<string, string> = { workspace: 'Workspace', project: 'Project', binding: 'Client', account: 'Account' }

function BudgetBar({ u }: { u: BudgetUsage }) {
  const pct = Math.max(0, Math.min(100, u.fraction * 100))
  const color = u.state === 'exceeded' ? 'red' : u.state === 'warning' ? 'yellow' : 'blue'
  return (
    <div style={{ position: 'relative' }}>
      <div
        role="meter"
        aria-label={`${u.name} spend`}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(pct)}
        aria-valuetext={`${pct.toFixed(1)} percent of limit used`}
      >
        <Progress.Root size={6} radius="xl">
          <Progress.Section value={Math.max(pct, 1)} color={color} aria-hidden="true" />
        </Progress.Root>
      </div>
      <Tooltip label={`Warning at ${Math.round(u.warn_at * 100)}%`}>
        <span style={{ position: 'absolute', top: -3, left: `${Math.min(100, u.warn_at * 100)}%`, width: 2, height: 12, background: 'var(--text-muted)' }} />
      </Tooltip>
    </div>
  )
}

function Budgets({ onAdd }: { onAdd: () => void }) {
  const status = useQuery(budgetStatusQuery)
  const list = useQuery(resourceListQuery<BudgetRes>('budgets'))
  const canWrite = useCan('config.write')
  const scopeName = useScopeNames()
  const res = byId(list.data?.items)
  const [editing, setEditing] = useState<BudgetRes | null>(null)
  const [deleting, setDeleting] = useState<BudgetRes | null>(null)
  const del = useDeleteResource('budgets')
  const items = status.data?.items ?? []
  const cols: Column<BudgetUsage>[] = [
    {
      id: 'name',
      header: 'Budget',
      mobile: 'title',
      cell: (u) => (
        <Stack gap={0} miw={0}>
          <Text size="sm" fw={500} truncate>
            {u.name}
          </Text>
          <Text size="xs" c="dimmed" truncate>
            {u.scope === 'global' ? 'Everything' : `${scopeNoun[u.scope] ?? u.scope} ${scopeName(u.scope_id)}`} · per {u.period}
          </Text>
        </Stack>
      ),
    },
    {
      id: 'spend',
      header: 'Spent this period',
      mobile: 'line',
      width: '34%',
      cell: (u) => {
        const projected = e10(u.projected_spend_e10)
        return (
          <Stack gap={4}>
            <Group justify="space-between" gap="xs" wrap="nowrap">
              <Text size="xs" className="num">
                {money(e10(u.spent_e10) ?? 0, u.currency)} of {money(e10(u.limit_e10) ?? 0, u.currency)}
              </Text>
              {projected && (
                <Tooltip label={`Projected for the period at the current rate`}>
                  <Text size="xs" c="dimmed" className="num">
                    → {money(projected, u.currency)}
                  </Text>
                </Tooltip>
              )}
            </Group>
            <BudgetBar u={u} />
          </Stack>
        )
      },
    },
    {
      id: 'state',
      header: 'Status',
      mobile: 'aside',
      cell: (u) =>
        u.state === 'exceeded' ? (
          <Badge tone="error" icon={OctagonAlert}>{u.enforcement === 'block' ? 'Blocking' : 'Over budget'}</Badge>
        ) : u.state === 'warning' ? (
          <Badge tone="warning" icon={AlertTriangle}>Near limit</Badge>
        ) : u.state === 'disabled' ? (
          <Badge>Off</Badge>
        ) : (
          <Text size="xs" c="dimmed">
            {u.enforcement === 'block' ? 'Blocks at limit' : 'Warns only'}
          </Text>
        ),
    },
    { id: 'reset', header: 'Resets', mobile: 'meta', cell: (u) => <Text size="xs" c="dimmed">{resetsIn(u.period_end)}</Text> },
    {
      id: 'actions',
      header: <span className="visually-hidden">Actions</span>,
      align: 'right',
      width: 48,
      mobile: 'hidden',
      cell: (u) =>
        res[u.budget_id] && canWrite ? (
          <RowMenu name={u.name} onEdit={() => setEditing(res[u.budget_id]!)} onDelete={() => setDeleting(res[u.budget_id]!)} />
        ) : null,
    },
  ]
  return (
    <Section title="Budgets" id="budgets" info="Spend caps per workspace, project, client or account. A blocking budget stops new requests before the limit is crossed.">
      <MutationError error={del.error} />
      {status.isError ? (
        <QueryError error={status.error} retry={() => void status.refetch()} />
      ) : !status.data ? (
        <TableSkeleton rows={2} />
      ) : (
        <DataTable
          label="Budget list"
          rows={items}
          columns={cols}
          rowKey={(u) => u.budget_id}
          onRow={canWrite ? (u) => res[u.budget_id] && setEditing(res[u.budget_id]!) : undefined}
          empty={
            <Empty title="No budgets yet" action={canWrite && <Button variant="primary" icon={Plus} onClick={onAdd}>Add budget</Button>}>
              Cap spend for a workspace, project, client or provider account.
            </Empty>
          }
        />
      )}
      {editing && <BudgetDialog budget={editing} onClose={() => setEditing(null)} />}
      <ConfirmDialog
        open={!!deleting}
        title={`Delete budget ${deleting?.name ?? ''}?`}
        message="Spend stops being checked against it."
        confirmLabel="Delete"
        onCancel={() => setDeleting(null)}
        onConfirm={() => {
          if (deleting) del.mutate({ id: deleting.id, revision: deleting.revision })
          setDeleting(null)
        }}
      />
    </Section>
  )
}

/* ---------- account drawer ---------- */

/** Quota windows of one account (or unlinked tracker), with forecasts. */
export function AccountQuota({ row }: { row: Row }) {
  const [detail, setDetail] = useState<{ owner: string; ownerName: string; b: BucketView } | null>(null)
  return (
    <Stack gap="lg">
      {row.statusDetail && <Text size="sm">{row.statusDetail}</Text>}
      {row.monitors.map((m) => (
        <Stack key={m.id} gap="sm">
          <Group justify="space-between" gap="xs">
            <Text size="sm" fw={600}>
              {m.adapter_name || m.adapter}
            </Text>
            <TrackingLine t={trackingOf([m])} />
          </Group>
          <Fields
            items={[
              ['Tracker', <span className="mono">{m.name}</span>],
              ['Signed in as', m.masked_identifier ? <span className="mono">{m.masked_identifier}</span> : '—'],
              ['Next check', m.poll.next_attempt_at ? fmtRelative(m.poll.next_attempt_at) : '—'],
            ]}
          />
          {m.poll.error_kind && (
            <Banner tone="error" icon={ShieldAlert}>
              {m.poll.error_message || m.poll.error_kind.replaceAll('_', ' ')}
            </Banner>
          )}
          {m.buckets.length === 0 ? (
            <Text size="sm" c="dimmed">
              No windows reported yet.
            </Text>
          ) : (
            m.buckets.map((b) => <BucketRow key={b.key} b={b} onDetails={() => setDetail({ owner: m.id, ownerName: m.name, b })} />)
          )}
        </Stack>
      ))}
      {row.observed.length > 0 && (
        <Stack gap="sm">
          <Text size="sm" fw={600}>
            Rate limits seen in responses
          </Text>
          {row.observed.map((b) => (
            <BucketRow key={b.key} b={b} />
          ))}
        </Stack>
      )}
      {row.monitors.length === 0 && row.observed.length === 0 && (
        <Text size="sm" c="dimmed">
          No quota is tracked for this account. Add quota tracking under Limits to see its windows here.
        </Text>
      )}
      {detail && <BucketDialog owner={detail.owner} ownerName={detail.ownerName} bucket={detail.b} onClose={() => setDetail(null)} />}
    </Stack>
  )
}

function AccountDrawer({ row, onClose }: { row: Row | null; onClose: () => void }) {
  const compact = useCompact()
  return (
    <Drawer
      opened={!!row}
      onClose={onClose}
      position={compact ? 'bottom' : 'right'}
      size={compact ? '100%' : 560}
      title={row && (
        <Group gap="sm">
          <Text fw={600}>{row.name}</Text>
          <LimitStatusBadge row={row} />
        </Group>
      )}
      closeButtonProps={{ 'aria-label': 'Close' }}
    >
      {row && <AccountQuota row={row} />}
    </Drawer>
  )
}

/* ---------- page ---------- */

function useRefreshAll(ids: string[]) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      await Promise.all(ids.map((id) => apiSend('POST', `/quota/monitors/${encodeURIComponent(id)}/refresh`)))
    },
    onSuccess: () => setTimeout(() => void qc.invalidateQueries({ queryKey: ['quota', 'board'] }), 1500),
  })
}

function answer(rows: Row[]): ReactNode {
  const reached = rows.filter((r) => r.status === 'limit')
  const tight = rows.filter((r) => r.tightest && r.status !== 'limit').sort((a, b) => a.tightest!.left - b.tightest!.left)[0]
  const parts: string[] = []
  if (reached.length) parts.push(`${reached.map((r) => r.name).join(', ')} ${reached.length === 1 ? 'has' : 'have'} reached a limit.`)
  else parts.push('No account has reached a limit.')
  if (tight?.tightest) {
    parts.push(
      `Closest: ${tight.name} ${tight.tightest.label}, ${Math.round(tight.tightest.left * 100)}% left${tight.tightest.reset ? `, ${resetsIn(tight.tightest.reset)}` : ''}.`,
    )
  }
  return parts.join(' ')
}

export function LimitsOverview({ addBudget = false }: { addBudget?: boolean }) {
  const board = useQuery(quotaBoardQuery)
  const budgets = useQuery(budgetStatusQuery)
  const canWrite = useCan('config.write')
  const [adding, setAdding] = useState(addBudget)
  const [open, setOpen] = useState<Row | null>(null)
  const rows = useMemo(
    () => (board.data ? rowsFrom(board.data.account_health, board.data.monitors, board.data.observed_accounts) : []),
    [board.data],
  )
  const refresh = useRefreshAll((board.data?.monitors ?? []).filter((m) => m.enabled).map((m) => m.id))

  if (board.isError) return <QueryError error={board.error} retry={() => void board.refetch()} />
  if (!board.data) return <TableSkeleton />

  const linked = rows.filter((r) => r.linked)
  const unlinked = rows.filter((r) => !r.linked)
  const reached = rows.filter((r) => r.status === 'limit').length
  const available = linked.filter((r) => r.status === 'available').length
  const trackingProblems = rows.filter((r) => r.tracking.state === 'failed').length
  const nearBudget = (budgets.data?.items ?? []).filter((u) => u.state === 'warning' || u.state === 'exceeded').length

  const cols: Column<Row>[] = [
    {
      id: 'name',
      header: 'Account',
      mobile: 'title',
      cell: (r) => (
        <Stack gap={0} miw={0}>
          <Text size="sm" fw={500} truncate>
            {r.name}
          </Text>
          <Text size="xs" c="dimmed" truncate>
            {r.sub}
          </Text>
        </Stack>
      ),
    },
    { id: 'status', header: 'Status', mobile: 'aside', cell: (r) => <LimitStatusBadge row={r} detail /> },
    { id: 'window', header: 'Tightest window', mobile: 'line', width: '30%', cell: (r) => <WindowCell t={r.tightest} /> },
    {
      id: 'reset',
      header: 'Resets',
      mobile: 'meta',
      cell: (r) => <Text size="xs" c="dimmed">{r.tightest?.reset ? resetsIn(r.tightest.reset) : '—'}</Text>,
    },
    { id: 'tracking', header: 'Quota tracking', mobile: 'meta', cell: (r) => <TrackingLine t={r.tracking} /> },
  ]

  return (
    <Stack gap="xl">
      <Group justify="space-between" gap="sm" wrap="wrap">
        <Text size="md">{answer(rows)}</Text>
        <Group gap="xs">
          {canWrite && board.data.monitors.length > 0 && (
            <Button icon={RefreshCw} busy={refresh.isPending} onClick={() => refresh.mutate()}>
              Refresh all
            </Button>
          )}
          {canWrite && (
            <Button variant="primary" icon={Plus} onClick={() => setAdding(true)}>
              Add budget
            </Button>
          )}
        </Group>
      </Group>
      <MutationError error={refresh.error} />
      {board.data.dropped_observations > 0 && (
        <Banner tone="warning" icon={AlertTriangle}>
          Some rate-limit readings were dropped under load ({board.data.dropped_observations} since the server started).
        </Banner>
      )}
      <StatGrid cols={4} label="Limits at a glance">
        <Stat label="Accounts available" value={`${available} of ${linked.length}`} tone={available < linked.length ? 'warning' : 'success'} />
        <Stat label="Limits reached" value={reached} tone={reached ? 'error' : 'default'} hint={reached ? 'Routing skips these until reset' : 'None'} />
        <Stat label="Budgets near limit" value={nearBudget} tone={nearBudget ? 'warning' : 'default'} hint={`${budgets.data?.items?.length ?? 0} budgets`} />
        <Stat
          label="Tracking problems"
          value={trackingProblems}
          tone={trackingProblems ? 'error' : 'default'}
          hint={trackingProblems ? 'Credentials need attention' : 'All trackers checking'}
          to="/quotas/monitors"
        />
      </StatGrid>
      <Section title="Accounts" info="What routing sees for each provider account: its status and the window with the least room left. Open an account for every window and its forecast.">
        <DataTable label="Account list" rows={linked} columns={cols} rowKey={(r) => r.key} onRow={setOpen} empty={<Empty title="No provider accounts yet" />} />
      </Section>
      {unlinked.length > 0 && (
        <Section title="Not linked to an account" info="Quota trackers that are watched but do not affect routing.">
          <DataTable label="Unlinked tracker list" rows={unlinked} columns={cols.map((c) => (c.id === 'name' ? { ...c, header: 'Tracker' } : c))} rowKey={(r) => r.key} onRow={setOpen} />
        </Section>
      )}
      <Budgets onAdd={() => setAdding(true)} />
      {adding && <BudgetDialog budget={null} onClose={() => setAdding(false)} />}
      <AccountDrawer row={open} onClose={() => setOpen(null)} />
    </Stack>
  )
}
