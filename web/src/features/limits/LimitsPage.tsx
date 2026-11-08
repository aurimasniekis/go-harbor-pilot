import { type ReactNode, useEffect, useMemo, useState } from 'react'
import { useLocation, useNavigate } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Group, Menu, Progress, Stack, Text, Tooltip } from '@mantine/core'
import { AlertTriangle, CalendarClock, CheckCircle2, CircleHelp, Clock3, KeyRound, OctagonAlert, PauseCircle, Pencil, Plus, RefreshCw, RotateCcw, ShieldAlert } from 'lucide-react'
import { Badge, Banner, Button, QuotaBar } from '@/components/ui'
import { ConfirmDialog, FormDrawer, MutationError } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { apiSend } from '@/lib/api/client'
import { budgetStatusQuery, quotaBoardQuery, resourceListQuery } from '@/lib/api/queries'
import { byId, useDeleteResource, useResetBudget } from '@/lib/api/resources'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import type { AccountHealthView, AccountRes, BindingRes, BucketView, BudgetRes, BudgetUsage, MonitorView, ProjectRes, QuotaMonitorRes, WorkspaceRes } from '@/lib/api/types'
import { fmtDateTime, fmtDuration, fmtRelative, fmtTime, serverNow } from '@/lib/format'
import { MonitorDialog } from '@/features/quota/MonitorDialog'
import { useCan } from '@/lib/session'
import { BudgetDialog } from '@/features/budgets/BudgetDialog'
import { budgetBadge, periodLabel, windowEnd } from '@/features/budgets/budgetSpec'
import { BucketRow } from '@/features/quota/BucketRow'
import { BucketDialog } from '@/features/quota/BucketDialog'
import { e10 } from '@/features/quota/quota'
import { RowMenu } from '@/features/config/ResourceList'
import { type Column, DataTable, Empty, Fields, Section, Stat, StatGrid } from '@/ui/kit'
import { money } from '@/ui/fmt'

/* ---------- derived state ---------- */

/** A window is used up only while its reported reset is still ahead. */
const usedUp = (b: BucketView) => b.exhausted && !b.reset_passed

