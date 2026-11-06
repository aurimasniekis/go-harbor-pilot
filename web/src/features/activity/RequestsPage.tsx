import { Fragment, useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useSearch } from '@tanstack/react-router'
import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { Autocomplete, Badge, Button, Divider, Group, Pill, SegmentedControl, Popover, Select, Skeleton, Stack, Text, Tooltip, UnstyledButton } from '@mantine/core'
import { Activity as ActivityIcon, ArrowLeft, CalendarDays, Filter, Search } from 'lucide-react'
import { z } from 'zod'
import { QueryError } from '@/components/states'
import { apiGet } from '@/lib/api/client'
import { resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { AccountRes, BindingRes, Page, ProjectRes, RequestSummary } from '@/lib/api/types'
import { fmtDateTime, fmtRelative, getDisplayTimeZone, isKnown, serverNow } from '@/lib/format'
import { Empty, Stat, StatGrid, StatSkeleton, Unknown, useCompact } from '@/ui/kit'
import { costKindHelp, costKindName, harnessName, human, money, ms, short } from '@/ui/fmt'
import { refLabel, refsSearch, withRef } from '@/features/refs/refs'
import { RefForm } from '@/features/refs/RefPicker'
import { delta, series, useDayStats } from './useDayStats'
import s from './activity.module.css'

/** Time ranges of the request log: days, or all time. Numbers stay numbers in the URL. */
const rangeValue = z
  .union([z.literal('all'), z.coerce.number().pipe(z.union([z.literal(1), z.literal(7), z.literal(30)]))])
  .optional()
  .catch(undefined)

export const requestSearch = z.object({
  status: z.enum(['error', 'blocked']).optional().catch(undefined),
  harness: z.string().optional().catch(undefined),
  binding_id: z.string().optional().catch(undefined),
  account_id: z.string().optional().catch(undefined),
  project_id: z.string().optional().catch(undefined),
  session_id: z.string().optional().catch(undefined),
  /** Trusted references, `key:value` (all must match, at most 4). */
  ref: refsSearch,
  /** Request id or the start of a requested or served model name. */
  q: z.string().optional().catch(undefined),
  /** Older links: same as q. */
  model: z.string().optional().catch(undefined),
  fallbacks: z.boolean().optional().catch(undefined),
  range: rangeValue,
})

/** Default range of the log, matching the 24-hour headline numbers above it. */
export const DEFAULT_LOG_RANGE = 1

const rangeOptions = [
  { value: '1', label: 'Last 24 hours' },
  { value: '7', label: 'Last 7 days' },
  { value: '30', label: 'Last 30 days' },
  { value: 'all', label: 'All time' },
]

export type RequestSearch = z.infer<typeof requestSearch>

const PAGE = 50

/** Headline numbers for the last 24 hours. */
export function ActivityStats() {
  const { query, totals, previous, hours } = useDayStats()
  if (query.isError) return <QueryError error={query.error} retry={() => void query.refetch()} />
  if (!totals) return <StatSkeleton count={4} />
  const failed = totals.errors + totals.blocked
  const rate = totals.requests ? (failed / totals.requests) * 100 : 0
  return (
    <StatGrid cols={4} label="All requests in the last 24 hours">
      <Stat label="Requests" value={short(totals.requests)} hint={delta(totals.requests, previous?.requests) ?? 'No earlier data to compare'} spark={series(hours, (g) => g.requests)} />
      <Stat
        label="Error rate"
        value={`${rate.toFixed(rate > 0 && rate < 10 ? 1 : 0)}%`}
        tone={failed > 0 ? 'error' : 'default'}
        hint={failed > 0 ? `${failed} failed or blocked` : 'No failed requests'}
        spark={series(hours, (g) => g.errors + g.blocked)}
      />
      <Stat label="Fallbacks" value={totals.failovers} tone={totals.failovers > 0 ? 'warning' : 'default'} hint={totals.failovers > 0 ? 'Served by a backup account' : 'None needed'} />
      {totals.duration.p95_ms !== null ? (
        <Stat label="Latency p95" value={ms(totals.duration.p95_ms)} hint={`Median ${ms(totals.duration.p50_ms)}`} spark={series(hours, (g) => g.duration.p95_ms ?? 0)} />
      ) : (
        <Stat label="Latency p95" value="—" hint="No finished requests yet" />
      )}
    </StatGrid>
  )
}

function dayLabel(iso: string): string {
  const tz = getDisplayTimeZone()
  const fmt = new Intl.DateTimeFormat('en-CA', { timeZone: tz, year: 'numeric', month: '2-digit', day: '2-digit' })
  const day = fmt.format(new Date(iso))
  const now = serverNow()
  if (day === fmt.format(now)) return 'Today'
  if (day === fmt.format(now - 86_400_000)) return 'Yesterday'
  return new Intl.DateTimeFormat('en-GB', { timeZone: tz, weekday: 'short', day: 'numeric', month: 'short' }).format(new Date(iso))
}

function clock(iso: string): string {
  return new Intl.DateTimeFormat('en-GB', { timeZone: getDisplayTimeZone(), hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }).format(new Date(iso))
}

/** Headline caption: what the numbers cover, and when the log last refreshed. */
export function StatsCaption({ updatedAt }: { updatedAt?: number }) {
  return (
    <Group justify="space-between" gap="xs" wrap="nowrap">
      <Text size="xs" c="dimmed" fw={500}>
        All requests · last 24 hours
      </Text>
      {updatedAt ? (
        <Text size="xs" c="dimmed" className={s.live} aria-live="polite">
          <span className={s.liveDot} aria-hidden="true" />
          Live · updated {fmtRelative(new Date(updatedAt).toISOString(), Date.now())}
        </Text>
      ) : null}
    </Group>
  )
}

/** Status only when something is off: fallback, error or blocked. */
function Status({ r }: { r: RequestSummary }) {
  if (r.status === 'error') return <Badge color="red">{r.error_kind ? human(r.error_kind) : 'Failed'}</Badge>
  if (r.status === 'blocked') return <Badge color="yellow">Blocked</Badge>
  if (r.status === 'in_progress') return <Badge color="teal">Running</Badge>
  if (r.status === 'canceled') return <Badge color="gray">Canceled</Badge>
  if (r.attempts > 1)
    return (
      <Tooltip label="The first account failed before any output, so the next one in the plan served it">
        <Badge color="yellow" variant="light">
          Fallback
        </Badge>
      </Tooltip>
    )
  return null
}

function rowTone(r: RequestSummary): 'error' | 'warning' | undefined {
  if (r.status === 'error') return 'error'
  if (r.status === 'blocked' || r.attempts > 1) return 'warning'
  return undefined
}

function Model({ r }: { r: RequestSummary }) {
  const served = r.final_model && r.final_model !== r.requested_model ? r.final_model : ''
  return (
    <Tooltip label={[r.requested_model, served].filter(Boolean).join(' → ')} openDelay={400}>
      <span className={s.model}>
        <span>{r.requested_model || '—'}</span>
        {served && (
          <>
            <span className={s.arrow}>→</span>
            <span>{served}</span>
          </>
        )}
      </span>
    </Tooltip>
  )
}

/** Latency, or unknown when the request is still running or reported no timing. */
function Latency({ r }: { r: RequestSummary }) {
  if (!isKnown(r.duration_ms)) return <Unknown reason="Not finished yet" />
  if (r.duration_ms === 0 && r.status !== 'in_progress') return <Unknown reason="Timing was not recorded for this request" />
  return <>{ms(r.duration_ms)}</>
}

/** Per-request cost with one precision for the whole column: 4 decimals under 1, else 2. */
export function requestCost(amount: string | number, currency: string): string {
  const n = Number(amount)
  if (!Number.isFinite(n) || Math.abs(n) >= 1 || n === 0) return money(amount, currency)
  const body = Math.abs(n).toFixed(4)
  const sym = ({ USD: '$', EUR: '€', GBP: '£' } as Record<string, string>)[currency]
  return `${n < 0 ? '-' : ''}${sym ?? `${currency} `}${body}`
}

function Cost({ r }: { r: RequestSummary }) {
  if (!r.cost) return <Unknown reason="No price configured, or usage not reported" />
  return (
    <Tooltip label={`${costKindName[r.cost.kind] ?? r.cost.kind}. ${costKindHelp[r.cost.kind] ?? ''}`}>
      <span style={{ fontVariantNumeric: 'tabular-nums' }}>{requestCost(r.cost.amount, r.cost.currency)}</span>
    </Tooltip>
  )
}

/** The request log with filters; a row opens the request page. */
export function RequestsPage() {
  const f = useSearch({ strict: false }) as RequestSearch
  const navigate = useNavigate()
  const compact = useCompact()
  const bindings = useQuery(resourceListQuery<BindingRes>('client-bindings'))
  const projects = useQuery(resourceListQuery<ProjectRes>('projects'))
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const client = byId(bindings.data?.items)
  const project = byId(projects.data?.items)
  const account = byId(accounts.data?.items)
  const query = f.q ?? f.model
  const [text, setText] = useState(query ?? '')
  useEffect(() => setText(query ?? ''), [query])
  const [moreOpen, setMoreOpen] = useState(false)
  // A session or run view covers the whole session or run, however old.
  const scoped = !!f.session_id || !!f.ref?.length
  const range = f.range ?? (scoped ? 'all' : DEFAULT_LOG_RANGE)
  // Range start rounded to the minute so the query key stays stable.
  const days = range === 'all' ? 0 : range
  const from = days ? new Date(Math.floor((serverNow() - days * 86_400_000) / 60_000) * 60_000).toISOString() : undefined
  const filter = {
    status: f.status,
    fallbacks: f.fallbacks || undefined,
    harness: f.harness,
    binding_id: f.binding_id,
    account_id: f.account_id,
    project_id: f.project_id,
    session_id: f.session_id,
    ref: f.ref,
    q: query,
    from,
  }
  const list = useInfiniteQuery({
    queryKey: ['activity', 'requests', 'log', filter],
    initialPageParam: '',
    queryFn: ({ pageParam, signal }) => apiGet<Page<RequestSummary> & { total?: number }>('/activity/requests', { limit: PAGE, ...filter, ...(pageParam ? { cursor: pageParam } : {}) }, signal),
    getNextPageParam: (last) => last.next_cursor || undefined,
    // Refresh the newest page only; refetching every loaded page grows with "Load more".
    refetchInterval: (q) => ((q.state.data?.pages.length ?? 1) > 1 ? false : 15_000),
  })
  const [, tick] = useState(0)
  useEffect(() => {
    const t = setInterval(() => tick((x) => x + 1), 5_000)
    return () => clearInterval(t)
  }, [])

  const set = (patch: Partial<RequestSearch>) => void navigate({ to: '/activity', search: { ...f, model: undefined, ...patch } })
  const open = (r: RequestSummary) => void navigate({ to: '/activity/$requestId', params: { requestId: r.id } })
  // Suggestions: clients by name or display name, and tools.
  const clientOptions = (bindings.data?.items ?? []).map((b) => ({ value: `client:${b.id}`, label: b.spec.display_name ? `${b.spec.display_name} (${b.name})` : b.name }))
  const toolOptions = Object.entries(harnessName).map(([k, label]) => ({ value: `tool:${k}`, label }))
  const pick = (value: string) => {
    if (value.startsWith('client:')) {
      setText('')
      set({ binding_id: value.slice(7), q: undefined })
    } else if (value.startsWith('tool:')) {
      setText('')
      set({ harness: value.slice(5), q: undefined })
    }
  }
  const submit = () => {
    const q = text.trim()
    if (!q) {
      set({ q: undefined })
      return
    }
    if (/^req_[a-z0-9]+$/i.test(q)) {
      void navigate({ to: '/activity/$requestId', params: { requestId: q } })
      return
    }
    const lower = q.toLowerCase()
    const clients = (bindings.data?.items ?? []).filter((b) => b.name.toLowerCase().includes(lower) || (b.spec.display_name ?? '').toLowerCase().includes(lower))
    const tools = toolOptions.filter((t) => t.label.toLowerCase().includes(lower) || t.value.slice(5).includes(lower))
    if (clients.length === 1 && tools.length === 0) return pick(`client:${clients[0]!.id}`)
    if (tools.length === 1 && clients.length === 0) return pick(tools[0]!.value)
    set({ q })
  }

  const items = useMemo(() => (list.data?.pages ?? []).flatMap((p) => p.items ?? []), [list.data])
  const total = list.data?.pages[0]?.total
  const chips: { key: string; label: string; remove: Partial<RequestSearch> }[] = []
  const chip = (key: keyof RequestSearch, label: string) => chips.push({ key, label, remove: { [key]: undefined } })
  if (f.binding_id) chip('binding_id', `Client: ${client[f.binding_id]?.name ?? f.binding_id}`)
  if (f.account_id) chip('account_id', `Account: ${account[f.account_id]?.name ?? f.account_id}`)
  if (f.harness) chip('harness', `Tool: ${harnessName[f.harness] ?? f.harness}`)
  if (f.project_id) chip('project_id', `Project: ${project[f.project_id]?.name ?? f.project_id}`)
  for (const r of f.ref ?? []) {
    const rest = f.ref!.filter((x) => x !== r)
    chips.push({ key: `ref:${r}`, label: refLabel(r), remove: { ref: rest.length ? rest : undefined } })
  }
  if (f.session_id) {
    const first = items[items.length - 1]
    const who = first ? (client[first.binding_id]?.spec.display_name ?? client[first.binding_id]?.name) : undefined
    chip('session_id', ['Session', who, first ? `started ${fmtDateTime(first.created_at)}` : undefined].filter(Boolean).join(' · '))
  }
  if (query) chip('q', `Search: ${query}`)
  const moreCount = [f.binding_id, f.harness, f.project_id].filter(Boolean).length + (f.ref?.length ?? 0)
  const statusView = f.fallbacks ? 'fallbacks' : (f.status ?? 'all')
  const filtered = chips.length > 0 || !!f.status || !!f.fallbacks
  const clearAll = () =>
    set({ status: undefined, fallbacks: undefined, harness: undefined, binding_id: undefined, account_id: undefined, project_id: undefined, session_id: undefined, ref: undefined, q: undefined })

  // Rows grouped by day, newest first.
  const groups: { day: string; rows: RequestSummary[] }[] = []
  for (const r of items) {
    const day = dayLabel(r.created_at)
    if (groups[groups.length - 1]?.day !== day) groups.push({ day, rows: [] })
    groups[groups.length - 1]!.rows.push(r)
  }

  return (
    <Stack gap="md">
      {f.session_id && (
        <Group gap={6}>
          <Button component={Link} to="/activity/sessions" variant="subtle" size="compact-sm" leftSection={<ArrowLeft size={14} />}>
            Back to sessions
          </Button>
        </Group>
      )}
      <Group gap="xs" wrap="wrap" role="search" aria-label="Filter requests" className={s.toolbar}>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            submit()
          }}
          className={s.search}
        >
          <Autocomplete
            aria-label="Search requests"
            placeholder="Client, tool, model or request ID"
            leftSection={<Search size={14} />}
            size={compact ? 'md' : 'sm'}
            value={text}
            onChange={setText}
            onOptionSubmit={pick}
            data={text.trim().length > 0 ? [{ group: 'Clients', items: clientOptions }, { group: 'Tools', items: toolOptions }] : []}
            limit={8}
            comboboxProps={{ withinPortal: true }}
          />
        </form>
        <SegmentedControl
          aria-label="Show"
          size={compact ? 'md' : 'sm'}
          value={statusView}
          onChange={(v) =>
            set(v === 'fallbacks' ? { status: undefined, fallbacks: true } : { status: v === 'all' ? undefined : (v as RequestSearch['status']), fallbacks: undefined })
          }
          data={[
            { value: 'all', label: 'All' },
            { value: 'error', label: 'Errors' },
            { value: 'blocked', label: 'Blocked' },
            { value: 'fallbacks', label: 'Fallbacks' },
          ]}
        />
        <Select
          aria-label="Time range"
          size={compact ? 'md' : 'sm'}
          w={170}
          leftSection={<CalendarDays size={14} />}
          allowDeselect={false}
          value={String(range)}
          onChange={(v) => v && set({ range: v === String(DEFAULT_LOG_RANGE) && !scoped ? undefined : v === 'all' ? 'all' : (Number(v) as 1 | 7 | 30) })}
          data={rangeOptions}
        />
        <Popover opened={moreOpen} onChange={setMoreOpen} position="bottom-end" width={280} shadow="md" withinPortal>
          <Popover.Target>
            <Button variant="default" size="sm" leftSection={<Filter size={14} />} onClick={() => setMoreOpen((o) => !o)}>
              {moreCount ? `Filters (${moreCount})` : 'More filters'}
            </Button>
          </Popover.Target>
          <Popover.Dropdown>
            <Stack gap="sm">
              <Select
                label="Client"
                placeholder="Any client"
                clearable
                searchable
                comboboxProps={{ withinPortal: false }}
                value={f.binding_id ?? null}
                onChange={(v) => set({ binding_id: v ?? undefined })}
                data={(bindings.data?.items ?? []).map((b) => ({ value: b.id, label: b.name }))}
              />
              <Select
                label="Tool"
                placeholder="Any tool"
                clearable
                comboboxProps={{ withinPortal: false }}
                value={f.harness ?? null}
                onChange={(v) => set({ harness: v ?? undefined })}
                data={Object.entries(harnessName).map(([value, label]) => ({ value, label }))}
              />
              <Select
                label="Project"
                placeholder="Any project"
                clearable
                comboboxProps={{ withinPortal: false }}
                value={f.project_id ?? null}
                onChange={(v) => set({ project_id: v ?? undefined })}
                data={(projects.data?.items ?? []).map((p) => ({ value: p.id, label: p.name }))}
              />
              <Divider label="Filter by reference" labelPosition="left" />
              <RefForm
                existing={f.ref ?? []}
                onAdd={(r) => {
                  set({ ref: withRef(f.ref, r) })
                  setMoreOpen(false)
                }}
              />
            </Stack>
          </Popover.Dropdown>
        </Popover>
      </Group>
      {chips.length > 0 && (
        <Group gap={6} wrap="wrap">
          {chips.map((c) => (
            <Pill key={c.key} withRemoveButton onRemove={() => set(c.remove)} removeButtonProps={{ 'aria-label': `Remove ${c.label}` }}>
              {c.label}
            </Pill>
          ))}
          <Button variant="subtle" size="compact-sm" color="gray" onClick={clearAll}>
            Clear
          </Button>
        </Group>
      )}

      {list.isError ? (
        <QueryError error={list.error} retry={() => void list.refetch()} />
      ) : !list.data ? (
        <Skeleton height={320} radius="md" />
      ) : items.length === 0 ? (
        <Empty
          icon={<ActivityIcon size={22} />}
          title={filtered ? 'No matching requests' : range !== 'all' ? 'No requests in this range' : 'No requests yet'}
          action={
            filtered ? (
              <Button variant="light" onClick={clearAll}>
                Show all requests
              </Button>
            ) : range !== 'all' ? (
              <Button variant="light" onClick={() => set({ range: 'all' })}>
                Show all time
              </Button>
            ) : (
              <Button component={Link} to="/clients/setup" variant="light">
                Connect a tool
              </Button>
            )
          }
        />
      ) : compact ? (
        <div role="region" aria-label="Requests" className={s.log}>
          {groups.map((g) => (
            <Fragment key={g.day}>
              <div className={s.dayHead}>{g.day}</div>
              <ul className={s.logList}>
                {g.rows.map((r) => (
                  <li key={r.id}>
                    <UnstyledButton className={s.logCard} data-tone={rowTone(r)} onClick={() => open(r)}>
                      <span className={s.logTop}>
                        <span className={s.logTitle}>{client[r.binding_id]?.name ?? 'Unknown client'}</span>
                        <span className={s.logTime}>{clock(r.created_at)}</span>
                      </span>
                      <span className={s.logModel}>
                        <Model r={r} />
                      </span>
                      <span className={s.logMeta}>
                        <Status r={r} />
                        {[
                          harnessName[r.harness] ?? (r.harness || 'Unknown tool'),
                          isKnown(r.duration_ms) && r.duration_ms > 0 ? ms(r.duration_ms) : null,
                          r.cost ? requestCost(r.cost.amount, r.cost.currency) : null,
                        ]
                          .filter(Boolean)
                          .join(' · ')}
                      </span>
                    </UnstyledButton>
                  </li>
                ))}
              </ul>
            </Fragment>
          ))}
        </div>
      ) : (
        <div role="region" aria-label="Requests" className={s.tableWrap}>
          <table className={s.table}>
            <thead>
              <tr>
                <th style={{ width: 96 }}>Time</th>
                <th>Client</th>
                <th>Model</th>
                <th style={{ width: 150 }}>Status</th>
                <th className={s.right} style={{ width: 96 }}>
                  Latency
                </th>
                <th className={s.right} style={{ width: 96 }}>
                  Cost
                </th>
              </tr>
            </thead>
            {groups.map((g) => (
              <tbody key={g.day}>
                <tr className={s.dayRow}>
                  <th colSpan={6} scope="colgroup">
                    {g.day}
                  </th>
                </tr>
                {g.rows.map((r) => (
                  <tr
                    key={r.id}
                    data-tone={rowTone(r)}
                    tabIndex={0}
                    onClick={() => open(r)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') open(r)
                    }}
                  >
                    <td>
                      <Tooltip label={`${fmtDateTime(r.created_at, { seconds: true })} · ${fmtRelative(r.created_at)}`}>
                        <span className={s.time}>{clock(r.created_at)}</span>
                      </Tooltip>
                    </td>
                    <td>
                      <span className={s.client}>{client[r.binding_id]?.name ?? <span className="muted">Unknown</span>}</span>
                      <span className={s.tool}>{harnessName[r.harness] ?? r.harness}</span>
                    </td>
                    <td>
                      <Model r={r} />
                    </td>
                    <td>
                      <Status r={r} />
                    </td>
                    <td className={s.right}>
                      <Latency r={r} />
                    </td>
                    <td className={s.right}>
                      <Cost r={r} />
                    </td>
                  </tr>
                ))}
              </tbody>
            ))}
          </table>
        </div>
      )}
      {list.data && items.length > 0 && (
        <Group justify="space-between" gap="xs">
          <Text size="xs" c="dimmed" className={s.live} aria-live="polite">
            <span className={s.liveDot} aria-hidden="true" />
            {total !== undefined && total > items.length
              ? `Showing ${items.length.toLocaleString('en-US')} of ${total.toLocaleString('en-US')} requests`
              : `${items.length.toLocaleString('en-US')} ${items.length === 1 ? 'request' : 'requests'}`}
            {list.dataUpdatedAt ? ` · live, updated ${fmtRelative(new Date(list.dataUpdatedAt).toISOString(), Date.now())}` : ''}
          </Text>
          {list.hasNextPage && (
            <Button variant="default" size="xs" loading={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()}>
              Load more
            </Button>
          )}
        </Group>
      )}
    </Stack>
  )
}
