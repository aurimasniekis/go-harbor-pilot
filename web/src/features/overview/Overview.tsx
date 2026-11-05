import { type ReactNode, useState } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { ActionIcon, Group, Paper, Progress, SimpleGrid, Skeleton, Stack, Text, Title, Tooltip, UnstyledButton } from '@mantine/core'
import { ArrowRight, CircleAlert, CircleCheck, Info, TriangleAlert, X } from 'lucide-react'
import { attentionQuery, budgetStatusQuery, quotaBoardQuery, requestsQuery, resourceListQuery, principalsQuery, oidcProvidersQuery, usersQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { AccountRes, AttentionItem, BindingRes } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { fmtDateTime, fmtDuration, fmtTime, serverNow } from '@/lib/format'
import { delta, series, useDayStats } from '@/features/activity/useDayStats'
import { Empty, PageHeader, Stat, StatGrid, StatSkeleton, StatusDot } from '@/ui/kit'
import { costKindHelp, harnessName, money, ms, short } from '@/ui/fmt'
import o from './overview.module.css'

type Severity = AttentionItem['severity']

/**
 * Problems from the server's attention endpoint. Older servers without it
 * fall back to what the quota board and budgets report.
 */
function useAttention(): { items: AttentionItem[]; loading: boolean } {
  const canConfig = useCan('config.read')
  const canUsage = useCan('usage.read')
  const att = useQuery({ ...attentionQuery, enabled: canConfig })
  const fallback = canConfig ? att.data === null : canUsage
  const board = useQuery({ ...quotaBoardQuery, enabled: fallback && canUsage })
  const budgets = useQuery({ ...budgetStatusQuery, enabled: fallback && canUsage })
  if (!fallback) return { items: att.data?.items ?? [], loading: !att.data && !att.isError }
  const items: AttentionItem[] = []
  for (const m of board.data?.monitors ?? []) {
    if (m.health === 'auth_failed') items.push({ id: `mon-${m.id}`, severity: 'critical', area: 'quota', title: `Quota tracking for ${m.name} can't sign in`, link: '/quotas' })
    for (const b of m.buckets.filter((x) => x.exhausted && !x.reset_passed)) {
      items.push({ id: `b-${m.id}-${b.key}`, severity: 'warning', area: 'quota', title: `${b.label} limit reached on ${m.name}`, detail: b.reset_at ? `Resets ${fmtDateTime(b.reset_at)}` : undefined, link: '/quotas' })
    }
  }
  for (const b of budgets.data?.items ?? []) {
    if (b.state === 'exceeded' || b.state === 'warning') {
      items.push({ id: `budget-${b.budget_id}`, severity: b.state === 'exceeded' ? 'critical' : 'warning', area: 'budget', title: `Budget ${b.name} is ${Math.round(b.fraction * 100)}% spent`, link: '/quotas#budgets' })
    }
  }
  const rank: Record<Severity, number> = { critical: 0, warning: 1, info: 2 }
  items.sort((a, b) => rank[a.severity] - rank[b.severity])
  return { items, loading: canUsage && (!board.data || !budgets.data) }
}

const severityIcon: Record<Severity, ReactNode> = {
  critical: <CircleAlert size={16} color="var(--error)" aria-label="Critical" />,
  warning: <TriangleAlert size={16} color="var(--warning)" aria-label="Warning" />,
  info: <Info size={16} color="var(--info)" aria-label="Information" />,
}

/** The answer to "is everything OK?": a short list of what needs fixing, or one calm line. */
function NeedsAttention({ calm }: { calm: ReactNode }) {
  const { items, loading } = useAttention()
  const [all, setAll] = useState(false)
  if (loading) return <Skeleton height={64} radius="md" />
  if (items.length === 0) {
    return (
      <Paper withBorder radius="md" p="md" className={o.allClear} role="status">
        <Group gap="sm" wrap="nowrap">
          <CircleCheck size={22} color="var(--success)" aria-hidden="true" />
          <div>
            <Title order={2} size="h3">
              All clear
            </Title>
            <Text size="sm" c="dimmed">
              {calm}
            </Text>
          </div>
        </Group>
      </Paper>
    )
  }
  const shown = all ? items : items.slice(0, 5)
  const critical = items.some((i) => i.severity === 'critical')
  return (
    <section aria-labelledby="attention-title" className={o.attentionBox} data-tone={critical ? 'critical' : 'warning'}>
      <div className={o.attentionHead}>
        <Title order={2} size="h3" id="attention-title">
          {items.length === 1 ? '1 thing needs your attention' : `${items.length} things need your attention`}
        </Title>
      </div>
      <ul className={o.attention}>
        {shown.map((i) => (
          <li key={i.id}>
            <Link to={i.link} className={o.attentionItem}>
              {severityIcon[i.severity]}
              <span className={o.attentionText}>
                <span className={o.attentionTitle}>{i.title}</span>
                {i.detail && <span className={o.attentionDetail}>{i.detail}</span>}
              </span>
              <span className={o.fix}>
                Fix <ArrowRight size={14} aria-hidden="true" />
              </span>
            </Link>
          </li>
        ))}
      </ul>
      {items.length > 5 && !all && (
        <UnstyledButton onClick={() => setAll(true)} className={o.showAll}>
          Show all {items.length}
        </UnstyledButton>
      )}
    </section>
  )
}

function DayStats() {
  const { query, totals, previous, hours } = useDayStats()
  if (query.isError || !totals) return <StatSkeleton count={4} />
  const failed = totals.errors + totals.blocked
  const errorRate = totals.requests ? (failed / totals.requests) * 100 : 0
  const cost = totals.costs.find((c) => c.kind === 'estimated_api_charge')
  const prevCost = Number(previous?.costs.find((c) => c.kind === 'estimated_api_charge')?.amount ?? 0)
  return (
    <StatGrid cols={4} label="Last 24 hours">
      <Stat label="Requests · 24h" value={short(totals.requests)} hint={delta(totals.requests, previous?.requests) ?? 'Last 24 hours'} spark={series(hours, (g) => g.requests)} to="/activity" />
      <Stat
        label="Error rate"
        value={`${errorRate.toFixed(errorRate > 0 && errorRate < 10 ? 1 : 0)}%`}
        tone={failed > 0 ? 'error' : 'default'}
        hint={failed > 0 ? `${failed} failed or blocked` : 'No failed requests'}
        spark={series(hours, (g) => g.errors + g.blocked)}
        to="/activity"
      />
      <Stat
        label="Fallbacks"
        value={totals.failovers}
        tone={totals.failovers > 0 ? 'warning' : 'default'}
        hint={totals.failovers > 0 ? 'Served by a backup account' : 'None needed'}
        info="The first account failed before any output, so the routing plan used the next one."
        to="/activity"
      />
      <Stat
        label="Estimated cost · 24h"
        value={cost ? money(cost.amount, cost.currency) : '—'}
        hint={(cost && delta(Number(cost.amount), prevCost || undefined)) || 'Last 24 hours'}
        info={costKindHelp.estimated_api_charge}
        spark={series(hours, (g) => Number(g.costs.find((c) => c.kind === 'estimated_api_charge')?.amount ?? 0))}
        to="/usage"
      />
    </StatGrid>
  )
}

function resetsIn(iso: string | null): string {
  if (!iso) return ''
  const left = new Date(iso).getTime() - serverNow()
  return left > 0 ? `resets in ${fmtDuration(left)}` : 'reset due'
}

function quotaColor(left: number): string {
  if (left < 0.1) return 'red'
  if (left < 0.25) return 'yellow'
  return 'green'
}

/** The quota windows and budgets with the least room left. */
function ClosestToLimit() {
  const board = useQuery(quotaBoardQuery)
  const budgets = useQuery(budgetStatusQuery)
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const acc = byId(accounts.data?.items)
  const windows = (board.data?.monitors ?? [])
    .flatMap((m) =>
      m.buckets
        .filter((b) => b.remaining_fraction !== null && !b.reset_passed)
        .map((b) => ({ key: `${m.id}-${b.key}`, who: (m.account_id && acc[m.account_id]?.name) || m.name, label: b.label, left: b.remaining_fraction as number, reset: b.reset_at, stale: b.stale })),
    )
    .sort((a, b) => a.left - b.left)
    .slice(0, 3)
  return (
    <Paper withBorder radius="md" className={o.card}>
      <div className={o.cardHead}>
        <Title order={2} size="h4">
          Closest to a limit
        </Title>
        <Link to="/quotas" className={o.more}>
          All limits
        </Link>
      </div>
      <Stack gap="md" px="md" pb="md">
        {!board.data ? (
          <Skeleton height={90} />
        ) : windows.length === 0 ? (
          <Text size="sm" c="dimmed">
            No quota windows are tracked yet.
          </Text>
        ) : (
          windows.map((w) => (
            <div key={w.key} className={o.limit}>
              <Group justify="space-between" gap="xs" wrap="nowrap">
                <Text size="sm" truncate>
                  {w.who} <span className="muted">· {w.label}</span>
                </Text>
                <Text size="xs" c="dimmed" className="num" style={{ whiteSpace: 'nowrap' }}>
                  {w.left <= 0 ? 'Used up' : `${Math.round(w.left * 100)}% left`}
                  {w.reset ? ` · ${resetsIn(w.reset)}` : ''}
                  {w.stale ? ' · stale' : ''}
                </Text>
              </Group>
              <Progress value={Math.max(1, (1 - w.left) * 100)} color={quotaColor(w.left)} size={8} radius="xl" aria-label={`${w.who} ${w.label} used`} />
            </div>
          ))
        )}
        {(budgets.data?.items ?? []).map((b) => (
          <Group key={b.budget_id} justify="space-between" gap="xs" wrap="nowrap">
            <Text size="sm" truncate>
              Budget {b.name}
            </Text>
            <Text size="xs" c="dimmed" className="num" style={{ whiteSpace: 'nowrap' }}>
              {money(b.spent_e10 / 1e10, b.currency)} of {money(b.limit_e10 / 1e10, b.currency)} · {(b.fraction * 100).toFixed(b.fraction < 0.1 ? 1 : 0)}%
            </Text>
          </Group>
        ))}
      </Stack>
    </Paper>
  )
}

function LatestRequests() {
  const q = useQuery(requestsQuery({ limit: 5 }))
  const bindings = useQuery(resourceListQuery<BindingRes>('client-bindings'))
  const client = byId(bindings.data?.items)
  const navigate = useNavigate()
  const items = q.data?.items ?? []
  return (
    <Paper withBorder radius="md" className={o.card}>
      <div className={o.cardHead}>
        <Title order={2} size="h4">
          Latest requests
        </Title>
        <Link to="/activity" className={o.more}>
          All requests
        </Link>
      </div>
      {!q.data ? (
        <Skeleton height={160} m="md" />
      ) : items.length === 0 ? (
        <Empty title="No requests yet" action={<Link to="/clients/setup">Connect a tool</Link>} />
      ) : (
        <ul className={o.recent}>
          {items.map((r) => (
            <li key={r.id}>
              <UnstyledButton className={o.recentItem} onClick={() => void navigate({ to: '/activity/$requestId', params: { requestId: r.id } })}>
                <span className={o.recentMain}>
                  <span className={o.recentTitle}>
                    {client[r.binding_id]?.name ?? 'Unknown client'}
                    <span className="muted"> · {harnessName[r.harness] ?? (r.harness || 'unknown tool')}</span>
                  </span>
                  <span className={o.recentSub}>
                    {r.requested_model}
                    {r.final_model && r.final_model !== r.requested_model && ` → ${r.final_model}`}
                  </span>
                </span>
                <span className={o.recentAside}>
                  <Tooltip label={fmtDateTime(r.created_at, { seconds: true })}>
                    <span>{fmtTime(r.created_at)}</span>
                  </Tooltip>
                  {r.status !== 'ok' ? (
                    <StatusDot status={r.status} label={r.status === 'blocked' ? 'Blocked' : 'Failed'} />
                  ) : r.attempts > 1 ? (
                    <span style={{ color: 'var(--warning)' }}>Fallback</span>
                  ) : (
                    <span>{ms(r.duration_ms)}</span>
                  )}
                </span>
              </UnstyledButton>
            </li>
          ))}
        </ul>
      )}
    </Paper>
  )
}

const SETUP_KEY = 'hp.setup.dismissed'

function Setup() {
  const users = useQuery(usersQuery)
  const principals = useQuery(principalsQuery)
  const sso = useQuery({ ...oidcProvidersQuery, enabled: useCan('auth.settings') })
  const [dismissed, setDismissed] = useState(() => {
    try {
      return localStorage.getItem(SETUP_KEY) === '1'
    } catch {
      return false
    }
  })
  if (dismissed || !users.data || !principals.data) return null
  const steps: [boolean, ReactNode][] = [
    [(users.data.items?.length ?? 0) > 1, <Link to="/settings/users">Invite your team</Link>],
    [(sso.data?.items?.length ?? 0) > 0, <Link to="/settings/auth">Connect single sign-on</Link>],
    [(principals.data.items?.length ?? 0) > 0, <Link to="/settings/service-principals">Create an API token for Terraform or CI</Link>],
  ]
  const done = steps.filter(([d]) => d).length
  if (done === steps.length) return null
  return (
    <Paper withBorder radius="md" className={o.card}>
      <div className={o.cardHead}>
        <Title order={2} size="h4">
          Finish setting up
        </Title>
        <Group gap={4}>
          <Text size="xs" c="dimmed">
            {done} of {steps.length}
          </Text>
          <ActionIcon
            variant="subtle"
            color="gray"
            size="sm"
            aria-label="Hide setup checklist"
            onClick={() => {
              try {
                localStorage.setItem(SETUP_KEY, '1')
              } catch {
                /* storage unavailable */
              }
              setDismissed(true)
            }}
          >
            <X size={14} />
          </ActionIcon>
        </Group>
      </div>
      <Stack gap={6} px="md" pb="md">
        {steps
          .filter(([d]) => !d)
          .map(([, label], i) => (
            <Group key={i} gap={8} wrap="nowrap">
              <span className={o.todo} role="img" aria-label="To do" />
              <Text size="sm">{label}</Text>
            </Group>
          ))}
      </Stack>
    </Paper>
  )
}

function CalmLine() {
  const { totals } = useDayStats()
  const board = useQuery(quotaBoardQuery)
  const accounts = board.data?.account_health ?? []
  const available = accounts.filter((a) => a.available).length
  const parts = [
    accounts.length ? `${available} of ${accounts.length} accounts available` : undefined,
    totals ? `${totals.errors + totals.blocked} errors in 24h` : undefined,
  ].filter(Boolean)
  return <>{parts.join(' · ') || 'Nothing needs you right now.'}</>
}

export function Overview() {
  const canActivity = useCan('activity.read')
  const canUsage = useCan('usage.read')
  const isAdmin = useCan('users.manage')
  return (
    <>
      <PageHeader title="Home" />
      <NeedsAttention calm={<CalmLine />} />
      {canActivity && <DayStats />}
      <SimpleGrid cols={{ base: 1, md: 2 }} spacing="md">
        {canUsage && <ClosestToLimit />}
        {canActivity && <LatestRequests />}
      </SimpleGrid>
      {isAdmin && <Setup />}
    </>
  )
}
