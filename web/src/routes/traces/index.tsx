import { useState } from 'react'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Alert, Button, Group, Select, Stack, Text, TextInput, Title } from '@mantine/core'
import { AlertTriangle, Lock, ScrollText, Search } from 'lucide-react'
import { z } from 'zod'
import { Badge, Banner } from '@/components/ui'
import { QueryError, TableSkeleton } from '@/components/states'
import { TraceModeBadge } from '@/features/traces/bits'
import { resourceListQuery, traceSearchQuery, tracesQuery, type TraceFilter } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { BindingRes, TraceMeta } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { fmtDateTime, fmtRelative } from '@/lib/format'
import { type Column, DataTable, Empty } from '@/ui/kit'

const search = z.object({
  request_id: z.string().optional().catch(undefined),
  session_id: z.string().optional().catch(undefined),
  binding_id: z.string().optional().catch(undefined),
})

export const Route = createFileRoute('/traces/')({
  validateSearch: (s) => search.parse(s),
  component: Traces,
})

/** 1640 → "1.6 KB". */
function size(b: number): string {
  if (b < 1024) return `${b} B`
  if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} KB`
  return `${(b / 1024 / 1024).toFixed(1)} MB`
}

function ContentSearch() {
  const [text, setText] = useState('')
  const [q, setQ] = useState('')
  const res = useQuery({ ...traceSearchQuery(q), enabled: q !== '' })
  return (
    <Stack gap="sm">
      <Title order={2}>Search captured content</Title>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          setQ(text.trim())
        }}
      >
        <Group gap="xs" wrap="nowrap">
          <TextInput aria-label="Words to find" placeholder="Words to find (masked text only)" value={text} onChange={(e) => setText(e.currentTarget.value)} style={{ flex: 1 }} maw={420} />
          <Button type="submit" leftSection={<Search size={14} />} disabled={!text.trim()}>
            Search
          </Button>
        </Group>
      </form>
      {res.isError ? (
        <QueryError error={res.error} />
      ) : q && !res.data ? (
        <TableSkeleton rows={2} />
      ) : res.data && res.data.items.length === 0 ? (
        <Text size="sm" c="dimmed">
          Nothing kept matches “{q}”.
        </Text>
      ) : res.data ? (
        <Stack gap={6}>
          {res.data.items.map((h) => (
            <Link key={h.id} to="/traces/$traceId" params={{ traceId: h.id }}>
              <Text size="sm">{h.snippet}</Text>
              <Text size="xs" c="dimmed">
                Kept until {fmtDateTime(h.expires_at)}
              </Text>
            </Link>
          ))}
        </Stack>
      ) : null}
    </Stack>
  )
}

function Traces() {
  const f = Route.useSearch()
  const navigate = useNavigate({ from: '/traces/' })
  const canRead = useCan('traces.content.read')
  const filter: TraceFilter = f
  const list = useQuery(tracesQuery(filter))
  const bindings = useQuery(resourceListQuery<BindingRes>('client-bindings'))
  const client = byId(bindings.data?.items)
  const [id, setId] = useState(f.request_id ?? f.session_id ?? '')
  const items = list.data?.items ?? []
  const filtered = !!(f.request_id || f.session_id || f.binding_id)

  const cols: Column<TraceMeta>[] = [
    {
      id: 'when',
      header: 'Captured',
      mobile: 'aside',
      cell: (t) => <span title={fmtDateTime(t.created_at)}>{fmtRelative(t.created_at)}</span>,
    },
    {
      id: 'client',
      header: 'Client',
      mobile: 'title',
      cell: (t) => (
        <Stack gap={0}>
          <Text size="sm" fw={500}>
            {client[t.binding_id]?.spec.display_name || client[t.binding_id]?.name || 'Unknown client'}
          </Text>
          <Link to="/activity/$requestId" params={{ requestId: t.request_id }} onClick={(e) => e.stopPropagation()}>
            <Text size="xs" span>
              View request
            </Text>
          </Link>
        </Stack>
      ),
    },
    { id: 'mode', header: 'Content', mobile: 'meta', cell: (t) => <TraceModeBadge mode={t.mode} /> },
    { id: 'kept', header: 'Kept until', mobile: 'meta', cell: (t) => <span title={fmtDateTime(t.expires_at)}>{fmtRelative(t.expires_at)}</span> },
    {
      id: 'size',
      header: 'Size',
      align: 'right',
      mobile: 'meta',
      cell: (t) => (
        <>
          {size(t.size_bytes)}
          {t.truncated && (
            <>
              {' '}
              <Badge tone="warning">Shortened</Badge>
            </>
          )}
        </>
      ),
    },
  ]

  return (
    <Stack gap="lg">
      {!canRead && (
        <Alert color="gray" variant="light" icon={<Lock size={16} />}>
          <Text size="sm">
            You can see what was captured but not read it. Reading content needs an explicit grant; ask an administrator to add it under{' '}
            <Link to="/settings/users" style={{ textDecoration: 'underline' }}>Settings → People</Link>. Every read is audited.
          </Text>
        </Alert>
      )}
      <Group gap="xs" wrap="wrap" role="search" aria-label="Filter captured content">
        <form
          onSubmit={(e) => {
            e.preventDefault()
            const v = id.trim()
            void navigate({ search: (s) => ({ ...s, request_id: v.startsWith('req_') ? v : undefined, session_id: v && !v.startsWith('req_') ? v : undefined }) })
          }}
        >
          <TextInput aria-label="Request or session id" placeholder="Request or session id" leftSection={<Search size={14} />} value={id} onChange={(e) => setId(e.currentTarget.value)} w={260} />
        </form>
        <Select
          aria-label="Client"
          placeholder="Any client"
          clearable
          searchable
          w={200}
          value={f.binding_id ?? null}
          onChange={(v) => void navigate({ search: (s) => ({ ...s, binding_id: v ?? undefined }) })}
          data={(bindings.data?.items ?? []).map((b) => ({ value: b.id, label: b.spec.display_name || b.name }))}
        />
        {filtered && (
          <Button variant="subtle" color="gray" size="sm" onClick={() => { setId(''); void navigate({ search: {} }) }}>
            Clear
          </Button>
        )}
      </Group>
      {list.data && (list.data.dropped > 0 || list.data.over_quota > 0) && (
        <Banner tone="warning" icon={AlertTriangle}>
          Some content wasn’t captured: {list.data.dropped} dropped while the writer was busy and {list.data.over_quota} over a plan’s storage limit since the
          server started.
        </Banner>
      )}
      {list.isError ? (
        <QueryError error={list.error} retry={() => void list.refetch()} />
      ) : !list.data ? (
        <TableSkeleton />
      ) : (
        <DataTable
          label="Captured content"
          rows={items}
          columns={cols}
          rowKey={(t) => t.id}
          onRow={(t) => void navigate({ to: '/traces/$traceId', params: { traceId: t.id } })}
          empty={
            <Empty icon={<ScrollText size={22} />} title={filtered ? 'Nothing captured matches' : 'No live traces'}>
              Content is captured only for plans that choose a <Link to="/privacy/trace-policies" style={{ textDecoration: 'underline' }}>content capture rule</Link>.
            </Empty>
          }
        />
      )}
      {canRead && <ContentSearch />}
    </Stack>
  )
}
