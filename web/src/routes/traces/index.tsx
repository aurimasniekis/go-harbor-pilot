import { Fragment, useState } from 'react'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Alert, Button, Group, SegmentedControl, Select, Stack, Text, TextInput } from '@mantine/core'
import { AlertTriangle, Lock, ScrollText, Search } from 'lucide-react'
import { z } from 'zod'
import { Badge, Banner } from '@/components/ui'
import { QueryError, TableSkeleton } from '@/components/states'
import { fmtSize, GrantLink, TraceModeBadge } from '@/features/traces/bits'
import { resourceListQuery, traceSearchQuery, tracesQuery, type TraceFilter } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { BindingRes, PlanRes, TraceHit, TraceMeta } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { fmtDateTime, fmtRelative } from '@/lib/format'
import { type Column, DataTable, Empty } from '@/ui/kit'

const search = z.object({
  request_id: z.string().optional().catch(undefined),
  session_id: z.string().optional().catch(undefined),
  binding_id: z.string().optional().catch(undefined),
  q: z.string().optional().catch(undefined),
})

export const Route = createFileRoute('/traces/')({
  validateSearch: (s) => search.parse(s),
  component: Traces,
})

/** The excerpt with matches marked; section breaks shown as separators. */
function Snippet({ hit }: { hit: TraceHit }) {
  const parts = hit.snippet_parts?.length ? hit.snippet_parts : [{ text: hit.snippet, match: false }]
  return (
    <Text size="sm" style={{ overflowWrap: 'anywhere' }}>
      {parts.map((p, i) => {
        const pieces = p.text.split('\n')
        const body = pieces.map((t, j) => (
          <Fragment key={j}>
            {j > 0 && <Text span c="dimmed"> · </Text>}
            {t}
          </Fragment>
        ))
        return p.match ? <mark key={i}>{body}</mark> : <Fragment key={i}>{body}</Fragment>
      })}
    </Text>
  )
}

