import { Fragment, useMemo, useState } from 'react'
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Button, Drawer, Group, SegmentedControl, Select, Skeleton, Stack, Text, TextInput, Tooltip, UnstyledButton } from '@mantine/core'
import { CircleCheck, CircleX, Search, ShieldAlert, ShieldCheck, ShieldX } from 'lucide-react'
import { Banner } from '@/components/ui'
import { MutationError } from '@/components/form'
import { QueryError } from '@/components/states'
import { apiGet, apiSend } from '@/lib/api/client'
import { principalsQuery, qk, resourceListQuery, securityAlertsQuery, usersQuery } from '@/lib/api/queries'
import type { AuditEntry, Page, ResourceMeta } from '@/lib/api/types'
import { fmtDateTime, fmtRelative, getDisplayTimeZone, serverNow } from '@/lib/format'
import { useCan } from '@/lib/session'
import { CopyText, Empty, Fields, useCompact } from '@/ui/kit'
import { actionGroups, actorName, detailPairs, kindNoun, type Names, sentence } from './audit'
import s from '@/features/activity/activity.module.css'

const PAGE = 100

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

/** Outcome as a small icon: success is quiet, denied and errors stand out. */
function Outcome({ outcome }: { outcome: string }) {
  if (outcome === 'success') {
    return (
      <Tooltip label="Succeeded">
        <span aria-label="Succeeded" role="img" style={{ color: 'var(--success)', display: 'inline-flex' }}>
          <CircleCheck size={14} />
        </span>
      </Tooltip>
    )
  }
  const denied = outcome === 'denied'
  return (
    <Group gap={4} wrap="nowrap" c={denied ? 'var(--warning)' : 'var(--error-text)'} style={{ whiteSpace: 'nowrap' }}>
      {denied ? <ShieldX size={14} aria-hidden="true" /> : <CircleX size={14} aria-hidden="true" />}
      <Text size="xs" c="inherit" fw={500}>
        {denied ? 'Denied' : 'Failed'}
      </Text>
    </Group>
  )
}

const tone = (e: AuditEntry) => (e.outcome === 'denied' ? 'warning' : e.outcome === 'success' ? undefined : 'error')

const ranges = [
  { value: '1', label: '24h' },
  { value: '7', label: '7d' },
  { value: '30', label: '30d' },
  { value: 'all', label: 'All' },
]

/** Names of users, automation accounts and configuration objects by id. */
function useNames(): Names {
  const users = useQuery(usersQuery)
  const sps = useQuery(principalsQuery)
  const bindings = useQuery(resourceListQuery<ResourceMeta>('client-bindings'))
  const plans = useQuery(resourceListQuery<ResourceMeta>('routing-plans'))
  const accounts = useQuery(resourceListQuery<ResourceMeta>('provider-accounts'))
  const providers = useQuery(resourceListQuery<ResourceMeta>('providers'))
  return useMemo(() => {
    const m = new Map<string, string>()
    for (const u of users.data?.items ?? []) m.set(u.id, u.display_name || u.email)
    for (const p of sps.data?.items ?? []) m.set(p.id, p.name)
    for (const list of [bindings, plans, accounts, providers]) for (const r of list.data?.items ?? []) m.set(r.id, r.name)
    return { name: (id) => (id ? m.get(id) : undefined) }
  }, [users.data, sps.data, bindings, plans, accounts, providers])
}

