import { useState } from 'react'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Compass, Download, ShieldAlert, Trash2 } from 'lucide-react'
import { PageHeader } from '@/components/PageHeader'
import { Badge, Banner, Button, EmptyState, KeyValues, Panel, Skeleton } from '@/components/ui'
import { MutationError } from '@/components/form'
import { QueryError } from '@/components/states'
import { GrantNeeded, TraceModeBadge } from '@/features/traces/bits'
import { TraceSections } from '@/features/traces/TraceSections'
import p from '@/features/privacy/privacy.module.css'
import { API, ApiError, apiSend } from '@/lib/api/client'
import { traceQuery } from '@/lib/api/queries'
import { useCan } from '@/lib/session'
import { fmtCompact, fmtDateTime, fmtRelative } from '@/lib/format'

export const Route = createFileRoute('/traces/$traceId')({
  component: TraceView,
})

/** Downloads the export without caching it anywhere but the user's disk. */
async function downloadExport(id: string): Promise<void> {
  const res = await fetch(`${API}/traces/${encodeURIComponent(id)}/export`, { credentials: 'same-origin', cache: 'no-store' })
  if (!res.ok) {
    let msg = res.statusText
    try {
      const b = (await res.json()) as { error?: { message?: string; code?: string } }
      msg = b.error?.message ?? msg
      throw new ApiError(res.status, b.error?.code ?? 'error', msg)
    } catch (e) {
      if (e instanceof ApiError) throw e
      throw new ApiError(res.status, 'error', msg)
    }
  }
  const blob = await res.blob()
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${id}.json`
  a.click()
  URL.revokeObjectURL(url)
}

function TraceView() {
  const { traceId } = Route.useParams()
  const canRead = useCan('traces.content.read')
  const canExport = useCan('traces.export')
  const canDelete = useCan('traces.delete')
  const q = useQuery({ ...traceQuery(traceId), enabled: canRead })
  const navigate = useNavigate()
  const qc = useQueryClient()
  const [confirming, setConfirming] = useState(false)
  const exp = useMutation({ mutationFn: () => downloadExport(traceId) })
  const del = useMutation({
    mutationFn: () => apiSend<void>('DELETE', `/traces/${encodeURIComponent(traceId)}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['traces'] })
      void navigate({ to: '/traces' })
    },
  })

  const back = (
    <p>
      <Link to="/traces">← All traces</Link>
    </p>
  )
  if (!canRead) {
    return (
      <>
        {back}
        <Panel>
          <GrantNeeded perm="traces.content.read" what="Reading trace content" />
        </Panel>
      </>
    )
  }
  if (q.isError) {
    const e = q.error
    return (
      <>
        {back}
        {e instanceof ApiError && e.status === 404 ? (
          <Panel>
            <EmptyState icon={Compass} title="Trace not found or expired">
              Expired traces are unreadable immediately and are erased shortly after, together with their search entries and keys.
            </EmptyState>
          </Panel>
        ) : e instanceof ApiError && e.status === 403 ? (
          <Panel>
            <GrantNeeded perm="traces.content.unredacted" what="Reading unredacted content" />
          </Panel>
        ) : (
          <QueryError error={e} retry={() => void q.refetch()} />
        )}
      </>
    )
  }
  if (!q.data) return <Skeleton height={240} />
  const t = q.data
  return (
    <>
      {back}
      <PageHeader
        eyebrow="Trace"
        title={<span className="mono">{t.id}</span>}
        subtitle={
          <span style={{ display: 'inline-flex', gap: 12, flexWrap: 'wrap', alignItems: 'center' }}>
            <TraceModeBadge mode={t.mode} />
            <span>
              Request <Link to="/activity/$requestId" params={{ requestId: t.request_id }} className="mono">{t.request_id}</Link>
            </span>
            <span title={fmtDateTime(t.expires_at)}>Expires {fmtRelative(t.expires_at)}</span>
          </span>
        }
        actions={
          <span style={{ display: 'inline-flex', gap: 8, flexWrap: 'wrap' }}>
            {canExport && <Button icon={Download} busy={exp.isPending} onClick={() => exp.mutate()}>Export</Button>}
            {canDelete && !confirming && <Button icon={Trash2} onClick={() => setConfirming(true)}>Delete</Button>}
            {canDelete && confirming && (
              <>
                <Button variant="danger" icon={Trash2} busy={del.isPending} onClick={() => del.mutate()}>Confirm delete</Button>
                <Button variant="ghost" onClick={() => setConfirming(false)}>Cancel</Button>
              </>
            )}
          </span>
        }
      />
      <div className={p.stack}>
        <Banner tone="info" icon={ShieldAlert}>
          Opening, exporting and deleting trace content is recorded in the audit log. This content expires {fmtDateTime(t.expires_at)} and cannot be read after that.
        </Banner>
        {t.mode === 'unredacted' && <Banner tone="warning">This trace holds unredacted content, which may include secrets the client sent.</Banner>}
        <MutationError error={exp.error} />
        <MutationError error={del.error} />
        <Panel title="Trace">
          <KeyValues
            items={[
              ['Captured', fmtDateTime(t.created_at)],
              ['Expires', `${fmtDateTime(t.expires_at)} (${fmtRelative(t.expires_at)})`],
              ['Size', <span className="num">{fmtCompact(t.size_bytes)} bytes {t.truncated && <Badge tone="warning">truncated to the policy limit</Badge>}</span>],
              ['Session', t.session_id ? <span className="mono">{t.session_id}</span> : <span className="muted">—</span>],
              ['Client binding', <span className="mono">{t.binding_id || '—'}</span>],
              ['Trace policy', <span className="mono">{t.policy_id}</span>],
            ]}
          />
        </Panel>
        <TraceSections sections={t.sections} />
      </div>
    </>
  )
}
