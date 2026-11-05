import { useState } from 'react'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Accordion, Anchor, Group, Paper, Select, Stack, Tabs, Text, Timeline, Title } from '@mantine/core'
import { CheckCircle2, FilePlus2, History, Pencil, Rocket, ShieldCheck, Trash2 } from 'lucide-react'
import { z } from 'zod'
import { Badge, Banner, Button, Skeleton, type Tone } from '@/components/ui'
import { ConfirmButton, MutationError } from '@/components/form'
import { QueryError } from '@/components/states'
import { ResourceBadges } from '@/components/ResourceBadges'
import { PlanDocumentEditor, defaultDocument } from '@/features/config/PlanDocumentEditor'
import { apiSend } from '@/lib/api/client'
import { effectiveQuery, planVersionsQuery, resourceListQuery, resourceQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { AccountRes, AliasRes, FieldErrorDTO, PlanDocument, PlanRes, PlanVersion, PoolRes, ProviderRes, ValidationReport } from '@/lib/api/types'
import { DecisionGraph } from '@/features/routing/DecisionGraph'
import { Simulator } from '@/features/routing/Simulator'
import { describeLocation, parseEngine } from '@/features/routing/engine'
import { type Lane, laneAccounts, planLanes } from '@/features/routing/graph'
import { fallbackSentence, orderedMembers, planSummary } from '@/features/routing/flow'
import { billingName } from '@/features/limits/LimitsPage'
import { useCan } from '@/lib/session'
import { fmtDateTime } from '@/lib/format'
import c from '@/features/config/config.module.css'

export const Route = createFileRoute('/routing/plans/$planId')({
  validateSearch: z.object({ tab: z.enum(['flow', 'versions', 'test']).optional().catch(undefined) }).parse,
  component: PlanDetail,
})

const statusTone: Record<string, Tone> = { draft: 'info', published: 'success', superseded: 'neutral', archived: 'neutral' }
const statusLabel: Record<string, string> = { draft: 'Draft', published: 'Live', superseded: 'Replaced', archived: 'Archived' }

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

/** One block per incoming model pattern: the accounts tried, in order. */
function Flow({ doc, lanes }: { doc: PlanDocument; lanes: Lane[] }) {
  const billings = new Set(lanes.flatMap((l) => l.members.map((m) => m.account?.spec.billing)).filter(Boolean))
  return (
    <Stack gap="md">
      {lanes.length === 0 && <Text c="dimmed">This version maps no models and has no default, so every request is rejected.</Text>}
      {lanes.map((l) => (
        <Paper key={l.key} withBorder radius="md" p="md">
          <Stack gap="xs">
            <Group justify="space-between" gap="xs">
              <Text fw={600}>
                {l.entry.kind === 'default' ? 'Any other model' : (
                  <>
                    Requests for <code>{l.entry.match}</code>
                  </>
                )}
              </Text>
              <Text size="xs" c="dimmed">
                {l.chain.length > 0 && <>via {l.chain.map((a) => a.name).join(' → ')}</>}
                {l.pool && <> · {l.pool.name}</>}
              </Text>
            </Group>
            {l.problem ? (
              <Banner tone="error">Rejected: {l.problem}.</Banner>
            ) : l.members.length === 0 ? (
              <Text size="sm" c="dimmed">
                The fallback group has no members.
              </Text>
            ) : (
              <ol className={c.flowList}>
                {orderedMembers(l).map((m) => (
                  <li key={m.accountId + m.model} data-blocked={m.blocked.length > 0 || undefined}>
                    <span>{m.account?.spec.display_name || m.account?.name || m.accountId}</span>
                    <span className="muted"> · {m.model}</span>
                    {billings.size > 1 && m.account && <span className="muted"> · {billingName[m.account.spec.billing] ?? m.account.spec.billing}</span>}
                    {m.blocked.length > 0 && <span className={c.flowBlocked}> · skipped: {m.blocked.join(', ')}</span>}
                  </li>
                ))}
              </ol>
            )}
            {l.required.length > 0 && (
              <Text size="xs" c="dimmed">
                Needs: {l.required.join(', ')}
              </Text>
            )}
          </Stack>
        </Paper>
      ))}
      <Text size="sm">{fallbackSentence(doc)}</Text>
    </Stack>
  )
}

function PlanDetail() {
  const { planId } = Route.useParams()
  const { tab } = Route.useSearch()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const plan = useQuery(resourceQuery<PlanRes>('routing-plans', planId))
  const versions = useQuery(planVersionsQuery(planId))
  const eff = useQuery(effectiveQuery)
  const cfg = useConfigMaps()
  const canWrite = useCan('config.write')
  const canPublish = useCan('policy.publish')
  const [shownId, setShownId] = useState<string | null>(null)
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
  const draft = items.find((v) => v.status === 'draft')
  const shown = items.find((v) => v.id === shownId) ?? published ?? draft ?? items[0]
  const lanes = shown && cfg.ready ? planLanes(shown.document, cfg.maps) : []
  const liveLanes = published && cfg.ready ? planLanes(published.document, cfg.maps) : []
  const allLanes = cfg.ready ? items.filter((v) => v.status === 'published' || v.status === 'draft').flatMap((v) => planLanes(v.document, cfg.maps)) : []
  const usedBy = eff.data?.plans.find((x) => x.id === p.id)?.bindings ?? 0
  const current = tab ?? 'flow'
  const accountName = (id: string) => {
    const a = cfg.maps.accounts[id]
    if (!a) return id
    const pr = cfg.maps.providers[a.spec.provider_id]
    return pr ? `${pr.name} / ${a.name}` : a.name
  }
  const firstAlias = lanes.find((l) => l.chain.length > 0)?.chain[0]?.name ?? ''
  const edit = (v: PlanVersion) => setEditing({ version: v, doc: v.document, note: v.note, errors: report?.id === v.id ? report.r.errors : storedErrors(v) })

  return (
    <Stack gap="lg">
      <Stack gap={4}>
        <Text size="sm">
          <Anchor component={Link} to="/routing/plans" c="dimmed">
            Routing
          </Anchor>{' '}
          <Text span c="dimmed">
            /
          </Text>
        </Text>
        <Group justify="space-between" align="flex-end" gap="sm" wrap="wrap">
          <Stack gap={0} miw={0}>
            <Group gap="sm" wrap="nowrap">
              <Title order={1}>{p.spec.display_name || p.name}</Title>
              {published ? <Badge tone="success">Live v{published.version}</Badge> : <Badge tone="warning">Not live</Badge>}
              {draft && <Badge tone="info">Draft v{draft.version}</Badge>}
              <ResourceBadges r={p} />
            </Group>
            <Text size="sm" c="dimmed">
              {p.spec.display_name && <span className="mono">{p.name} · </span>}
              Used by {usedBy} {usedBy === 1 ? 'client' : 'clients'}
            </Text>
          </Stack>
          <Group gap="xs">
            {draft && canWrite && (
              <Button icon={Pencil} onClick={() => edit(draft)}>
                Edit draft
              </Button>
            )}
            {draft && canPublish && (
              <ConfirmButton variant="primary" danger={false} icon={Rocket} busy={publish.isPending} title={`Publish v${draft.version}?`}
                message="It becomes active immediately for every client using this plan." confirmLabel="Publish" onConfirm={() => publish.mutate(draft.id)}>
                {`Publish v${draft.version}`}
              </ConfirmButton>
            )}
            {!draft && canWrite && (
              <Button variant="primary" icon={FilePlus2} onClick={() => setEditing({ doc: published?.document ?? defaultDocument(), note: '' })}>
                New draft
              </Button>
            )}
          </Group>
        </Group>
      </Stack>
      <Text size="md">{cfg.ready ? planSummary(published?.document, liveLanes) : ' '}</Text>
      {p.spec.description && (
        <Text size="sm" c="dimmed">
          {p.spec.description}
        </Text>
      )}
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

      <Tabs value={current} onChange={(v) => v && void navigate({ to: '/routing/plans/$planId', params: { planId }, search: { tab: v as 'flow' | 'versions' | 'test' }, replace: true })}>
        <Group justify="space-between" gap="sm" wrap="wrap" align="flex-end">
          <Tabs.List aria-label="Plan sections">
            <Tabs.Tab value="flow">Flow</Tabs.Tab>
            <Tabs.Tab value="versions">Versions</Tabs.Tab>
            <Tabs.Tab value="test">Test a request</Tabs.Tab>
          </Tabs.List>
          {items.length > 1 && current === 'flow' && (
            <Select
              aria-label="Version"
              size="xs"
              w={200}
              allowDeselect={false}
              value={shown?.id ?? null}
              onChange={setShownId}
              data={items.map((v) => ({ value: v.id, label: `v${v.version} · ${statusLabel[v.status] ?? v.status}` }))}
            />
          )}
        </Group>

        <Tabs.Panel value="flow" pt="md">
          {!shown ? (
            <Text c="dimmed">No versions yet. Create a draft to define where requests go.</Text>
          ) : !cfg.ready ? (
            <Skeleton height={120} />
          ) : (
            <Stack gap="md">
              <Flow doc={shown.document} lanes={lanes} />
              <Accordion variant="separated" radius="md">
                <Accordion.Item value="graph">
                  <Accordion.Control>Detailed graph</Accordion.Control>
                  <Accordion.Panel>
                    <section aria-label="Decision graph">
                      <DecisionGraph doc={shown.document} lanes={lanes} />
                    </section>
                  </Accordion.Panel>
                </Accordion.Item>
                <Accordion.Item value="json">
                  <Accordion.Control>View as JSON</Accordion.Control>
                  <Accordion.Panel>
                    <pre className={c.json}>{JSON.stringify(shown.document, null, 2)}</pre>
                  </Accordion.Panel>
                </Accordion.Item>
              </Accordion>
            </Stack>
          )}
        </Tabs.Panel>

        <Tabs.Panel value="versions" pt="md">
          {versions.isError ? (
            <QueryError error={versions.error} retry={() => void versions.refetch()} />
          ) : items.length === 0 ? (
            <Text c="dimmed">No versions yet.</Text>
          ) : (
            <Timeline bulletSize={20} lineWidth={2} active={-1}>
              {items.map((v) => (
                <Timeline.Item
                  key={v.id}
                  bullet={v.status === 'published' ? <CheckCircle2 size={12} /> : undefined}
                  title={
                    <Group gap="xs">
                      <Text fw={600}>v{v.version}</Text>
                      <Badge tone={statusTone[v.status]}>{statusLabel[v.status] ?? v.status}</Badge>
                      {v.rollback_of && <Badge>Rollback</Badge>}
                    </Group>
                  }
                >
                  <Stack gap={6} mt={4}>
                    {v.note && <Text size="sm">{v.note}</Text>}
                    <Text size="xs" c="dimmed">
                      Created {fmtDateTime(v.created_at)}
                      {v.published_at ? ` · published ${fmtDateTime(v.published_at)}` : ''}
                    </Text>
                    <Group gap="xs">
                      <Button size="small" variant="ghost" icon={ShieldCheck} busy={validate.isPending && validate.variables === v.id} onClick={() => validate.mutate(v.id)}>
                        Validate
                      </Button>
                      {v.status === 'draft' && canWrite && (
                        <>
                          <Button size="small" variant="ghost" icon={Pencil} onClick={() => edit(v)}>
                            Edit
                          </Button>
                          <ConfirmButton size="small" icon={Trash2} label={`Discard draft v${v.version}`} title={`Discard draft v${v.version}?`}
                            message="The draft and its changes are deleted." confirmLabel="Discard" onConfirm={() => discard.mutate(v.id)} />
                        </>
                      )}
                      {v.status === 'superseded' && canPublish && (
                        <ConfirmButton size="small" danger={false} icon={History} busy={rollback.isPending} title={`Roll back to v${v.version}?`}
                          message="A new version copying it is published and becomes active immediately." confirmLabel="Roll back" onConfirm={() => rollback.mutate(v.id)}>
                          Roll back
                        </ConfirmButton>
                      )}
                    </Group>
                  </Stack>
                </Timeline.Item>
              ))}
            </Timeline>
          )}
        </Tabs.Panel>

        <Tabs.Panel value="test" pt="md">
          {items.length === 0 ? (
            <Text c="dimmed">Create a draft first; drafts can be tested before publishing.</Text>
          ) : cfg.ready ? (
            <section aria-label="Simulator">
              <Simulator key={planId} planId={planId} versions={items} accounts={laneAccounts(allLanes)} defaultModel={firstAlias} accountName={accountName} />
            </section>
          ) : (
            <Skeleton height={120} />
          )}
        </Tabs.Panel>
      </Tabs>

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
    </Stack>
  )
}