export function AuditLog() {
  const qc = useQueryClient()
  const compact = useCompact()
  const canAck = useCan('system.manage')
  const alerts = useQuery({ ...securityAlertsQuery, enabled: canAck })
  const users = useQuery(usersQuery)
  const sps = useQuery(principalsQuery)
  const names = useNames()
  const [group, setGroup] = useState<string | null>(null)
  const [actor, setActor] = useState<string | null>(null)
  const [outcome, setOutcome] = useState('all')
  const [range, setRange] = useState('7')
  const [text, setText] = useState('')
  const [open, setOpen] = useState<AuditEntry | null>(null)
  const filter = { action: group ?? undefined, actor_id: actor ?? undefined, outcome: outcome === 'all' ? undefined : outcome }
  const log = useInfiniteQuery({
    queryKey: [...qk.audit, 'log', filter],
    initialPageParam: '',
    queryFn: ({ pageParam, signal }) => apiGet<Page<AuditEntry>>('/audit', { limit: PAGE, ...filter, ...(pageParam ? { cursor: pageParam } : {}) }, signal),
    getNextPageParam: (last) => last.next_cursor || undefined,
  })
  const ack = useMutation({
    mutationFn: (id: string) => apiSend<void>('POST', `/security-alerts/${id}/acknowledge`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.alerts })
      void qc.invalidateQueries({ queryKey: qk.session })
    },
  })

  // The time range and the text search apply to the loaded entries (the
  // audit API filters by action, actor and outcome).
  const since = range === 'all' ? 0 : serverNow() - Number(range) * 86_400_000
  const q = text.trim().toLowerCase()
  const all = (log.data?.pages ?? []).flatMap((p) => p.items ?? [])
  const items = all
    .filter((e) => new Date(e.at).getTime() >= since)
    .map((e) => ({ e, text: sentence(e, names) }))
    .filter((x) => !q || x.text.toLowerCase().includes(q) || x.e.action.includes(q))
  const reachedRangeEnd = all.length > 0 && new Date(all[all.length - 1]!.at).getTime() < since

  const groups: { day: string; rows: typeof items }[] = []
  for (const it of items) {
    const day = dayLabel(it.e.at)
    if (groups[groups.length - 1]?.day !== day) groups.push({ day, rows: [] })
    groups[groups.length - 1]!.rows.push(it)
  }
  const actors = [
    { value: 'system', label: 'System' },
    ...(users.data?.items ?? []).map((u) => ({ value: u.id, label: u.display_name || u.email })),
    ...(sps.data?.items ?? []).map((p) => ({ value: p.id, label: `${p.name} (automation)` })),
  ]
  const filtered = !!(group || actor || outcome !== 'all' || q)

  return (
    <Stack gap="md">
      {(alerts.data?.items ?? []).map((a) => (
        <Banner key={a.id} tone="error" icon={ShieldAlert} role="alert">
          <Group justify="space-between" gap="sm" wrap="wrap">
            <span>
              <strong>Security alert</strong> · {fmtDateTime(a.created_at)}: {a.message}
            </span>
            <Button size="compact-sm" variant="default" leftSection={<ShieldCheck size={14} />} onClick={() => ack.mutate(a.id)}>
              Acknowledge
            </Button>
          </Group>
        </Banner>
      ))}
      <MutationError error={ack.error} />
      <Group gap="xs" wrap="wrap" role="search" aria-label="Filter the audit log" className={s.toolbar}>
        <div className={s.search}>
          <TextInput aria-label="Search the audit log" placeholder="Search" leftSection={<Search size={14} />} value={text} onChange={(e) => setText(e.currentTarget.value)} />
        </div>
        <Select aria-label="Who" placeholder="Anyone" clearable searchable data={actors} value={actor} onChange={setActor} w={180} />
        <Select aria-label="What" placeholder="Any action" clearable data={actionGroups} value={group} onChange={setGroup} w={200} />
        <SegmentedControl aria-label="Outcome" size="sm" value={outcome} onChange={setOutcome} data={[{ value: 'all', label: 'All' }, { value: 'denied', label: 'Denied' }, { value: 'error', label: 'Failed' }]} />
        <SegmentedControl aria-label="Time range" size="sm" value={range} onChange={setRange} data={ranges} />
      </Group>

      {log.isError ? (
        <QueryError error={log.error} retry={() => void log.refetch()} />
      ) : !log.data ? (
        <Skeleton height={320} radius="md" />
      ) : items.length === 0 ? (
        <Empty title={filtered ? 'Nothing matches' : 'Nothing recorded in this range'}>{filtered ? 'Try removing a filter.' : undefined}</Empty>
      ) : compact ? (
        <div role="region" aria-label="Audit entries" className={s.log}>
          {groups.map((g) => (
            <Fragment key={g.day}>
              <div className={s.dayHead}>{g.day}</div>
              <ul className={s.logList}>
                {g.rows.map(({ e, text }) => (
                  <li key={e.id}>
                    <UnstyledButton className={s.logCard} data-tone={tone(e)} onClick={() => setOpen(e)}>
                      <span className={s.logTop}>
                        <span style={{ fontWeight: 500, minWidth: 0 }}>{text}</span>
                        <span className={s.logTime}>{clock(e.at)}</span>
                      </span>
                      {e.outcome !== 'success' && (
                        <span className={s.logMeta}>
                          <Outcome outcome={e.outcome} />
                        </span>
                      )}
                    </UnstyledButton>
                  </li>
                ))}
              </ul>
            </Fragment>
          ))}
        </div>
      ) : (
        <div role="region" aria-label="Audit entries" className={s.tableWrap}>
          <table className={s.table}>
            <thead>
              <tr>
                <th style={{ width: 96 }}>Time</th>
                <th>What happened</th>
                <th style={{ width: 96 }}>Outcome</th>
              </tr>
            </thead>
            {groups.map((g) => (
              <tbody key={g.day}>
                <tr className={s.dayRow}>
                  <th colSpan={3} scope="colgroup">
                    {g.day}
                  </th>
                </tr>
                {g.rows.map(({ e, text }) => (
                  <tr
                    key={e.id}
                    data-tone={tone(e)}
                    tabIndex={0}
                    onClick={() => setOpen(e)}
                    onKeyDown={(ev) => {
                      if (ev.key === 'Enter') setOpen(e)
                    }}
                  >
                    <td>
                      <Tooltip label={`${fmtDateTime(e.at, { seconds: true })} · ${fmtRelative(e.at)}`}>
                        <span className={s.time}>{clock(e.at)}</span>
                      </Tooltip>
                    </td>
                    <td>{text}</td>
                    <td>
                      <Outcome outcome={e.outcome} />
                    </td>
                  </tr>
                ))}
              </tbody>
            ))}
          </table>
        </div>
      )}
      {log.data && (
        <Group justify="space-between" gap="xs">
          <Text size="xs" c="dimmed">
            Showing {items.length} {items.length === 1 ? 'entry' : 'entries'}
            {!log.hasNextPage || reachedRangeEnd ? ' · that’s all in this range' : ''}
          </Text>
          {log.hasNextPage && !reachedRangeEnd && (
            <Button variant="default" size="xs" loading={log.isFetchingNextPage} onClick={() => void log.fetchNextPage()}>
              Load more
            </Button>
          )}
        </Group>
      )}
      <EntryDrawer e={open} names={names} onClose={() => setOpen(null)} compact={compact} />
    </Stack>
  )
}

