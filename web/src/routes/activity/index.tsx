import { useState } from 'react'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Activity as ActivityIcon, X } from 'lucide-react'
import { z } from 'zod'
import { Badge, Button, EmptyState, Panel, TableScroll, uiStyles } from '@/components/ui'
import { QueryError, TableSkeleton } from '@/components/states'
import { requestsQuery, resourceListQuery, type RequestFilter } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { AliasRes, BindingRes, ProjectRes } from '@/lib/api/types'
import { fmtDateTime, fmtRelative } from '@/lib/format'
import { CostValue, HarnessBadge, Ms, ProtocolBadge, StatusBadge, TokenTriple } from '@/features/activity/bits'

const search = z.object({
  status: z.string().optional().catch(undefined),
  harness: z.string().optional().catch(undefined),
  binding_id: z.string().optional().catch(undefined),
  project_id: z.string().optional().catch(undefined),
  session_id: z.string().optional().catch(undefined),
  model: z.string().optional().catch(undefined),
})

export const Route = createFileRoute('/activity/')({
  validateSearch: (s) => search.parse(s),
  component: Requests,
})

function FilterSelect({ label, value, onChange, options }: {
  label: string
  value: string | undefined
  onChange: (v: string | undefined) => void
  options: { value: string; label: string }[]
}) {
  return (
    <label style={{ display: 'inline-flex', flexDirection: 'column', gap: 4, fontSize: 'var(--text-sm)' }}>
      <span className="muted">{label}</span>
      <select className={uiStyles.select} value={value ?? ''} onChange={(e) => onChange(e.target.value || undefined)}>
        <option value="">All</option>
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    </label>
  )
}

