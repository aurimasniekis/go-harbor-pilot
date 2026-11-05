import { useState } from 'react'
import { createFileRoute, Link } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CheckCircle2, FilePlus2, History, Pencil, Rocket, ShieldCheck, Trash2 } from 'lucide-react'
import { Badge, Banner, Button, KeyValues, Panel, Skeleton, TableScroll, type Tone } from '@/components/ui'
import { MutationError, SelectField, ConfirmButton } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { ResourceBadges } from '@/components/ResourceBadges'
import { PlanDocumentEditor, defaultDocument } from '@/features/config/PlanDocumentEditor'
import { apiSend } from '@/lib/api/client'
import { planVersionsQuery, resourceListQuery, resourceQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { AccountRes, AliasRes, FieldErrorDTO, PlanDocument, PlanRes, PlanVersion, PoolRes, ProviderRes, ValidationReport } from '@/lib/api/types'
import { DecisionGraph } from '@/features/routing/DecisionGraph'
import { Simulator } from '@/features/routing/Simulator'
import { describeLocation, parseEngine } from '@/features/routing/engine'
import { laneAccounts, planLanes } from '@/features/routing/graph'
import { useCan } from '@/lib/session'
import { fmtDateTime } from '@/lib/format'
import c from '@/features/config/config.module.css'

export const Route = createFileRoute('/routing/plans/$planId')({
  component: PlanDetail,
})

const statusTone: Record<string, Tone> = { draft: 'info', published: 'success', superseded: 'neutral', archived: 'neutral' }

/** Errors from a version's stored validation report, if any. */
function storedErrors(v: PlanVersion): FieldErrorDTO[] {
  const rep = v.validation as Partial<ValidationReport> | null | undefined
  return rep && Array.isArray(rep.errors) ? rep.errors : []
}

function useConfigMaps() {
  const aliases = useQuery(resourceListQuery<AliasRes>('model-aliases'))
  const pools = useQuery(resourceListQuery<PoolRes>('pools'))
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const providers = useQuery(resourceListQuery<ProviderRes>('providers'))
  return {
    ready: Boolean(aliases.data && pools.data && accounts.data && providers.data),
    maps: {
      aliases: byId(aliases.data?.items),
      pools: byId(pools.data?.items),
      accounts: byId(accounts.data?.items),
      providers: byId(providers.data?.items),
    },
  }
}

/** Read-only route picture plus the simulator, for a chosen version. */
function Explore({ planId, items }: { planId: string; items: PlanVersion[] }) {
  const cfg = useConfigMaps()
  const published = items.find((v) => v.status === 'published')
  const [graphId, setGraphId] = useState('')
  const shown = items.find((v) => v.id === graphId) ?? published ?? items.find((v) => v.status === 'draft') ?? items[0]
  if (!shown) return null
  const lanes = cfg.ready ? planLanes(shown.document, cfg.maps) : []
  const allLanes = cfg.ready ? items.filter((v) => v.status === 'published' || v.status === 'draft').flatMap((v) => planLanes(v.document, cfg.maps)) : []
  const accountName = (id: string) => {
    const a = cfg.maps.accounts[id]
    if (!a) return id
    const p = cfg.maps.providers[a.spec.provider_id]
    return p ? `${p.name} / ${a.name}` : a.name
  }
  const firstAlias = lanes.find((l) => l.chain.length > 0)?.chain[0]?.name ?? ''
  return (
    <>
      <Panel
        title="Decision graph"
        subtitle="How requests flow through this version: requested model → alias → pool → members, then rule stages and fallback. Read-only."
        actions={
          <SelectField label="Version" value={shown.id} onChange={setGraphId}
            options={items.map((v) => ({ value: v.id, label: `v${v.version} (${v.status})` }))} />
        }
      >
        {cfg.ready ? <DecisionGraph doc={shown.document} lanes={lanes} /> : <Skeleton height={120} />}
      </Panel>
      <Panel
        title="Simulator"
        subtitle="Route a synthetic request through a version without contacting any upstream. Drafts can be simulated before publishing."
      >
        {cfg.ready ? (
          <Simulator key={planId} planId={planId} versions={items} accounts={laneAccounts(allLanes)} defaultModel={firstAlias} accountName={accountName} />
        ) : (
          <Skeleton height={120} />
        )}
      </Panel>
    </>
  )
}

function PlanDetail() {
  const { planId } = Route.useParams()
  const qc = useQueryClient()
  const plan = useQuery(resourceQuery<PlanRes>('routing-plans', planId))
  const versions = useQuery(planVersionsQuery(planId))
  const canWrite = useCan('config.write')
  const canPublish = useCan('policy.publish')
  const [editing, setEditing] = useState<{ version?: PlanVersion; doc: PlanDocument; note: string; errors?: FieldErrorDTO[] } | null>(null)
  const [report, setReport] = useState<{ id: string; r: ValidationReport } | null>(null)
  const invalidate = () => void qc.invalidateQueries({ queryKey: ['resources'] })
  const base = `/routing-plans/${encodeURIComponent(planId)}/versions`

  const saveDraft = useMutation({
    mutationFn: ({ version, doc, note }: { version?: PlanVersion; doc: PlanDocument; note: string }) =>
      version ? apiSend<PlanVersion>('PUT', `${base}/${version.id}`, { document: doc, note }) : apiSend<PlanVersion>('POST', base, { document: doc, note }),
    onSuccess: () => {
      setEditing(null)
      invalidate()
    },
  })
  const validate = useMutation({
    mutationFn: (id: string) => apiSend<ValidationReport>('POST', `${base}/${id}/validate`),
    onSuccess: (r, id) => setReport({ id, r }),
  })
  const publish = useMutation({ mutationFn: (id: string) => apiSend<PlanVersion>('POST', `${base}/${id}/publish`), onSuccess: invalidate })
  const discard = useMutation({ mutationFn: (id: string) => apiSend<void>('DELETE', `${base}/${id}`), onSuccess: invalidate })
  const rollback = useMutation({
    mutationFn: (id: string) => apiSend<PlanVersion>('POST', `/routing-plans/${encodeURIComponent(planId)}/rollback`, { version_id: id }),
    onSuccess: invalidate,
  })

  if (plan.isError) return <QueryError error={plan.error} retry={() => void plan.refetch()} />
  if (!plan.data) return <Skeleton height={120} />
  const p = plan.data
  const items = versions.data?.items ?? []
  const published = items.find((v) => v.status === 'published')
  const hasDraft = items.some((v) => v.status === 'draft')

  return (
    <>
      <p>
        <Link to="/routing/plans">← All plans</Link>
      </p>
      <Panel title={<span className="mono">{p.name}</span>} subtitle={p.spec.description || undefined} actions={<ResourceBadges r={p} />}>
        <KeyValues
          items={[
            ['Display name', p.spec.display_name || '—'],
            ['Published version', published ? <Badge tone="success">v{published.version}</Badge> : <Badge tone="warning">none — requests using this plan are rejected</Badge>],
            ['Plan id', <span className="mono">{p.id}</span>],
          ]}
        />
      </Panel>
      <Panel
        title="Versions"
        subtitle="Drafts never affect traffic. Publishing supersedes the current version; rollback publishes a copy of an older version."
        actions={
          canWrite && (
            <Button variant="primary" icon={FilePlus2} onClick={() => setEditing({ doc: published?.document ?? defaultDocument(), note: '' })}>
              New draft
            </Button>
          )
        }
      >
        {hasDraft && <Banner tone="info">A draft exists. Validate it, then publish to make it active.</Banner>}
        <MutationError error={validate.error ?? publish.error ?? discard.error ?? rollback.error} />
        {report && (
          <Banner tone={report.r.valid ? 'info' : 'error'} icon={report.r.valid ? CheckCircle2 : undefined} role="status">
            <strong>{report.r.valid ? 'Valid.' : 'Invalid.'}</strong> Checked against configuration revision {report.r.config_revision}.
            {report.r.errors.length > 0 && (
              <ul>
                {report.r.errors.map((e, i) => (
                  <li key={i}>
                    <span className="mono">{describeLocation(e.location, parseEngine(items.find((v) => v.id === report.id)?.document.engine))}</span>: {e.message}
                  </li>
                ))}
              </ul>
            )}
            {report.r.warnings.length > 0 && (
              <ul>
                {report.r.warnings.map((e, i) => (
                  <li key={i}>Warning: {e.message}</li>
                ))}
              </ul>
            )}
          </Banner>
        )}
        {versions.isError ? (
          <QueryError error={versions.error} retry={() => void versions.refetch()} />
        ) : !versions.data ? (
          <TableSkeleton rows={3} />
        ) : items.length === 0 ? (
          <p className="secondary">No versions yet. Create a draft to define how this plan routes requests.</p>
        ) : (
          <TableScroll label="Plan versions">
            <table className="data-table" style={{ width: '100%' }}>
              <thead>
                <tr>
                  <th>Version</th>
                  <th>Status</th>
                  <th>Note</th>
                  <th>Created</th>
                  <th>Published</th>
                  <th aria-label="Actions" />
                </tr>
              </thead>
              <tbody>
                {items.map((v) => (
                  <tr key={v.id}>
                    <td className="num">v{v.version}</td>
                    <td>
                      <Badge tone={statusTone[v.status]}>{v.status}</Badge>
                      {v.rollback_of && <Badge>rollback</Badge>}
                    </td>
                    <td>{v.note || <span className="muted">—</span>}</td>
                    <td>{fmtDateTime(v.created_at)}</td>
                    <td>{v.published_at ? fmtDateTime(v.published_at) : '—'}</td>
                    <td className={c.cellActions}>
                      <Button size="small" variant="ghost" icon={ShieldCheck} busy={validate.isPending && validate.variables === v.id} onClick={() => validate.mutate(v.id)}>
                        Validate
                      </Button>
                      {v.status === 'draft' && canWrite && (
                        <>
                          <Button size="small" variant="ghost" icon={Pencil}
                            onClick={() => setEditing({ version: v, doc: v.document, note: v.note, errors: report?.id === v.id ? report.r.errors : storedErrors(v) })}>
                            Edit
                          </Button>
                          <ConfirmButton size="small" icon={Trash2} label={`Discard draft v${v.version}`} title={`Discard draft v${v.version}?`}
                            message="The draft and its changes are deleted." confirmLabel="Discard" onConfirm={() => discard.mutate(v.id)} />
                        </>
                      )}
                      {v.status === 'draft' && canPublish && (
                        <ConfirmButton size="small" variant="primary" danger={false} icon={Rocket} busy={publish.isPending} title={`Publish v${v.version}?`}
                          message="It becomes active immediately for every client using this plan." confirmLabel="Publish" onConfirm={() => publish.mutate(v.id)}>
                          Publish
                        </ConfirmButton>
                      )}
                      {v.status === 'superseded' && canPublish && (
                        <ConfirmButton size="small" danger={false} icon={History} busy={rollback.isPending} title={`Roll back to v${v.version}?`}
                          message="A new version copying it is published and becomes active immediately." confirmLabel="Roll back" onConfirm={() => rollback.mutate(v.id)}>
                          Roll back
                        </ConfirmButton>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </TableScroll>
        )}
        {published && (
          <details>
            <summary>Published document (v{published.version})</summary>
            <pre className={c.json}>{JSON.stringify(published.document, null, 2)}</pre>
          </details>
        )}
      </Panel>
      {items.length > 0 && <Explore planId={planId} items={items} />}
      {editing && (
        <PlanDocumentEditor
          title={editing.version ? `Edit draft v${editing.version.version}` : 'New draft'}
          initial={editing.doc}
          note={editing.note}
          validationErrors={editing.errors}
          busy={saveDraft.isPending}
          error={saveDraft.error}
          onSave={(doc, note) => saveDraft.mutate({ version: editing.version, doc, note })}
          onClose={() => {
            saveDraft.reset()
            setEditing(null)
          }}
        />
      )}
    </>
  )
}
