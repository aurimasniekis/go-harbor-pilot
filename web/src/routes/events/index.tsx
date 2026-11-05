import { Fragment, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { Button, Group, SegmentedControl, Select, Skeleton, Stack, Text, UnstyledButton } from '@mantine/core'
import { AlertTriangle, ChevronDown, ChevronUp, OctagonAlert, Radio } from 'lucide-react'
import { QueryError } from '@/components/states'
import { apiGet } from '@/lib/api/client'
import { eventCatalogQuery } from '@/lib/api/queries'
import type { EventEnvelope, EventItem, EventKindInfo, Page } from '@/lib/api/types'
import { fmtDateTime, fmtRelative, getDisplayTimeZone } from '@/lib/format'
import { Empty, Fields } from '@/ui/kit'
import e from '@/features/events/events.module.css'
import s from '@/features/activity/activity.module.css'

export const Route = createFileRoute('/events/')({
  component: EventStream,
})

function envelopeOf(ev: EventItem): EventEnvelope | null {
  const env = ev.envelope as EventEnvelope | null | undefined
  return env && typeof env === 'object' ? env : null
}

function clock(iso: string): string {
  return new Intl.DateTimeFormat('en-GB', { timeZone: getDisplayTimeZone(), hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }).format(new Date(iso))
}

/** A readable line: the kind's description plus its most telling data. */
function sentence(env: EventEnvelope | null, kind: EventKindInfo | undefined, fallback: string): string {
  const d = (env?.data ?? {}) as Record<string, unknown>
  const name = env?.name ?? fallback
  if (name.startsWith('config.')) {
    const [, k, op] = name.split('.')
    const verb = op === 'created' ? 'created' : op === 'deleted' ? 'deleted' : 'changed'
    return `${(k ?? '').replaceAll('_', ' ')} ${String(d.name ?? '')} ${verb}`.replace(/\s+/g, ' ').trim()
  }
  const detail = ['monitor', 'budget', 'pool', 'label', 'plan', 'path', 'reason', 'name'].map((k) => d[k]).filter((v) => typeof v === 'string' && v) as string[]
  const base = kind?.description.replace(/\.$/, '') ?? name.replaceAll('.', ' ').replaceAll('_', ' ')
  return detail.length ? `${base}: ${detail.slice(0, 2).join(' · ')}` : base
}

type View = 'problems' | 'all' | 'config'

export function EventStream() {
  const catalog = useQuery(eventCatalogQuery)
  const kinds = Object.fromEntries((catalog.data?.items ?? []).map((k) => [k.name, k]))
  const [view, setView] = useState<View>('problems')
  const [type, setType] = useState<string | null>(null)
  const [open, setOpen] = useState<string | null>(null)
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(new Set())
  const log = useInfiniteQuery({
    queryKey: ['events', 'stream', type],
    initialPageParam: '',
    queryFn: ({ pageParam, signal }) => apiGet<Page<EventItem>>('/events', { limit: 100, type: type ?? undefined, ...(pageParam ? { cursor: pageParam } : {}) }, signal),
    getNextPageParam: (last) => last.next_cursor || undefined,
    refetchInterval: 15_000,
  })
  const all = (log.data?.pages ?? []).flatMap((p) => p.items ?? [])
  const items = all.filter((ev) => {
    const name = envelopeOf(ev)?.name ?? ''
    if (view === 'problems') return ev.severity !== 'info'
    if (view === 'config') return name.startsWith('config.')
    return true
  })

  // Consecutive configuration changes collapse into one row.
  type Row = { kind: 'event'; ev: EventItem } | { kind: 'group'; key: string; evs: EventItem[] }
  const rows: Row[] = []
  for (const ev of items) {
    const isConfig = (envelopeOf(ev)?.name ?? '').startsWith('config.') && view !== 'config'
    const last = rows[rows.length - 1]
    if (isConfig && last?.kind === 'group') last.evs.push(ev)
    else if (isConfig && last?.kind === 'event' && (envelopeOf(last.ev)?.name ?? '').startsWith('config.')) {
      rows[rows.length - 1] = { kind: 'group', key: last.ev.id, evs: [last.ev, ev] }
    } else rows.push({ kind: 'event', ev })
  }

  const eventRow = (ev: EventItem) => {
    const env = envelopeOf(ev)
    const expanded = open === ev.id
    const tone = ev.severity === 'critical' ? 'error' : ev.severity === 'warning' ? 'warning' : undefined
    const kind = kinds[env?.name ?? '']
    return (
      <li key={ev.id} data-severity={ev.severity}>
        <div className={s.logCard} data-tone={tone}>
          <span className={s.logTop}>
            <Group gap={6} wrap="nowrap" miw={0}>
              {ev.severity === 'critical' ? (
                <OctagonAlert size={14} color="var(--error)" aria-label="Critical" />
              ) : ev.severity === 'warning' ? (
                <AlertTriangle size={14} color="var(--warning)" aria-label="Warning" />
              ) : null}
              <Text size="sm" fw={500}>
                {sentence(env, kind, ev.type)}
              </Text>
            </Group>
            <Group gap={4} wrap="nowrap">
              <span className={s.logTime} title={`${fmtDateTime(ev.occurred_at, { seconds: true })} · ${fmtRelative(ev.occurred_at)}`}>
                {clock(ev.occurred_at)}
              </span>
              <UnstyledButton
                aria-expanded={expanded}
                aria-label={`${expanded ? 'Hide' : 'Show'} envelope for ${env?.name ?? ev.type}`}
                onClick={() => setOpen(expanded ? null : ev.id)}
                style={{ display: 'inline-flex', color: 'var(--text-muted)' }}
              >
                {expanded ? <ChevronUp size={16} /> : <ChevronDown size={16} />}
              </UnstyledButton>
            </Group>
          </span>
          {expanded && (
            <Stack gap="xs" mt="xs">
              <Fields
                cols={2}
                items={[
                  ['Event', <code>{env?.name ?? ev.type}</code>],
                  ['Severity', ev.severity],
                  ...Object.entries(env?.subjects ?? {}).map(([k, v]) => [k.replaceAll('_', ' '), <code>{v}</code>] as [string, React.ReactNode]),
                ]}
              />
              <pre className={e.envelope} aria-label="Event envelope">
                {JSON.stringify(ev.envelope, null, 2)}
              </pre>
            </Stack>
          )}
        </div>
      </li>
    )
  }

  return (
    <Stack gap="md">
      <Group gap="xs" wrap="wrap" role="search" aria-label="Filter events">
        <SegmentedControl
          aria-label="Show"
          size="sm"
          value={view}
          onChange={(v) => setView(v as View)}
          data={[
            { value: 'problems', label: 'Problems' },
            { value: 'all', label: 'All' },
            { value: 'config', label: 'Config changes' },
          ]}
        />
        <Select
          aria-label="Event"
          placeholder="Any event"
          clearable
          searchable
          w={240}
          value={type}
          onChange={setType}
          data={(catalog.data?.items ?? []).map((k) => ({ value: k.type, label: k.name }))}
        />
        <Text size="xs" c="dimmed">
          Newest first; refreshes every 15 seconds.
        </Text>
      </Group>
      {log.isError ? (
        <QueryError error={log.error} retry={() => void log.refetch()} />
      ) : !log.data ? (
        <Skeleton height={240} radius="md" />
      ) : rows.length === 0 ? (
        <Empty icon={<Radio size={22} />} title={view === 'problems' ? 'No problems recorded' : 'No events yet'}>
          Events are recorded for changes such as an account switch, a used-up quota or a paused protocol path.
        </Empty>
      ) : (
        <div role="region" aria-label="Event log">
          <ul className={s.logList}>
            {rows.map((r) =>
              r.kind === 'event' ? (
                eventRow(r.ev)
              ) : (
                <Fragment key={r.key}>
                  <li>
                    <UnstyledButton
                      className={s.logCard}
                      aria-expanded={expandedGroups.has(r.key)}
                      onClick={() => setExpandedGroups((g) => {
                        const n = new Set(g)
                        if (n.has(r.key)) n.delete(r.key)
                        else n.add(r.key)
                        return n
                      })}
                    >
                      <span className={s.logTop}>
                        <Text size="sm" c="dimmed">
                          {r.evs.length} configuration changes · {expandedGroups.has(r.key) ? 'collapse' : 'expand'}
                        </Text>
                        <span className={s.logTime}>{clock(r.evs[0]!.occurred_at)}</span>
                      </span>
                    </UnstyledButton>
                  </li>
                  {expandedGroups.has(r.key) && r.evs.map(eventRow)}
                </Fragment>
              ),
            )}
          </ul>
        </div>
      )}
      {log.hasNextPage && (
        <Group justify="flex-end">
          <Button variant="default" size="xs" loading={log.isFetchingNextPage} onClick={() => void log.fetchNextPage()}>
            Load more
          </Button>
        </Group>
      )}
      <Text size="xs" c="dimmed">
        Each event kind and its fields are listed in the <a href="/events/catalog" style={{ textDecoration: 'underline' }}>event reference</a>.
      </Text>
    </Stack>
  )
}