function Traces() {
  const f = Route.useSearch()
  const navigate = useNavigate({ from: '/traces/' })
  const canRead = useCan('traces.content.read')
  const filter: TraceFilter = { request_id: f.request_id, session_id: f.session_id, binding_id: f.binding_id }
  const list = useQuery(tracesQuery(filter))
  const bindings = useQuery(resourceListQuery<BindingRes>('client-bindings'))
  const plans = useQuery(resourceListQuery<PlanRes>('routing-plans'))
  const client = byId(bindings.data?.items)
  const plan = byId(plans.data?.items)
  const [mode, setMode] = useState<'id' | 'words'>(f.q ? 'words' : 'id')
  const [text, setText] = useState(f.q ?? f.request_id ?? f.session_id ?? '')
  const hits = useQuery({ ...traceSearchQuery(canRead ? (f.q ?? '') : ''), enabled: !!f.q && canRead })
  const items = list.data?.items ?? []
  const words = canRead ? (f.q ?? '') : ''
  const filtered = !!(f.request_id || f.session_id || f.binding_id || words)
  const clientName = (id: string) => client[id]?.spec.display_name || client[id]?.name || 'Unknown client'
  const planName = (id: string) => (id ? plan[id]?.spec.display_name || plan[id]?.name || '—' : '—')
  const clear = () => {
    setText('')
    void navigate({ search: {} })
  }

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
            {clientName(t.binding_id)}
          </Text>
          <Link to="/activity/$requestId" params={{ requestId: t.request_id }} onClick={(e) => e.stopPropagation()}>
            <Text size="xs" span>
              View request
            </Text>
          </Link>
        </Stack>
      ),
    },
    { id: 'plan', header: 'Plan', mobile: 'meta', cell: (t) => planName(t.plan_id) },
    { id: 'mode', header: 'Content', mobile: 'meta', cell: (t) => <TraceModeBadge mode={t.mode} /> },
    { id: 'kept', header: 'Kept until', mobile: 'meta', cell: (t) => <span title={fmtDateTime(t.expires_at)}>{fmtRelative(t.expires_at)}</span> },
    {
      id: 'size',
      header: 'Size',
      align: 'right',
      mobile: 'meta',
      cell: (t) => (
        <>
          {fmtSize(t.size_bytes)}
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

  const hitCols: Column<TraceHit>[] = [
    { id: 'when', header: 'Captured', mobile: 'aside', cell: (h) => <span title={fmtDateTime(h.created_at)}>{fmtRelative(h.created_at)}</span> },
    { id: 'client', header: 'Client', mobile: 'title', cell: (h) => clientName(h.binding_id) },
    { id: 'match', header: 'Match', mobile: 'line', cell: (h) => <Snippet hit={h} /> },
  ]
  const submit = () => {
    const v = text.trim()
    if (mode === 'words') void navigate({ search: (s) => ({ binding_id: s.binding_id, q: v || undefined }) })
    else void navigate({ search: (s) => ({ binding_id: s.binding_id, request_id: v.startsWith('req_') ? v : undefined, session_id: v && !v.startsWith('req_') ? v : undefined }) })
  }
  const nothing = (
    <Empty icon={<ScrollText size={22} />} title={filtered ? 'Nothing matches' : 'Nothing captured yet'}>
      {filtered ? (
        <>
          No captured content matches {words || f.request_id || f.session_id ? <>“{words || f.request_id || f.session_id}”</> : 'this client'}.{' '}
          <Button variant="subtle" size="compact-sm" onClick={clear}>
            Clear filters
          </Button>
        </>
      ) : (
        <>
          Content is captured only for plans that choose a{' '}
          <Link to="/privacy/trace-policies" style={{ textDecoration: 'underline' }}>
            content capture rule
          </Link>
          .
        </>
      )}
    </Empty>
  )

  return (
    <Stack gap="lg">
      {!canRead && (
        <Alert color="gray" variant="light" icon={<Lock size={16} />}>
          <Text size="sm">
            You can see what was captured but not read it. Reading content needs an explicit grant; ask an administrator to add it under <GrantLink />. Every
            read is audited.
          </Text>
        </Alert>
      )}
      <form
        role="search"
        aria-label="Find captured content"
        onSubmit={(e) => {
          e.preventDefault()
          submit()
        }}
      >
        <Group gap="xs" wrap="wrap" align="flex-end">
          {canRead && (
            <SegmentedControl aria-label="Find by" value={mode} onChange={(v) => setMode(v as 'id' | 'words')}
              data={[{ value: 'id', label: 'Request or session id' }, { value: 'words', label: 'Words in content' }]} />
          )}
          <TextInput aria-label={mode === 'words' ? 'Words to find' : 'Request or session id'} placeholder={mode === 'words' ? 'Words to find (masked text only)' : 'e.g. req_01…'}
            leftSection={<Search size={14} />} value={text} onChange={(e) => setText(e.currentTarget.value)} w={280} />
          <Button type="submit" disabled={!text.trim()}>
            Find
          </Button>
          <Select aria-label="Client" placeholder="Any client" clearable searchable w={200} value={f.binding_id ?? null}
            onChange={(v) => void navigate({ search: (s) => ({ ...s, binding_id: v ?? undefined }) })}
            data={(bindings.data?.items ?? []).map((b) => ({ value: b.id, label: b.spec.display_name || b.name }))} />
          {filtered && (
            <Button variant="subtle" color="gray" onClick={clear}>
              Clear
            </Button>
          )}
        </Group>
      </form>
      {list.data && (list.data.dropped > 0 || list.data.over_quota > 0) && (
        <Banner tone="warning" icon={AlertTriangle}>
          Some content wasn’t captured: {list.data.dropped} dropped while the writer was busy and {list.data.over_quota} over a plan’s storage limit since the
          server started.
        </Banner>
      )}
      {words ? (
        hits.isError ? (
          <QueryError error={hits.error} />
        ) : !hits.data ? (
          <TableSkeleton rows={2} />
        ) : (
          <DataTable label="Search results" rows={hits.data.items.filter((h) => !f.binding_id || h.binding_id === f.binding_id)} columns={hitCols} rowKey={(h) => h.id}
            onRow={(h) => void navigate({ to: '/traces/$traceId', params: { traceId: h.id } })} empty={nothing} />
        )
      ) : list.isError ? (
        <QueryError error={list.error} retry={() => void list.refetch()} />
      ) : !list.data ? (
        <TableSkeleton />
      ) : (
        <DataTable label="Captured content" rows={items} columns={cols} rowKey={(t) => t.id}
          onRow={canRead ? (t) => void navigate({ to: '/traces/$traceId', params: { traceId: t.id } }) : undefined} empty={nothing} />
      )}
    </Stack>
  )
}
