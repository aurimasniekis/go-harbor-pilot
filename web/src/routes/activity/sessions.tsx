import { useState } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { MessagesSquare } from 'lucide-react'
import { Badge, Button } from '@/components/ui'
import { Group, Stack, Text } from '@mantine/core'
import { DataTable, Empty } from '@/ui/kit'
import { harnessName } from '@/ui/fmt'
import { QueryError, TableSkeleton } from '@/components/states'
import { resourceListQuery, sessionsQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { BindingRes } from '@/lib/api/types'
import { fmtDateTime, fmtRelative } from '@/lib/format'

export const Route = createFileRoute('/activity/sessions')({
  component: Sessions,
})

function Sessions() {
  const navigate = useNavigate()
  const [cursors, setCursors] = useState<string[]>([])
  const list = useQuery(sessionsQuery({ cursor: cursors[cursors.length - 1] }))
  const bindings = useQuery(resourceListQuery<BindingRes>('client-bindings'))
  const bindingById = byId(bindings.data?.items)
  const items = list.data?.items ?? []
  return (
    <Stack gap="md">
      <Text size="sm" c="dimmed">
        Requests grouped into conversations by a session the tool reports (when the client trusts it) or a session bound to the token. Requests are never
        grouped just because they share a token.
      </Text>
      {list.isError ? (
        <QueryError error={list.error} retry={() => void list.refetch()} />
      ) : !list.data ? (
        <TableSkeleton />
      ) : (
        <DataTable
          label="Sessions"
          rows={items}
          rowKey={(x) => x.id}
          onRow={(x) => void navigate({ to: '/activity', search: { session_id: x.id } })}
          empty={
            <Empty icon={<MessagesSquare size={22} />} title="No sessions yet">
              Sessions appear when a tool sends a session header and its client trusts it, or a token carries a session.
            </Empty>
          }
          columns={[
            { id: 'client', header: 'Client', mobile: 'title', cell: (x) => bindingById[x.binding_id]?.spec.display_name || bindingById[x.binding_id]?.name || 'Unknown client' },
            { id: 'tool', header: 'Tool', mobile: 'meta', cell: (x) => harnessName[x.harness] ?? x.harness },
            { id: 'requests', header: 'Requests', align: 'right', mobile: 'aside', cell: (x) => <span className="num">{x.requests}</span> },
            { id: 'first', header: 'Started', mobile: 'meta', cell: (x) => <span title={fmtDateTime(x.first_seen, { seconds: true })}>{fmtRelative(x.first_seen)}</span> },
            { id: 'last', header: 'Last request', mobile: 'meta', cell: (x) => <span title={fmtDateTime(x.last_seen, { seconds: true })}>{fmtRelative(x.last_seen)}</span> },
            { id: 'conf', header: 'Grouping', mobile: 'meta', cell: (x) => (x.confidence === 'low' ? <Badge tone="warning">Uncertain</Badge> : <Text size="xs" c="dimmed" span>{x.source.replace('_', ' ')}</Text>) },
          ]}
        />
      )}
      {list.data && (cursors.length > 0 || list.data.next_cursor) && (
        <Group justify="flex-end" gap="xs">
          <Button size="small" disabled={cursors.length === 0} onClick={() => setCursors(cursors.slice(0, -1))}>
            Newer
          </Button>
          <Button size="small" disabled={!list.data.next_cursor} onClick={() => list.data?.next_cursor && setCursors([...cursors, list.data.next_cursor])}>
            Older
          </Button>
        </Group>
      )}
    </Stack>
  )
}