function EntryDrawer({ e, names, onClose, compact }: { e: AuditEntry | null; names: Names; onClose: () => void; compact: boolean }) {
  const pairs = e ? detailPairs(e.details) : []
  return (
    <Drawer
      opened={!!e}
      onClose={onClose}
      position={compact ? 'bottom' : 'right'}
      size={compact ? '100%' : 520}
      title={e && <Text fw={600}>{sentence(e, names)}</Text>}
      closeButtonProps={{ 'aria-label': 'Close' }}
    >
      {e && (
        <Stack gap="lg">
          <Fields
            cols={1}
            items={[
              ['When', `${fmtDateTime(e.at, { seconds: true })} (${fmtRelative(e.at)})`],
              ['Who', actorName(e, names)],
              ['Outcome', <Outcome outcome={e.outcome} />],
              ['Action', <code>{e.action}</code>],
              ...(e.target_kind || e.target_id
                ? ([['Target', <Group gap={6} wrap="nowrap">{kindNoun[e.target_kind ?? ''] ?? e.target_kind}{e.target_id && <CopyText value={e.target_id} maw={220} />}</Group>]] as [string, React.ReactNode][])
                : []),
              ...(e.actor_id && e.actor_id !== 'system' ? ([['Actor id', <CopyText value={e.actor_id} maw={220} />]] as [string, React.ReactNode][]) : []),
              ...(e.request_id ? ([['Request id', <CopyText value={e.request_id} maw={220} />]] as [string, React.ReactNode][]) : []),
              ...(e.ip ? ([['IP address', e.ip]] as [string, React.ReactNode][]) : []),
            ]}
          />
          {pairs.length > 0 && (
            <Stack gap="xs">
              <Text size="sm" fw={600}>
                Details
              </Text>
              <Fields cols={1} items={pairs.map(([k, v]) => [k, <Text size="sm" style={{ overflowWrap: 'anywhere' }}>{v}</Text>])} />
            </Stack>
          )}
        </Stack>
      )}
    </Drawer>
  )
}
