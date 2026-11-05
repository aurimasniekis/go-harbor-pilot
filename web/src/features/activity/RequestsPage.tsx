import { Fragment, useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useSearch } from '@tanstack/react-router'
import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { Badge, Button, Group, Pill, SegmentedControl, Popover, Select, Skeleton, Stack, Text, TextInput, Tooltip, UnstyledButton } from '@mantine/core'
import { Activity as ActivityIcon, Filter, Search } from 'lucide-react'
import { z } from 'zod'
import { QueryError } from '@/components/states'
import { apiGet } from '@/lib/api/client'
import { resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { BindingRes, Page, ProjectRes, RequestSummary } from '@/lib/api/types'
import { fmtDateTime, fmtRelative, getDisplayTimeZone, isKnown, serverNow } from '@/lib/format'
import { Empty, Stat, StatGrid, StatSkeleton, Unknown, useCompact } from '@/ui/kit'
import { costKindHelp, costKindName, harnessName, human, money, ms, short } from '@/ui/fmt'
import { delta, series, useDayStats } from './useDayStats'
import s from './activity.module.css'

export const requestSearch = z.object({
  status: z.string().optional().catch(undefined),
  harness: z.string().optional().catch(undefined),
  binding_id: z.string().optional().catch(undefined),
  project_id: z.string().optional().catch(undefined),
  session_id: z.string().optional().catch(undefined),
  model: z.string().optional().catch(undefined),
  fallbacks: z.boolean().optional().catch(undefined),
  range: z.enum(['1', '7', '30', 'all']).optional().catch(undefined),
})

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
    <StatGrid cols={4} label="Last 24 hours">
      <Stat label="Requests · 24h" value={short(totals.requests)} hint={delta(totals.requests, previous?.requests) ?? 'Last 24 hours'} spark={series(hours, (g) => g.requests)} />
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

/** Status only when something is off: fallback, error or blocked. */
function Status({ r }: { r: RequestSummary }) {
  if (r.status === 'error') return <Badge color="red">{r.error_kind ? human(r.error_kind) : 'Failed'}</Badge>
  if (r.status === 'blocked') return <Badge color="yellow">Blocked</Badge>
  if (r.status === 'in_progress') return <Badge color="teal">Running</Badge>
  if (r.status === 'canceled') return <Badge color="gray">Canceled</Badge>
  if (r.attempts > 1)
    return (
      <Tooltip label="The first account failed before any output, so the next one in the plan served it">
        <Badge color="violet" variant="outline">
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

function Cost({ r }: { r: RequestSummary }) {
  if (!r.cost) return <Unknown reason="No price configured, or usage not reported" />
  return (
    <Tooltip label={`${costKindName[r.cost.kind] ?? r.cost.kind}. ${costKindHelp[r.cost.kind] ?? ''}`}>
      <span>{money(r.cost.amount, r.cost.currency)}</span>
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
  const client = byId(bindings.data?.items)
  const project = byId(projects.data?.items)
  const [text, setText] = useState('')
  const [moreOpen, setMoreOpen] = useState(false)
  // Range start rounded to the minute so the query key stays stable.
  const days = f.range === 'all' ? 0 : Number(f.range ?? '7')
  const from = days ? new Date(Math.floor((serverNow() - days * 86_400_000) / 60_000) * 60_000).toISOString() : undefined
  const filter = {
    status: f.status,
    fallbacks: f.fallbacks || undefined,
    harness: f.harness,
    binding_id: f.binding_id,
    project_id: f.project_id,
    session_id: f.session_id,
    q: f.model,
    from,
  }
  const list = useInfiniteQuery({
    queryKey: ['activity', 'requests', 'log', filter],
    initialPageParam: '',
    queryFn: ({ pageParam, signal }) => apiGet<Page<RequestSummary> & { total?: number }>('/activity/requests', { limit: PAGE, ...filter, ...(pageParam ? { cursor: pageParam } : {}) }, signal),
    getNextPageParam: (last) => last.next_cursor || undefined,
    refetchInterval: 15_000,
  })
  const [, tick] = useState(0)
  useEffect(() => {
    const t = setInterval(() => tick((x) => x + 1), 5_000)
    return () => clearInterval(t)
  }, [])

  const set = (patch: Partial<RequestSearch>) => void navigate({ to: '/activity', search: { ...f, ...patch } })
  const open = (r: RequestSummary) => void navigate({ to: '/activity/$requestId', params: { requestId: r.id } })
  const submit = () => {
    const q = text.trim()
    if (!q) return
    if (/^req_[a-z0-9]+$/i.test(q)) {
      void navigate({ to: '/activity/$requestId', params: { requestId: q } })
      return
    }
    const match = (bindings.data?.items ?? []).find((b) => b.name.toLowerCase() === q.toLowerCase())
    setText('')
    set(match ? { binding_id: match.id } : { model: q })
  }

  const items = useMemo(() => (list.data?.pages ?? []).flatMap((p) => p.items ?? []), [list.data])
  const total = list.data?.pages[0]?.total
  const chips: { key: keyof RequestSearch; label: string }[] = []
  if (f.binding_id) chips.push({ key: 'binding_id', label: `Client: ${client[f.binding_id]?.name ?? f.binding_id}` })
  if (f.harness) chips.push({ key: 'harness', label: `Tool: ${harnessName[f.harness] ?? f.harness}` })
  if (f.project_id) chips.push({ key: 'project_id', label: `Project: ${project[f.project_id]?.name ?? f.project_id}` })
  if (f.session_id) chips.push({ key: 'session_id', label: 'This session' })
  if (f.model) chips.push({ key: 'model', label: `Model: ${f.model}` })
  const moreCount = [f.binding_id, f.harness, f.project_id].filter(Boolean).length
  const statusView = f.fallbacks ? 'fallbacks' : f.status === 'error' || f.status === 'blocked' ? f.status : 'all'

  // Rows grouped by day, newest first.
  const groups: { day: string; rows: RequestSummary[] }[] = []
  for (const r of items) {
    const day = dayLabel(r.created_at)
    if (groups[groups.length - 1]?.day !== day) groups.push({ day, rows: [] })
    groups[groups.length - 1]!.rows.push(r)
  }

  return (
    <Stack gap="md">
      <Group gap="xs" wrap="wrap" role="search" aria-label="Filter requests" className={s.toolbar}>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            submit()
          }}
          className={s.search}
        >
          <TextInput
            aria-label="Search requests"
            placeholder="Request ID, client or model"
            leftSection={<Search size={14} />}
            value={text}
            onChange={(e) => setText(e.currentTarget.value)}
          />
        </form>
        <SegmentedControl
          aria-label="Show"
          size="sm"
          value={statusView}
          onChange={(v) =>
            set(v === 'fallbacks' ? { status: undefined, fallbacks: true } : { status: v === 'all' ? undefined : v, fallbacks: undefined })
          }
          data={[
            { value: 'all', label: 'All' },
            { value: 'error', label: 'Errors' },
            { value: 'blocked', label: 'Blocked' },
            { value: 'fallbacks', label: 'Fallbacks' },
          ]}
        />
        <SegmentedControl
          aria-label="Time range"
          size="sm"
          value={f.range ?? '7'}
          onChange={(v) => set({ range: v === '7' ? undefined : (v as RequestSearch['range']) })}
          data={[
            { value: '1', label: '24h' },
            { value: '7', label: '7d' },
            { value: '30', label: '30d' },
            { value: 'all', label: 'All' },
          ]}
        />
        <Popover opened={moreOpen} onChange={setMoreOpen} position="bottom-end" width={260} shadow="md" withinPortal>
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
            </Stack>
          </Popover.Dropdown>
        </Popover>
        <Text size="xs" c="dimmed" className={s.live} aria-live="polite">
          <span className={s.liveDot} aria-hidden="true" />
          Live{list.dataUpdatedAt ? ` · updated ${fmtRelative(new Date(list.dataUpdatedAt).toISOString(), Date.now())}` : ''}
        </Text>
      </Group>
      {chips.length > 0 && (
        <Group gap={6} wrap="wrap">
          {chips.map((c) => (
            <Pill key={c.key} withRemoveButton onRemove={() => set({ [c.key]: undefined })} removeButtonProps={{ 'aria-label': `Remove ${c.label}` }}>
              {c.label}
            </Pill>
          ))}
          <Button variant="subtle" size="compact-sm" color="gray" onClick={() => set({ status: undefined, fallbacks: undefined, harness: undefined, binding_id: undefined, project_id: undefined, session_id: undefined, model: undefined })}>
            Clear
          </Button>
        </Group>
      )}

      {list.isError ? (
        <QueryError error={list.error} retry={() => void list.refetch()} />
      ) : !list.data ? (
        <Skeleton height={320} radius="md" />
      ) : items.length === 0 ? (
        <Empty icon={<ActivityIcon size={22} />} title={chips.length || f.status || f.fallbacks ? 'No matching requests' : 'No requests yet'} action={!chips.length && !f.status && !f.fallbacks ? <Link to="/clients/setup">Connect a tool</Link> : undefined}>
          {chips.length || f.status || f.fallbacks ? 'Try removing a filter.' : undefined}
        </Empty>
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
                        <span>{harnessName[r.harness] ?? (r.harness || 'Unknown tool')}</span>
                        <span>{ms(r.duration_ms)}</span>
                        <span>{r.cost ? money(r.cost.amount, r.cost.currency) : '—'}</span>
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
                    <td className={s.right}>{isKnown(r.duration_ms) ? ms(r.duration_ms) : <Unknown reason="Not finished" />}</td>
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
          <Text size="xs" c="dimmed">
            Showing {items.length.toLocaleString('en-US')}
            {total !== undefined && total > items.length ? ` of ${total.toLocaleString('en-US')}` : ''} {(total ?? items.length) === 1 ? 'request' : 'requests'}
            {!list.hasNextPage ? ' · that’s all' : ''}
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
