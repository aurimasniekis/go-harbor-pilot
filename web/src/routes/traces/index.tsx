import { useState } from 'react'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { AlertTriangle, ScrollText, Search, X } from 'lucide-react'
import { z } from 'zod'
import { PageHeader } from '@/components/PageHeader'
import { Badge, Banner, Button, EmptyState, Panel, TableScroll, uiStyles } from '@/components/ui'
import { QueryError, TableSkeleton } from '@/components/states'
import { GrantNeeded, TraceModeBadge } from '@/features/traces/bits'
import p from '@/features/privacy/privacy.module.css'
import { traceSearchQuery, tracesQuery, type TraceFilter } from '@/lib/api/queries'
import { useCan } from '@/lib/session'
import { fmtCompact, fmtDateTime, fmtRelative } from '@/lib/format'

const search = z.object({
  request_id: z.string().optional().catch(undefined),
  session_id: z.string().optional().catch(undefined),
  binding_id: z.string().optional().catch(undefined),
})

export const Route = createFileRoute('/traces/')({
  validateSearch: (s) => search.parse(s),
  component: Traces,
})

function FilterInput({ label, value, onApply }: { label: string; value?: string; onApply: (v: string | undefined) => void }) {
  const [v, setV] = useState(value ?? '')
  return (
    <label style={{ display: 'inline-flex', flexDirection: 'column', gap: 4, fontSize: 'var(--text-sm)' }}>
      <span className="muted">{label}</span>
      <input className={uiStyles.input} value={v} onChange={(e) => setV(e.target.value)} onBlur={() => onApply(v.trim() || undefined)}
        onKeyDown={(e) => e.key === 'Enter' && onApply(v.trim() || undefined)} spellCheck={false} />
    </label>
  )
}

function SearchPanel() {
  const canRead = useCan('traces.content.read')
  const [text, setText] = useState('')
  const [q, setQ] = useState('')
  const res = useQuery({ ...traceSearchQuery(q), enabled: canRead && q !== '' })
  return (
    <Panel title="Search" subtitle="Searches redacted text only. Expired traces are never returned.">
      {!canRead ? (
        <GrantNeeded perm="traces.content.read" what="Searching trace content" />
      ) : (
        <>
          <form className={p.filters} onSubmit={(e) => { e.preventDefault(); setQ(text.trim()) }}>
            <label style={{ display: 'inline-flex', flexDirection: 'column', gap: 4, fontSize: 'var(--text-sm)', flex: 1, minWidth: 200 }}>
              <span className="muted">Words to find</span>
              <input className={uiStyles.input} value={text} onChange={(e) => setText(e.target.value)} />
            </label>
            <Button type="submit" icon={Search} disabled={!text.trim()}>Search</Button>
          </form>
          {res.isError ? (
            <QueryError error={res.error} />
          ) : q && !res.data ? (
            <TableSkeleton rows={2} />
          ) : res.data && res.data.items.length === 0 ? (
            <p className="secondary">No live trace matches “{q}”.</p>
          ) : res.data ? (
            <TableScroll label="Search results">
              <table className="data-table" style={{ width: '100%' }}>
                <thead>
                  <tr>
                    <th>Trace</th>
                    <th>Excerpt</th>
                    <th>Expires</th>
                  </tr>
                </thead>
                <tbody>
                  {res.data.items.map((h) => (
                    <tr key={h.id}>
                      <td><Link to="/traces/$traceId" params={{ traceId: h.id }} className="mono">{h.id}</Link></td>
                      <td className={p.snippet}>{h.snippet}</td>
                      <td title={fmtDateTime(h.expires_at)}>{fmtRelative(h.expires_at)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </TableScroll>
          ) : null}
        </>
      )}
    </Panel>
  )
}

function Traces() {
  const f = Route.useSearch()
  const navigate = useNavigate({ from: '/traces/' })
  const filter: TraceFilter = f
  const list = useQuery(tracesQuery(filter))
  const set = (k: keyof TraceFilter) => (v: string | undefined) => void navigate({ search: (s) => ({ ...s, [k]: v }) })
  const items = list.data?.items ?? []
  const filtered = !!(f.request_id || f.session_id || f.binding_id)
  return (
    <>
      <PageHeader
        title="Traces"
        subtitle="Short-lived, encrypted interaction content captured under plan trace policies. Content expires on schedule; accounting metadata lives on in Activity."
      />
      <div className={p.stack}>
        <Panel title="Live traces" subtitle="Metadata only. Opening a trace reads its content and is audited.">
          <div className={p.filters}>
            <FilterInput key={`r${f.request_id}`} label="Request id" value={f.request_id} onApply={set('request_id')} />
            <FilterInput key={`s${f.session_id}`} label="Session id" value={f.session_id} onApply={set('session_id')} />
            <FilterInput key={`b${f.binding_id}`} label="Client binding id" value={f.binding_id} onApply={set('binding_id')} />
            {filtered && <Button icon={X} variant="ghost" onClick={() => void navigate({ search: {} })}>Clear filters</Button>}
          </div>
          {list.data && (list.data.dropped > 0 || list.data.over_quota > 0) && (
            <Banner tone="warning" icon={AlertTriangle}>
              Coverage is reduced: {list.data.dropped} capture(s) were dropped because the writer was behind and {list.data.over_quota} skipped by plan
              quotas since the server started.
            </Banner>
          )}
          {list.isError ? (
            <QueryError error={list.error} retry={() => void list.refetch()} />
          ) : !list.data ? (
            <TableSkeleton />
          ) : items.length === 0 ? (
            <EmptyState icon={ScrollText} title={filtered ? 'No live traces match' : 'No live traces'}>
              Content is captured only for plans that select a trace policy under <Link to="/privacy/trace-policies" style={{ textDecoration: 'underline' }}>Privacy → Trace policies</Link>.
            </EmptyState>
          ) : (
            <TableScroll label="Live traces">
              <table className="data-table" style={{ width: '100%' }}>
                <thead>
                  <tr>
                    <th>Trace</th>
                    <th>Request</th>
                    <th>Mode</th>
                    <th>Captured</th>
                    <th>Expires</th>
                    <th>Size</th>
                    <th>Sections</th>
                  </tr>
                </thead>
                <tbody>
                  {items.map((t) => (
                    <tr key={t.id}>
                      <td><Link to="/traces/$traceId" params={{ traceId: t.id }} className="mono">{t.id}</Link></td>
                      <td><Link to="/activity/$requestId" params={{ requestId: t.request_id }} className="mono">{t.request_id}</Link></td>
                      <td><TraceModeBadge mode={t.mode} /></td>
                      <td title={fmtDateTime(t.created_at)}>{fmtRelative(t.created_at)}</td>
                      <td title={fmtDateTime(t.expires_at)}>{fmtRelative(t.expires_at)}</td>
                      <td className="num">
                        {fmtCompact(t.size_bytes)} B {t.truncated && <Badge tone="warning">truncated</Badge>}
                      </td>
                      <td>{t.sections.length}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </TableScroll>
          )}
        </Panel>
        <SearchPanel />
      </div>
    </>
  )
}
