import { useEffect, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ClipboardList, FileCode2, RefreshCw, Save } from 'lucide-react'
import { Badge, Banner, Button, EmptyState, KeyValues, Panel, TableScroll } from '@/components/ui'
import { Checkbox, Field, MutationError, SelectField, TextField, formStyles as f } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { ApiError, apiSend } from '@/lib/api/client'
import { workItemQuery, workItemsQuery } from '@/lib/api/queries'
import type { WorkItem, WorkStatus, WorkUpdate } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { fmtDateTime, fmtRelative } from '@/lib/format'
import { prettyJSON, statusLabel, workStatuses, workTone } from './compat'
import c from './compat.module.css'

export function WorkStatusBadge({ s }: { s: string }) {
  return <Badge tone={workTone[s] ?? 'neutral'}>{statusLabel(s)}</Badge>
}

export function WorkItemsPanel({ status, onStatus }: { status?: WorkStatus; onStatus: (s: WorkStatus | undefined) => void }) {
  const q = useQuery(workItemsQuery(status))
  return (
    <Panel title="Follow-ups" subtitle="One follow-up per observed protocol change. Repeats add evidence instead of new items; resolved follow-ups reopen if the change returns.">
      <div className={c.filters}>
        <SelectField label="Status" value={status ?? ''} onChange={(v) => onStatus((v || undefined) as WorkStatus | undefined)}
          options={[{ value: '', label: 'All statuses' }, ...workStatuses.map((s) => ({ value: s.value as string, label: s.label }))]} />
      </div>
      {q.isError ? (
        <QueryError error={q.error} retry={() => void q.refetch()} />
      ) : !q.data ? (
        <TableSkeleton rows={4} />
      ) : q.data.items.length === 0 ? (
        <EmptyState icon={ClipboardList} title={status ? `No ${statusLabel(status).toLowerCase()} follow-ups` : 'No follow-ups'}>
          A follow-up is created automatically when Diagnostics records a new protocol change.
        </EmptyState>
      ) : (
        <TableScroll label="Follow-ups">
          <table className="data-table" style={{ width: '100%' }}>
            <thead>
              <tr>
                <th>Title</th>
                <th>Status</th>
                <th>Routes</th>
                <th>Evidence</th>
                <th>Fixture</th>
                <th>Updated</th>
              </tr>
            </thead>
            <tbody>
              {q.data.items.map((w) => (
                <tr key={w.id}>
                  <td style={{ maxWidth: 420 }}>
                    <Link to="/compatibility/work-items/$itemId" params={{ itemId: w.id }}>{w.title}</Link>
                  </td>
                  <td><WorkStatusBadge s={w.status} /></td>
                  <td className="num">{w.routes.length}</td>
                  <td className="num">{w.evidence.length}</td>
                  <td>{w.fixture != null ? (w.fixture_reviewed ? <Badge tone="success">reviewed</Badge> : <Badge tone="warning">needs review</Badge>) : <span className="muted">—</span>}</td>
                  <td title={fmtDateTime(w.updated_at)}>{fmtRelative(w.updated_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableScroll>
      )}
    </Panel>
  )
}

interface Draft {
  status: WorkStatus
  issue_url: string
  commit_ref: string
  release_ref: string
  notes: string
}

function draftOf(w: WorkItem): Draft {
  return { status: w.status, issue_url: w.issue_url, commit_ref: w.commit_ref, release_ref: w.release_ref, notes: w.notes }
}

function Triage({ w }: { w: WorkItem }) {
  const qc = useQueryClient()
  const canManage = useCan('compat.manage')
  const [d, setD] = useState<Draft>(() => draftOf(w))
  const [server, setServer] = useState<Draft>(() => draftOf(w))
  const [base, setBase] = useState(w.revision)
  useEffect(() => {
    // Adopt a newer server version only when nothing was edited locally;
    // otherwise keep the edits and let If-Match report the conflict.
    if (w.revision !== base && JSON.stringify(d) === JSON.stringify(server)) {
      setD(draftOf(w))
      setServer(draftOf(w))
      setBase(w.revision)
    }
  }, [w, base, d, server])
  const save = useMutation({
    mutationFn: (body: WorkUpdate) => apiSend<WorkItem>('PATCH', `/protocol-watch/work-items/${encodeURIComponent(w.id)}`, body, { ifMatch: base }),
    onSuccess: (nw) => {
      qc.setQueryData(workItemQuery(w.id).queryKey, nw)
      setBase(nw.revision)
      setD(draftOf(nw))
      setServer(draftOf(nw))
      void qc.invalidateQueries({ queryKey: ['protocol-watch', 'work-items'] })
    },
  })
  const stale = save.error instanceof ApiError && save.error.status === 412
  const reload = async () => {
    const fresh = await qc.fetchQuery({ ...workItemQuery(w.id), staleTime: 0 })
    setD(draftOf(fresh))
    setServer(draftOf(fresh))
    setBase(fresh.revision)
    save.reset()
  }
  if (!canManage) {
    return (
      <Panel title="Triage">
        <Banner tone="info">Triage needs the compat.manage permission.</Banner>
      </Panel>
    )
  }
  return (
    <Panel title="Triage" subtitle="Changes are saved only if nobody else updated this item since you opened it.">
      <form className={c.stack} onSubmit={(e) => { e.preventDefault(); save.mutate(d) }}>
        <div className={f.grid}>
          <SelectField label="Status" value={d.status} onChange={(v) => setD({ ...d, status: v })} options={workStatuses.map((s) => ({ ...s }))} />
          <TextField label="Issue URL" value={d.issue_url} onChange={(v) => setD({ ...d, issue_url: v })} maxLength={500} placeholder="https://…" />
          <TextField label="Commit" value={d.commit_ref} onChange={(v) => setD({ ...d, commit_ref: v })} maxLength={200} />
          <TextField label="Release" value={d.release_ref} onChange={(v) => setD({ ...d, release_ref: v })} maxLength={200} />
        </div>
        <Field label="Notes">
          {(fp) => <textarea {...fp} className={f.textarea} rows={4} maxLength={4000} value={d.notes} onChange={(e) => setD({ ...d, notes: e.target.value })} />}
        </Field>
        {stale ? (
          <Banner tone="warning" role="alert">
            This follow-up was changed by someone else since you opened it. Reload to see the latest version, then reapply your edits.{' '}
            <Button size="small" icon={RefreshCw} onClick={() => void reload()}>Reload</Button>
          </Banner>
        ) : (
          <MutationError error={save.error} />
        )}
        {save.isSuccess && !save.isPending && <p className="secondary" role="status">Saved.</p>}
        <div>
          <Button type="submit" variant="primary" icon={Save} busy={save.isPending}>Save triage</Button>
        </div>
      </form>
    </Panel>
  )
}

function Fixture({ w }: { w: WorkItem }) {
  const qc = useQueryClient()
  const canManage = useCan('compat.manage')
  const set = (nw: WorkItem) => {
    qc.setQueryData(workItemQuery(w.id).queryKey, nw)
    void qc.invalidateQueries({ queryKey: ['protocol-watch', 'work-items'] })
  }
  const gen = useMutation({
    mutationFn: () => apiSend<WorkItem>('POST', `/protocol-watch/work-items/${encodeURIComponent(w.id)}/fixture`),
    onSuccess: set,
  })
  const review = useMutation({
    mutationFn: (v: boolean) => apiSend<WorkItem>('PATCH', `/protocol-watch/work-items/${encodeURIComponent(w.id)}`, { fixture_reviewed: v }, { ifMatch: w.revision }),
    onSuccess: set,
  })
  return (
    <Panel title="Synthetic fixture" subtitle="Built from the structural diff with placeholder values only — never captured content.">
      {w.fixture ? (
        <>
          <pre className={c.json} aria-label="Fixture JSON">{prettyJSON(w.fixture)}</pre>
          <Checkbox label="Reviewed and safe to add to the protocol test fixtures" checked={w.fixture_reviewed}
            disabled={!canManage || review.isPending} onChange={(v) => review.mutate(v)} />
          {review.error instanceof ApiError && review.error.status === 412 ? (
            <Banner tone="warning">The item changed since it was loaded; reload the page and try again.</Banner>
          ) : (
            <MutationError error={review.error} />
          )}
        </>
      ) : (
        <p className="secondary">No fixture yet.</p>
      )}
      {canManage && (
        <div>
          <Button icon={FileCode2} busy={gen.isPending} onClick={() => gen.mutate()}>
            {w.fixture ? 'Regenerate fixture' : 'Generate fixture'}
          </Button>
        </div>
      )}
      {w.fixture ? <p className="muted">Regenerating clears the reviewed flag.</p> : null}
      <MutationError error={gen.error} />
    </Panel>
  )
}

export function WorkItemDetail({ w }: { w: WorkItem }) {
  return (
    <div className={c.stack}>
      <Panel title="Change">
        <KeyValues
          items={[
            ['Status', <WorkStatusBadge s={w.status} />],
            ['Created', <span title={fmtDateTime(w.created_at)}>{fmtRelative(w.created_at)}</span>],
            ['Updated', <span title={fmtDateTime(w.updated_at)}>{fmtRelative(w.updated_at)}</span>],
            ['Issue', w.issue_url ? <a href={w.issue_url} target="_blank" rel="noreferrer noopener">{w.issue_url}</a> : <span className="muted">—</span>],
            ['Commit', w.commit_ref || <span className="muted">—</span>],
            ['Release', w.release_ref || <span className="muted">—</span>],
          ]}
        />
        <h3 style={{ fontSize: 'var(--text-md)' }}>Sanitized structural diff</h3>
        <pre className={c.json} aria-label="Structural diff">{prettyJSON(w.diff)}</pre>
        <h3 style={{ fontSize: 'var(--text-md)' }}>Action taken and features lost</h3>
        {w.action && typeof w.action === 'object' && Object.keys(w.action as object).length > 0 ? (
          <pre className={c.json} aria-label="Action taken">{prettyJSON(w.action)}</pre>
        ) : (
          <p className="secondary">No automatic action was recorded.</p>
        )}
      </Panel>
      <div style={{ display: 'grid', gap: 'var(--space-4)', gridTemplateColumns: 'repeat(auto-fit, minmax(320px, 1fr))' }}>
        <Panel title="Affected routes" subtitle="Provider and protocol path.">
          {w.routes.length === 0 ? (
            <p className="secondary">No route recorded.</p>
          ) : (
            <ul className={c.list}>{w.routes.map((r) => <li key={r} className={c.path}>{r}</li>)}</ul>
          )}
        </Panel>
        <Panel title="Evidence" subtitle="Request ids only. Their activity records or traces may already have expired; the structural record remains.">
          {w.evidence.length === 0 ? (
            <p className="secondary">No request evidence recorded.</p>
          ) : (
            <ul className={c.list}>
              {w.evidence.map((id) => (
                <li key={id}><Link to="/activity/$requestId" params={{ requestId: id }} className="mono">{id}</Link></li>
              ))}
            </ul>
          )}
        </Panel>
      </div>
      <Triage key={w.id} w={w} />
      <Fixture w={w} />
    </div>
  )
}