function Requests() {
  const f = Route.useSearch()
  const navigate = useNavigate({ from: '/activity/' })
  const [cursors, setCursors] = useState<string[]>([])
  const cursor = cursors[cursors.length - 1]
  const filter: RequestFilter = { ...f, cursor }
  const list = useQuery(requestsQuery(filter))
  const bindings = useQuery(resourceListQuery<BindingRes>('client-bindings'))
  const projects = useQuery(resourceListQuery<ProjectRes>('projects'))
  const aliases = useQuery(resourceListQuery<AliasRes>('model-aliases'))
  const bindingById = byId(bindings.data?.items)
  const projectById = byId(projects.data?.items)
  const aliasById = byId(aliases.data?.items)
  const set = (patch: Partial<typeof f>) => {
    setCursors([])
    void navigate({ search: { ...f, ...patch } })
  }
  const [model, setModel] = useState(f.model ?? '')
  const filtered = Object.values(f).some(Boolean)
  const items = list.data?.items ?? []

  return (
    <Panel title="Requests" subtitle="Newest first. Token counts show — when the upstream did not report them; they are never shown as zero.">
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12, alignItems: 'flex-end', marginBottom: 12 }} role="search" aria-label="Filter requests">
        <FilterSelect label="Status" value={f.status} onChange={(v) => set({ status: v })}
          options={['ok', 'error', 'blocked', 'canceled', 'in_progress'].map((v) => ({ value: v, label: v.replace('_', ' ') }))} />
        <FilterSelect label="Harness" value={f.harness} onChange={(v) => set({ harness: v })}
          options={[['claude_code', 'Claude Code'], ['codex', 'Codex'], ['opencode', 'OpenCode'], ['pi', 'Pi'], ['generic', 'Generic']].map(([v, l]) => ({ value: v!, label: l! }))} />
        <FilterSelect label="Client" value={f.binding_id} onChange={(v) => set({ binding_id: v })}
          options={(bindings.data?.items ?? []).map((b) => ({ value: b.id, label: b.name }))} />
        <FilterSelect label="Project" value={f.project_id} onChange={(v) => set({ project_id: v })}
          options={(projects.data?.items ?? []).map((p) => ({ value: p.id, label: p.name }))} />
        <form
          onSubmit={(e) => {
            e.preventDefault()
            set({ model: model || undefined })
          }}
          style={{ display: 'inline-flex', flexDirection: 'column', gap: 4, fontSize: 'var(--text-sm)' }}
        >
          <label htmlFor="req-model" className="muted">
            Requested model
          </label>
          <input id="req-model" className={uiStyles.input} value={model} onChange={(e) => setModel(e.target.value)} placeholder="exact name, Enter to apply" />
        </form>
        {f.session_id && (
          <Badge tone="info">
            session <span className="mono">{f.session_id}</span>
          </Badge>
        )}
        {filtered && (
          <Button size="small" variant="ghost" icon={X} onClick={() => { setModel(''); set({ status: undefined, harness: undefined, binding_id: undefined, project_id: undefined, session_id: undefined, model: undefined }) }}>
            Clear filters
          </Button>
        )}
      </div>
      {list.isError ? (
        <QueryError error={list.error} retry={() => void list.refetch()} />
      ) : !list.data ? (
        <TableSkeleton />
      ) : items.length === 0 ? (
        <EmptyState icon={ActivityIcon} title={filtered ? 'No matching requests' : 'No requests yet'}>
          {filtered ? (
            'Nothing matches these filters.'
          ) : (
            <>
              Point a client at this gateway with a credential from <Link to="/clients">Clients</Link>; every request it makes appears here.
            </>
          )}
        </EmptyState>
      ) : (
        <>
          <TableScroll label="Requests">
            <table className="data-table" style={{ width: '100%' }}>
              <thead>
                <tr>
                  <th>Time</th>
                  <th>Client</th>
                  <th>Harness</th>
                  <th>Protocol</th>
                  <th>Model</th>
                  <th>Status</th>
                  <th>Attempts</th>
                  <th>TTFT / total</th>
                  <th>Tokens in / out</th>
                  <th>Cost</th>
                  <th>Tools</th>
                </tr>
              </thead>
              <tbody>
                {items.map((r) => (
                  <tr key={r.id}>
                    <td title={fmtDateTime(r.created_at, { seconds: true })} style={{ whiteSpace: 'nowrap' }}>
                      <Link to="/activity/$requestId" params={{ requestId: r.id }}>
                        {fmtRelative(r.created_at)}
                      </Link>
                    </td>
                    <td>{bindingById[r.binding_id]?.name ?? <span className="mono muted">{r.binding_id || '—'}</span>}</td>
                    <td>
                      <HarnessBadge h={r.harness} />
                    </td>
                    <td>
                      <ProtocolBadge p={r.inbound_protocol} />
                    </td>
                    <td style={{ minWidth: 180 }}>
                      <span className="mono">{r.requested_model || '—'}</span>
                      {r.alias_id && aliasById[r.alias_id] && <span className="muted"> → {aliasById[r.alias_id]!.name}</span>}
                      {r.final_model && <div className="muted mono">→ {r.final_model}</div>}
                    </td>
                    <td>
                      <StatusBadge status={r.status} />
                      {r.error_kind && <div className="muted">{r.error_kind}</div>}
                    </td>
                    <td>{r.attempts > 1 ? <Badge tone="warning" title="More than one upstream attempt">{r.attempts} · failover</Badge> : r.attempts}</td>
                    <td style={{ whiteSpace: 'nowrap' }}>
                      <Ms v={r.ttft_ms} /> <span className="muted">/</span> <Ms v={r.duration_ms} />
                    </td>
                    <td>
                      <TokenTriple t={r.tokens} />
                    </td>
                    <td>
                      <CostValue cost={r.cost} />
                    </td>
                    <td className="num">{r.tool_calls}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </TableScroll>
          <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 12 }}>
            <Button size="small" disabled={cursors.length === 0} onClick={() => setCursors(cursors.slice(0, -1))}>
              Newer
            </Button>
            <Button size="small" disabled={!list.data.next_cursor} onClick={() => list.data?.next_cursor && setCursors([...cursors, list.data.next_cursor])}>
              Older
            </Button>
          </div>
        </>
      )}
      {f.project_id && projectById[f.project_id] && <p className="muted">Project: {projectById[f.project_id]!.name}</p>}
    </Panel>
  )
}