type LimitStatus = 'available' | 'limit' | 'paused' | 'unavailable' | 'unknown' | 'untracked'

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
    const tracking = trackingOf(mons)
    // Never claim "Available" from data that is failing or out of date.
    if (status === 'available' && (tracking.state === 'failed' || tracking.state === 'stale')) {
      status = 'untracked'
      statusDetail = tracking.state === 'failed' ? `Quota tracking failed: ${tracking.detail}` : 'Quota data is out of date'
    }
    return {
      key: h.account_id,
      name: h.name,
      sub: billingName[h.billing] ?? h.billing.replaceAll('_', ' '),
      linked: true,
      status,
      statusDetail,
      tightest: tightestOf(buckets) ?? (h.remaining !== null && h.remaining !== undefined ? { label: 'Tightest window', left: h.remaining, reset: h.reset_at, stale: h.stale } : undefined),
      tracking,
      monitors: mons,
      observed: obs,
    }
  })
  for (const m of monitors.filter((x) => !x.account_id && x.enabled)) {
    const exhausted = m.buckets.find(usedUp)
    const tracking = trackingOf([m])
    const failing = tracking.state === 'failed' || tracking.state === 'stale'
    rows.push({
      key: m.id,
      name: m.name,
      sub: m.adapter_name || m.adapter,
      linked: false,
      status: exhausted ? 'limit' : failing ? 'untracked' : m.buckets.length ? 'available' : 'unknown',
      statusDetail: exhausted
        ? `${exhausted.label} used up`
        : failing
          ? tracking.state === 'failed'
            ? `Quota tracking failed: ${tracking.detail}`
            : 'Quota data is out of date'
          : undefined,
      tightest: tightestOf(m.buckets),
      tracking,
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
  untracked: { label: 'Unknown', tone: 'warning', icon: CircleHelp },
}

export function resetsIn(iso: string | null | undefined): string {
  if (!iso) return ''
  const left = new Date(iso).getTime() - serverNow()
  return left > 0 ? `resets in ${fmtDuration(left)}` : 'reset due'
}

/**
 * Reset time for a "Resets" column: "in 3d 19h", absolute time on hover.
 * The ends variant reads "Ends in 3d" (a one-time budget's end).
 */
export function ResetCell({ iso, variant = 'resets' }: { iso: string | null | undefined; variant?: 'resets' | 'ends' }) {
  if (!iso) return <Text size="xs" c="dimmed">—</Text>
  const left = new Date(iso).getTime() - serverNow()
  const ends = variant === 'ends'
  return (
    <Tooltip label={fmtDateTime(iso)}>
      <Text size="xs" c="dimmed" span>
        {left > 0 ? `${ends ? 'Ends in' : 'in'} ${fmtDuration(left)}` : ends ? 'Ended' : 'Due now'}
      </Text>
    </Tooltip>
  )
}

/** The "Resets / ends" cell of a budget row. */
function BudgetEndCell({ u }: { u: BudgetUsage }) {
  const w = windowEnd(u)
  if (w.kind === 'never') return <Text size="xs" c="dimmed">Never</Text>
  if (w.kind === 'ended') return <Text size="xs" c="dimmed">Ended</Text>
  return <ResetCell iso={w.iso} variant={w.kind} />
}

function BudgetBadge({ u }: { u: BudgetUsage }) {
  switch (budgetBadge(u)) {
    case 'blocking':
      return <Badge tone="error" icon={OctagonAlert}>Blocking</Badge>
    case 'over':
      return <Badge tone="error" icon={OctagonAlert}>Over budget</Badge>
    case 'near':
      return <Badge tone="warning" icon={AlertTriangle}>Near limit</Badge>
    case 'off':
      return <Badge>Off</Badge>
    case 'scheduled':
      return <Badge icon={CalendarClock}>Scheduled</Badge>
    case 'ended':
      return <Badge>Ended</Badge>
  }
  return <Badge tone="success" icon={CheckCircle2}>On track</Badge>
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
  if (!detail || (row.status !== 'paused' && row.status !== 'untracked') || !row.statusDetail) return badge
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
    <div style={{ minWidth: 160 }}>
      <QuotaBar label={t.label} srLabel={`${t.label} remaining`} percent={t.left * 100} stale={t.stale} />
    </div>
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
      <Tooltip label={`Warns at ${Math.round(u.warn_at * 100)}% of the limit`}>
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
  const [resetting, setResetting] = useState<{ b: BudgetRes; u: BudgetUsage } | null>(null)
  const del = useDeleteResource('budgets')
  const reset = useResetBudget()
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
            {u.scope === 'global' ? 'Everything' : `${scopeNoun[u.scope] ?? u.scope} ${scopeName(u.scope_id)}`} · {periodLabel(u.period)}
          </Text>
        </Stack>
      ),
    },
    {
      id: 'spend',
      header: 'Spent',
      mobile: 'line',
      width: '34%',
      cell: (u) => {
        const projected = e10(u.projected_spend_e10)
        return (
          <Stack gap={4}>
            <Group justify="space-between" gap="xs" wrap="nowrap">
              <Text size="xs" className="num">
                {money(e10(u.spent_e10) ?? 0, u.currency)} of {money(e10(u.limit_e10) ?? 0, u.currency)} · {money(e10(u.remaining_e10) ?? 0, u.currency)} left
              </Text>
              {projected && (
                <Tooltip label="Where spend ends this period at the current rate">
                  <Text size="xs" c="dimmed" className="num">
                    Forecast {money(projected, u.currency)}
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
      width: 130,
      cell: (u) => <BudgetBadge u={u} />,
    },
    {
      id: 'mode',
      header: 'When reached',
      mobile: 'meta',
      width: 130,
      cell: (u) => (
        <Text size="xs" c="dimmed">
          {u.enforcement === 'block' ? 'Blocks requests' : 'Warns only'}
        </Text>
      ),
    },
    { id: 'reset', header: 'Resets / ends', mobile: 'meta', width: 120, cell: (u) => <BudgetEndCell u={u} /> },
    {
      id: 'actions',
      header: <span className="visually-hidden">Actions</span>,
      align: 'right',
      width: 48,
      mobile: 'hidden',
      cell: (u) =>
        res[u.budget_id] && canWrite ? (
          <RowMenu
            name={u.name}
            onEdit={() => setEditing(res[u.budget_id]!)}
            onDelete={() => setDeleting(res[u.budget_id]!)}
            extra={
              u.period === 'once' && (
                <Menu.Item leftSection={<RotateCcw size={14} />} onClick={() => setResetting({ b: res[u.budget_id]!, u })} aria-label={`Start over ${u.name}`}>
                  Start over…
                </Menu.Item>
              )
            }
          />
        ) : null,
    },
  ]
  return (
    <Section title="Budgets" id="budgets" info="Spend caps per workspace, project, client or account. A blocking budget stops new requests before the limit is crossed.">
      <MutationError error={del.error} />
      <MutationError error={reset.error} />
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
      <ConfirmDialog
        open={!!resetting}
        title={`Start ${resetting?.u.name ?? ''} over?`}
        message={
          <Stack gap="sm">
            <TerraformEditWarning r={resetting?.b ?? null} />
            <Text size="sm">
              Start counting again from now? {resetting ? money(e10(resetting.u.spent_e10) ?? 0, resetting.u.currency) : ''} already spent stops counting toward this budget.
              It stays in usage reports.
            </Text>
          </Stack>
        }
        confirmLabel="Start over"
        danger={false}
        onCancel={() => setResetting(null)}
        onConfirm={() => {
          if (resetting) reset.mutate({ id: resetting.b.id, revision: resetting.b.revision })
          setResetting(null)
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
  const canWrite = useCan('config.write')
  const monitors = useQuery({ ...resourceListQuery<QuotaMonitorRes>('quota-monitors'), enabled: !!row && canWrite })
  const qc = useQueryClient()
  const [editing, setEditing] = useState<QuotaMonitorRes | null>(null)
  const ids = row?.monitors.map((m) => m.id) ?? []
  const check = useMutation({
    mutationFn: () => Promise.allSettled(ids.map((id) => apiSend('POST', `/quota/monitors/${encodeURIComponent(id)}/refresh`))),
    onSuccess: () => setTimeout(() => void qc.invalidateQueries({ queryKey: ['quota', 'board'] }), 1500),
  })
  const tracker = row && row.monitors.length > 0 ? (monitors.data?.items ?? []).find((m) => m.id === row.monitors[0]!.id) : undefined
  const failing = row?.tracking.state === 'failed'
  return (
    <>
      <FormDrawer
        open={!!row}
        onClose={() => {
          check.reset()
          onClose()
        }}
        size="lg"
        dirty={false}
        title={
          row && (
            <Group gap="sm">
              <span>{row.name}</span>
              <LimitStatusBadge row={row} />
            </Group>
          )
        }
        footer={
          row && canWrite && row.monitors.length > 0 ? (
            <>
              {check.isSuccess && (
                <Text size="xs" c="dimmed" mr="auto">
                  Checking {ids.length === 1 ? 'the tracker' : `${ids.length} trackers`}…
                </Text>
              )}
              <Button icon={RefreshCw} busy={check.isPending} onClick={() => check.mutate()}>
                Check now
              </Button>
              {tracker && (
                <Button variant={failing ? 'primary' : 'default'} icon={failing ? KeyRound : Pencil} onClick={() => setEditing(tracker)}>
                  {failing ? 'Fix sign-in' : 'Edit tracker'}
                </Button>
              )}
            </>
          ) : undefined
        }
      >
        {row && <AccountQuota row={row} />}
      </FormDrawer>
      {editing && <MonitorDialog monitor={editing} onClose={() => setEditing(null)} />}
    </>
  )
}

/* ---------- page ---------- */

function useRefreshAll(ids: string[]) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      const res = await Promise.allSettled(ids.map((id) => apiSend('POST', `/quota/monitors/${encodeURIComponent(id)}/refresh`)))
      return { total: res.length, failed: res.filter((r) => r.status === 'rejected').length }
    },
    onSuccess: () => setTimeout(() => void qc.invalidateQueries({ queryKey: ['quota', 'board'] }), 1500),
  })
}

/** "Checking 7 trackers · 1 could not start", shown after Refresh all. */
export function refreshSummary(r: { total: number; failed: number }): string {
  const n = r.total === 1 ? '1 tracker' : `${r.total} trackers`
  return r.failed ? `Checking ${n} · ${r.failed} could not start` : `Checking ${n}; results appear in a few seconds`
}

function answer(rows: Row[]): ReactNode {
  const reached = rows.filter((r) => r.status === 'limit')
  const tight = rows.filter((r) => r.tightest && r.status !== 'limit').sort((a, b) => a.tightest!.left - b.tightest!.left)[0]
  return (
    <Stack gap={4}>
      <Group gap={6} wrap="nowrap">
        {reached.length ? <OctagonAlert size={16} color="var(--error)" aria-hidden /> : <CheckCircle2 size={16} color="var(--success)" aria-hidden />}
        <Text size="sm">
          {reached.length ? `Limit reached: ${reached.map((r) => r.name).join(', ')}` : 'No account has reached a limit'}
        </Text>
      </Group>
      {tight?.tightest && (
        <Group gap={6} wrap="nowrap">
          <Clock3 size={16} color="var(--text-muted)" aria-hidden />
          <Text size="sm" c="dimmed">
            Closest: {tight.name} · {tight.tightest.label} · {Math.round(tight.tightest.left * 100)}% left
            {tight.tightest.reset ? ` · ${resetsIn(tight.tightest.reset)}` : ''}
          </Text>
        </Group>
      )}
    </Stack>
  )
}

export function LimitsOverview({ addBudget = false }: { addBudget?: boolean }) {
  const board = useQuery(quotaBoardQuery)
  const budgets = useQuery(budgetStatusQuery)
  const canWrite = useCan('config.write')
  const navigate = useNavigate()
  const location = useLocation()
  const [adding, setAdding] = useState(addBudget)
  useEffect(() => setAdding(addBudget), [addBudget])
  const closeAdd = () => {
    setAdding(false)
    // Drop ?add=budget so reload and back do not reopen the dialog.
    if (addBudget) void navigate({ to: '/quotas', search: {}, replace: true })
  }
  // Scroll to #budgets once the sections have rendered (palette and Home links).
  useEffect(() => {
    if (location.hash === 'budgets' && board.data && budgets.data) document.getElementById('budgets')?.scrollIntoView({ block: 'start' })
  }, [location.hash, board.data, budgets.data])
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
    { id: 'status', header: 'Status', mobile: 'aside', width: 170, cell: (r) => <LimitStatusBadge row={r} detail /> },
    { id: 'window', header: 'Tightest window', mobile: 'line', width: '30%', cell: (r) => <WindowCell t={r.tightest} /> },
    { id: 'reset', header: 'Resets', mobile: 'meta', width: 110, cell: (r) => <ResetCell iso={r.tightest?.reset} /> },
    { id: 'tracking', header: 'Quota tracking', mobile: 'meta', width: 170, cell: (r) => <TrackingLine t={r.tracking} /> },
  ]

  return (
    <Stack gap="xl">
      <Group justify="space-between" gap="sm" wrap="wrap" align="flex-start">
        {answer(rows)}
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
      {refresh.data && (
        <Text size="xs" c="dimmed" role="status">
          {refreshSummary(refresh.data)}
        </Text>
      )}
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
        <DataTable fixed label="Account list" rows={linked} columns={cols} rowKey={(r) => r.key} onRow={setOpen} empty={<Empty title="No provider accounts yet" />} />
      </Section>
      {unlinked.length > 0 && (
        <Section title="Not linked to an account" info="Quota trackers that are watched but do not affect routing.">
          <DataTable fixed label="Unlinked tracker list" rows={unlinked} columns={cols.map((c) => (c.id === 'name' ? { ...c, header: 'Tracker' } : c))} rowKey={(r) => r.key} onRow={setOpen} />
        </Section>
      )}
      <Budgets onAdd={() => setAdding(true)} />
      {adding && <BudgetDialog budget={null} onClose={closeAdd} />}
      <AccountDrawer row={open} onClose={() => setOpen(null)} />
    </Stack>
  )
}
