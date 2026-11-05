import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Compass, Download, ShieldAlert, Trash2 } from 'lucide-react'
import { Badge, Banner, Button, EmptyState, KeyValues, Panel, Skeleton } from '@/components/ui'
import { ConfirmButton, MutationError } from '@/components/form'
import { Group, Tooltip } from '@mantine/core'
import { QueryError } from '@/components/states'
import { fmtSize, GrantNeeded, TraceModeBadge } from '@/features/traces/bits'
import { TraceSections } from '@/features/traces/TraceSections'
import { accountLabel } from '@/features/routing/names'
import p from '@/features/privacy/privacy.module.css'
import { API, ApiError, apiSend } from '@/lib/api/client'
import { resourceListQuery, traceQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { AccountRes, BindingRes, PlanRes, ProviderRes, TracePolicyRes } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { fmtDateTime, fmtRelative } from '@/lib/format'
import { PageHeader } from '@/ui/kit'

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
  const exp = useMutation({ mutationFn: () => downloadExport(traceId) })
  const del = useMutation({
    mutationFn: () => apiSend<void>('DELETE', `/traces/${encodeURIComponent(traceId)}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['traces'] })
      void navigate({ to: '/traces' })
    },
  })

  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const providers = useQuery(resourceListQuery<ProviderRes>('providers'))
  const bindings = useQuery(resourceListQuery<BindingRes>('client-bindings'))
  const plans = useQuery(resourceListQuery<PlanRes>('routing-plans'))
  const rules = useQuery(resourceListQuery<TracePolicyRes>('trace-policies'))
  const acc = byId(accounts.data?.items)
  const prov = byId(providers.data?.items)
  const accountName = (id: string) => accountLabel(acc[id], prov, id)
  const crumbs = [{ label: 'Privacy', to: '/privacy' }, { label: 'Captured content', to: '/traces' }, { label: 'Capture' }]
  const back = <PageHeader title="Captured content" crumbs={crumbs} />
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
            <EmptyState icon={Compass} title="Captured content not found or expired">
              Expired captures are unreadable immediately and are erased shortly after, together with their search entries and keys.
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
  const client = bindings.data?.items?.find((x) => x.id === t.binding_id)
  const plan = plans.data?.items?.find((x) => x.id === t.plan_id)
  const rule = rules.data?.items?.find((x) => x.id === t.policy_id)
  return (
    <>
      <PageHeader
        crumbs={crumbs.slice(0, 2)}
        title={`Captured ${fmtDateTime(t.created_at)}`}
        context={
          <Group gap={12} wrap="wrap" component="span">
            <TraceModeBadge mode={t.mode} />
            <Link to="/activity/$requestId" params={{ requestId: t.request_id }}>
              Open the request
            </Link>
            <span title={fmtDateTime(t.expires_at)}>Erased {fmtRelative(t.expires_at)}</span>
          </Group>
        }
        actions={
          <Group gap={8} wrap="wrap">
            {canExport && (
              <Tooltip label="This capture rule doesn’t allow downloads." disabled={t.allow_export}>
                <span>
                  <Button icon={Download} busy={exp.isPending} disabled={!t.allow_export} onClick={() => exp.mutate()}>
                    Download
                  </Button>
                </span>
              </Tooltip>
            )}
            {canDelete && (
              <ConfirmButton variant="default" icon={Trash2} busy={del.isPending} title="Delete this captured content?"
                message="Its content and search entries are erased now instead of at expiry. Request details stay in Requests." confirmLabel="Delete"
                onConfirm={() => del.mutate()}>
                Delete
              </ConfirmButton>
            )}
          </Group>
        }
      />
      <div className={p.stack}>
        <Banner tone="info" icon={ShieldAlert}>
          Reading, downloading and deleting this content is recorded in the audit log. It is erased {fmtDateTime(t.expires_at)}.
        </Banner>
        {t.mode === 'unredacted' && <Banner tone="warning">This capture holds unredacted content, which may include secrets the client sent.</Banner>}
        <MutationError error={exp.error} />
        <MutationError error={del.error} />
        <Panel title="Details">
          <KeyValues
            items={[
              ['Client', client ? <Link to="/clients/$bindingId" params={{ bindingId: client.id }}>{client.spec.display_name || client.name}</Link> : <span className="muted">Unknown client</span>],
              ['Plan', plan ? <Link to="/routing/plans/$planId" params={{ planId: plan.id }}>{plan.spec.display_name || plan.name}</Link> : <span className="muted">—</span>],
              ['Capture rule', rule ? <Link to="/privacy/trace-policies">{rule.name}</Link> : <span className="muted">—</span>],
              ['Size', <span className="num">{fmtSize(t.size_bytes)} {t.truncated && <Badge tone="warning">Shortened to the rule’s limit</Badge>}</span>],
              ['Session', t.session_id ? <span className="mono">{t.session_id}</span> : <span className="muted">—</span>],
            ]}
          />
        </Panel>
        <TraceSections sections={t.sections} accountName={accountName} />
      </div>
    </>
  )
}
