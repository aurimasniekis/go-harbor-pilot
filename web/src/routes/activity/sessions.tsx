import { useMemo, useState } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Button, Group, Stack, Text, Tooltip } from '@mantine/core'
import { MessagesSquare } from 'lucide-react'
import { DataTable, Empty, InfoTip } from '@/ui/kit'
import { harnessName, money, short } from '@/ui/fmt'
import { QueryError, TableSkeleton } from '@/components/states'
import { resourceListQuery, sessionsQuery, statsQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { BindingRes, ObservedSession, StatsGroup } from '@/lib/api/types'
import { fmtDateTime, fmtRelative } from '@/lib/format'
import { presetRange } from '@/features/usage/range'
import { findCost } from '@/features/stats/stats'

export const Route = createFileRoute('/activity/sessions')({
  component: Sessions,
})

/** How the session was identified, in plain words. */
const sourceName: Record<string, string> = {
  harness_header: 'Reported by the tool',
  token: 'From the access token',
  bound_ref: 'From the access token',
}

/** Short, readable session handle: the end of the id. */
export function sessionLabel(id: string): string {
  return `…${id.slice(-6)}`
}

/** "12 min", "1 h 5 min"; under a minute "<1 min". */
export function span(from: string, to: string): string {
  const m = Math.round((new Date(to).getTime() - new Date(from).getTime()) / 60_000)
  if (m < 1) return '<1 min'
  if (m < 60) return `${m} min`
  const h = Math.floor(m / 60)
  return `${h} h ${m % 60} min`
}

function Sessions() {
  const navigate = useNavigate()
  const [cursors, setCursors] = useState<string[]>([])
  const list = useQuery(sessionsQuery({ cursor: cursors[cursors.length - 1] }))
  const bindings = useQuery(resourceListQuery<BindingRes>('client-bindings'))
  // Cost and tokens per session over the last 30 days.
  const range = useMemo(() => presetRange(30), [])
  const stats = useQuery(statsQuery(range, 'session', 'day'))
  const statBy = useMemo(() => Object.fromEntries((stats.data?.groups ?? []).map((g) => [g.key, g])) as Record<string, StatsGroup>, [stats.data])
  const bindingById = byId(bindings.data?.items)
  const items = useMemo(() => [...(list.data?.items ?? [])].sort((a, b) => b.last_seen.localeCompare(a.last_seen)), [list.data])
  const clientName = (x: ObservedSession) => bindingById[x.binding_id]?.spec.display_name || bindingById[x.binding_id]?.name || 'Unknown client'
  return (
    <Stack gap="md">
      <Group gap={6}>
        <Text size="sm" c="dimmed">
          Conversations, newest activity first.
        </Text>
        <InfoTip>
          Requests are grouped by a session the tool reports (when its client trusts it) or a session bound to the access token. They are never grouped
          just because they share a token.
        </InfoTip>
      </Group>
      {list.isError ? (
        <QueryError error={list.error} retry={() => void list.refetch()} />
      ) : !list.data ? (
        <TableSkeleton />
      ) : (
        <DataTable
          label="Sessions"
          rows={items}
          rowKey={(x) => x.id}
          onRow={(x) => void navigate({ to: '/activity', search: { session_id: x.id, range: 'all' } })}
          empty={
            <Empty icon={<MessagesSquare size={22} />} title="No sessions yet">
              Sessions appear when a tool sends a session header and its client trusts it, or a token carries a session.
            </Empty>
          }
          columns={[
            {
              id: 'session',
              header: 'Session',
              mobile: 'title',
              cell: (x) => (
                <Stack gap={0}>
                  <Text size="sm" fw={500}>
                    {clientName(x)}
                  </Text>
                  <Text size="xs" c="dimmed" className="mono">
                    {harnessName[x.harness] ?? x.harness} · {sessionLabel(x.id)}
                  </Text>
                </Stack>
              ),
            },
            { id: 'requests', header: 'Requests', align: 'right', mobile: 'aside', cell: (x) => short(x.requests) },
            {
              id: 'tokens',
              header: 'Tokens in / out',
              align: 'right',
              mobileLabel: 'Tokens',
              cell: (x) => {
                const g = statBy[x.id]
                return g ? `${short(g.input_tokens)} / ${short(g.output_tokens)}` : <span className="muted">—</span>
              },
            },
            {
              id: 'cost',
              header: 'Estimated cost',
              align: 'right',
              mobileLabel: 'Cost',
              cell: (x) => {
                const c = statBy[x.id]?.costs?.find((k) => k.kind === 'estimated_api_charge')
                const v = c ? findCost(statBy[x.id]!.costs, c.kind, c.currency) : undefined
                return v ? money(v.amount, v.currency) : <span className="muted">—</span>
              },
            },
            { id: 'duration', header: 'Duration', align: 'right', mobileLabel: 'Duration', cell: (x) => span(x.first_seen, x.last_seen) },
            {
              id: 'last',
              header: 'Last request',
              align: 'right',
              mobileLabel: 'Last',
              cell: (x) => (
                <Tooltip label={`Started ${fmtDateTime(x.first_seen, { seconds: true })} · last ${fmtDateTime(x.last_seen, { seconds: true })}`}>
                  <span>{fmtRelative(x.last_seen)}</span>
                </Tooltip>
              ),
            },
            {
              id: 'source',
              header: 'Grouped by',
              mobile: 'hidden',
              cell: (x) => (
                <Text size="xs" c={x.confidence === 'low' ? 'yellow' : 'dimmed'} span>
                  {x.confidence === 'low' ? 'Uncertain' : (sourceName[x.source] ?? x.source.replaceAll('_', ' '))}
                </Text>
              ),
            },
          ]}
        />
      )}
      {list.data && (cursors.length > 0 || list.data.next_cursor) && (
        <Group justify="flex-end" gap="xs">
          <Button variant="default" size="xs" disabled={cursors.length === 0} onClick={() => setCursors(cursors.slice(0, -1))}>
            Newer
          </Button>
          <Button
            variant="default"
            size="xs"
            disabled={!list.data.next_cursor}
            onClick={() => list.data?.next_cursor && setCursors([...cursors, list.data.next_cursor])}
          >
            Older
          </Button>
        </Group>
      )}
    </Stack>
  )
}
