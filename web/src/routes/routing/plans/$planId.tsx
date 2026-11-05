import { useEffect, useState } from 'react'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Accordion, Alert, Anchor, Checkbox as MCheckbox, Group, List, Menu, Paper, SegmentedControl, Select, Stack, Tabs, Text, Timeline, Tooltip, ActionIcon } from '@mantine/core'
import { AlertTriangle, CheckCircle2, Eye, FilePlus2, GitCompare, History, MoreHorizontal, Pencil, Rocket, ShieldAlert, ShieldCheck, Trash2 } from 'lucide-react'
import { z } from 'zod'
import { Badge, Banner, Button, Skeleton, type Tone } from '@/components/ui'
import { ConfirmButton, Dialog, DialogActions, MutationError } from '@/components/form'
import { QueryError } from '@/components/states'
import { ResourceBadges } from '@/components/ResourceBadges'
import { PageHeader } from '@/ui/kit'
import { PlanDocumentEditor, defaultDocument } from '@/features/config/PlanDocumentEditor'
import { PlanDialog } from '@/features/config/PlanDialog'
import { apiSend } from '@/lib/api/client'
import { effectiveQuery, planVersionsQuery, resourceListQuery, resourceQuery, usersQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type {
  AccountRes, AliasRes, FieldErrorDTO, PlanDocument, PlanRes, PlanVersion, PoolRes, PrivacyPolicyRes, ProviderRes, TracePolicyRes, ValidationReport,
} from '@/lib/api/types'
import { DecisionGraph } from '@/features/routing/DecisionGraph'
import { Simulator } from '@/features/routing/Simulator'
import { describeLocation, parseEngine } from '@/features/routing/engine'
import { type Lane, laneAccounts, planLanes } from '@/features/routing/graph'
import { docDiff, engineEffects, engineHazards, fallbackSentence, orderedMembers, planSummary, sameDocument } from '@/features/routing/flow'
import { accountLabel } from '@/features/routing/names'
import { traceModes } from '@/features/privacy/privacy'
import { billingName } from '@/features/limits/LimitsPage'
import { useCan } from '@/lib/session'
import { fmtDateTime } from '@/lib/format'
import { plural } from '@/ui/glossary'
import { ApiError } from '@/lib/api/client'
import c from '@/features/config/config.module.css'

export const Route = createFileRoute('/routing/plans/$planId')({
  validateSearch: z.object({ tab: z.enum(['flow', 'versions', 'test']).optional().catch(undefined) }).parse,
  component: PlanDetail,
})

const statusTone: Record<string, Tone> = { draft: 'info', published: 'success', superseded: 'neutral', archived: 'neutral' }
const statusLabel: Record<string, string> = { draft: 'Draft', published: 'Live', superseded: 'Replaced', archived: 'Archived' }

/** Errors and warnings from a version's stored validation report, if any. */
function storedReport(v: PlanVersion): Partial<ValidationReport> {
  const rep = v.validation as Partial<ValidationReport> | null | undefined
  return rep && typeof rep === 'object' ? rep : {}
}
const storedErrors = (v: PlanVersion): FieldErrorDTO[] => (Array.isArray(storedReport(v).errors) ? (storedReport(v).errors as FieldErrorDTO[]) : [])

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

const actionWords: Record<string, string> = { block: 'Blocks', require: 'Requires', set_intent: 'Sets intent', exclude: 'Excludes', boost: 'Re-ranks' }

/** Rules and decision-table rows that act before routing, in plain words. */
function BeforeRouting({ doc }: { doc: PlanDocument }) {
  const effects = engineEffects(doc)
  if (effects.length === 0) return null
  return (
    <Paper withBorder radius="md" p="md">
      <Stack gap={6}>
        <Text fw={600}>Rules</Text>
        <List size="sm" spacing={4}>
          {effects.map((e) => (
            <List.Item key={e.label}>
              <Group gap={6} wrap="wrap">
                <Badge tone={e.action === 'block' ? 'error' : e.action === 'exclude' ? 'warning' : 'info'}>{actionWords[e.action] ?? e.action}</Badge>
                <span>
                  {e.always ? 'every request' : e.stage === 'request' ? 'matching requests' : 'matching accounts'} · {e.label}
                  {e.reason ? ` · “${e.reason}”` : ''}
                </span>
              </Group>
            </List.Item>
          ))}
        </List>
      </Stack>
    </Paper>
  )
}

/** One block per incoming model pattern: the accounts tried, in order. */
function Flow({ doc, lanes }: { doc: PlanDocument; lanes: Lane[] }) {
  const billings = new Set(lanes.flatMap((l) => l.members.map((m) => m.account?.spec.billing)).filter(Boolean))
  return (
    <Stack gap="md">
      <BeforeRouting doc={doc} />
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
                {l.chain.length > 0 && (
                  <>
                    Model name{' '}
                    <Anchor component={Link} to="/routing" size="xs">
                      {l.chain.map((a) => a.name).join(' → ')}
                    </Anchor>
                  </>
                )}
                {l.pool && (
                  <>
                    {' '}
                    → fallback group{' '}
                    <Anchor component={Link} to="/routing/pools" size="xs">
                      {l.pool.spec.display_name || l.pool.name}
                    </Anchor>
                  </>
                )}
              </Text>
            </Group>
            {l.problem ? (
              <Banner tone="error">Rejected: {l.problem}.</Banner>
            ) : l.members.length === 0 ? (
              <Text size="sm" c="dimmed">
                The fallback group has no accounts, so these requests are rejected.
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

/** Masking, capture, restrictions and rules at a glance, with links. */
function PoliciesStrip({ doc }: { doc: PlanDocument }) {
  const privacy = useQuery(resourceListQuery<PrivacyPolicyRes>('privacy-policies'))
  const traces = useQuery(resourceListQuery<TracePolicyRes>('trace-policies'))
  const mask = (privacy.data?.items ?? []).find((p) => p.id === doc.privacy?.policy_id)
  const capture = (traces.data?.items ?? []).find((t) => t.id === doc.privacy?.trace_policy_id)
  const r = doc.restrictions
  const restrictions: string[] = []
  if (r.allowed_provider_ids.length) restrictions.push(`only ${plural(r.allowed_provider_ids.length, 'provider')}`)
  if (r.denied_provider_ids.length) restrictions.push(`excludes ${plural(r.denied_provider_ids.length, 'provider')}`)
  if (r.allowed_billing.length) restrictions.push(`billing: ${r.allowed_billing.map((b) => billingName[b] ?? b).join(', ')}`)
  if (r.allowed_classifications.length) restrictions.push(`kind: ${r.allowed_classifications.join(', ')}`)
  if (r.required_capabilities.length) restrictions.push(`needs ${r.required_capabilities.join(', ')}`)
  const e = parseEngine(doc.engine)
  const on = e.rules.filter((x) => x.enabled).length + e.tables.filter((x) => x.enabled).length
  const modeLabel = capture ? (traceModes.find((m) => m.value === capture.spec.mode)?.label ?? capture.spec.mode) : ''
  return (
    <Group gap="xs" wrap="wrap" aria-label="Policies">
      <Text size="sm" c="dimmed">
        Masking:{' '}
        {mask ? (
          <Anchor component={Link} to="/privacy" size="sm">
            {mask.name}
          </Anchor>
        ) : (
          'none'
        )}
      </Text>
      <Text size="sm" c="dimmed">
        · Content capture:{' '}
        {capture ? (
          <Anchor component={Link} to="/privacy/trace-policies" size="sm">
            {capture.name} ({modeLabel}, {capture.spec.retention})
          </Anchor>
        ) : (
          'off'
        )}
      </Text>
      <Text size="sm" c="dimmed">
        · Restrictions: {restrictions.length ? restrictions.join('; ') : 'none'}
      </Text>
      <Text size="sm" c="dimmed">
        · {on === 0 ? 'No rules' : plural(on, 'rule or table', 'rules and tables')}
      </Text>
    </Group>
  )
}

/** Validation result in words, scoped to one version. */
function ReportView({ v, r }: { v: PlanVersion; r: ValidationReport }) {
  const engine = parseEngine(v.document.engine)
  const blocking = r.warnings.filter((w) => /blocks (every|most) request/.test(w.message))
  const other = r.warnings.filter((w) => !blocking.includes(w))
  if (r.valid && r.warnings.length === 0) {
    return (
      <Text size="sm" c="teal">
        <CheckCircle2 size={14} style={{ verticalAlign: '-2px' }} /> v{v.version} checked {fmtDateTime(r.checked_at)}: no problems found.
      </Text>
    )
  }
  return (
    <Stack gap={4}>
      {!r.valid && (
        <Text size="sm" c="red" fw={600}>
          v{v.version} can't be published until these are fixed:
        </Text>
      )}
      <List size="sm" spacing={2}>
        {r.errors.map((e, i) => (
          <List.Item key={`e${i}`}>
            <strong>{describeLocation(e.location, engine)}:</strong> {e.message}
          </List.Item>
        ))}
        {blocking.map((w, i) => (
          <List.Item key={`b${i}`}>
            <Text span c="red" fw={600} size="sm">
              This version {w.message}.
            </Text>
          </List.Item>
        ))}
        {other.map((w, i) => (
          <List.Item key={`w${i}`}>Warning: {w.message}</List.Item>
        ))}
      </List>
    </Stack>
  )
}

/** Confirmation before publishing or rolling back: what changes, problems, who is affected. */
function ReleaseDialog({ open, title, target, live, usedBy, aliasName, busy, error, confirmLabel, onConfirm, onClose, validate }: {
  open: boolean
  title: string
  target: PlanVersion
  live?: PlanVersion
  usedBy: number
  aliasName: (id: string) => string
  busy: boolean
  error: unknown
  confirmLabel: string
  onConfirm: () => void
  onClose: () => void
  validate: (id: string) => Promise<ValidationReport>
}) {
  const [report, setReport] = useState<ValidationReport | null>(null)
  const [ack, setAck] = useState(false)
  useEffect(() => {
    if (!open) return
    setReport(null)
    setAck(false)
    void validate(target.id).then(setReport, () => setReport(null))
  }, [open, target.id]) // eslint-disable-line react-hooks/exhaustive-deps
  const changes = docDiff(live?.document, target.document, aliasName)
  const hazards = engineHazards(target.document)
  const severe = hazards.length > 0 || (report?.warnings ?? []).some((w) => /blocks (every|most) request/.test(w.message))
  const invalid = report ? !report.valid : false
  return (
    <Dialog open={open} title={title} onClose={onClose} dirty={false}>
      <Text size="sm">
        It becomes active immediately for {plural(usedBy, 'client')} using this plan.
      </Text>
      <Stack gap={4}>
        <Text size="sm" fw={600}>
          Changes from {live ? `live v${live.version}` : 'nothing (first version)'}
        </Text>
        {changes.length === 0 ? (
          <Text size="sm" c="dimmed">
            No routing changes.
          </Text>
        ) : (
          <List size="sm" spacing={2}>
            {changes.map((x) => (
              <List.Item key={x}>{x}</List.Item>
            ))}
          </List>
        )}
      </Stack>
      {hazards.length > 0 && (
        <Alert color="red" icon={<ShieldAlert size={16} />} title="This version blocks traffic">
          <List size="sm">
            {hazards.map((h) => (
              <List.Item key={h}>{h}</List.Item>
            ))}
          </List>
        </Alert>
      )}
      {report ? <ReportView v={target} r={report} /> : <Text size="sm" c="dimmed">Checking the version…</Text>}
      {severe && !invalid && (
        <MCheckbox label="I understand that clients on this plan will have requests refused." checked={ack} onChange={(e) => setAck(e.currentTarget.checked)} />
      )}
      <MutationError error={error} />
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant={severe ? 'danger' : 'primary'} icon={Rocket} busy={busy} disabled={!report || invalid || (severe && !ack)} onClick={onConfirm}>
          {confirmLabel}
        </Button>
      </DialogActions>
    </Dialog>
  )
}

/** Plain-language differences between a version and live. */
function CompareDialog({ v, live, aliasName, onClose }: { v: PlanVersion; live?: PlanVersion; aliasName: (id: string) => string; onClose: () => void }) {
  const changes = live && live.id !== v.id ? docDiff(live.document, v.document, aliasName) : []
  return (
    <Dialog open title={`v${v.version} compared with ${live ? `live v${live.version}` : 'nothing live'}`} onClose={onClose} dirty={false}>
      {live?.id === v.id ? (
        <Text size="sm">This is the live version.</Text>
      ) : changes.length === 0 ? (
        <Text size="sm">Routes exactly like the live version.</Text>
      ) : (
        <List size="sm" spacing={4}>
          {changes.map((x) => (
            <List.Item key={x}>{x}</List.Item>
          ))}
        </List>
      )}
      {engineHazards(v.document).map((h) => (
        <Banner key={h} tone="error">
          {h}
        </Banner>
      ))}
      <DialogActions>
        <Button onClick={onClose}>Close</Button>
      </DialogActions>
    </Dialog>
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
  const canUsers = useCan('users.manage')
  const users = useQuery({ ...usersQuery, enabled: canUsers })
  const [shownId, setShownId] = useState<string | null>(null)
  const [editing, setEditing] = useState<{ version?: PlanVersion; doc: PlanDocument; note: string; errors?: FieldErrorDTO[] } | null>(null)
  const [report, setReport] = useState<{ id: string; r: ValidationReport } | null>(null)
  const [releasing, setReleasing] = useState<{ kind: 'publish' | 'rollback'; v: PlanVersion } | null>(null)
  const [comparing, setComparing] = useState<PlanVersion | null>(null)
  const [editingPlan, setEditingPlan] = useState(false)
  const invalidate = () => void qc.invalidateQueries({ queryKey: ['resources'] })
  const base = `/routing-plans/${encodeURIComponent(planId)}/versions`

  const saveDraft = useMutation({
    mutationFn: ({ version, doc, note }: { version?: PlanVersion; doc: PlanDocument; note: string }) =>
      version ? apiSend<PlanVersion>('PUT', `${base}/${version.id}`, { document: doc, note }) : apiSend<PlanVersion>('POST', base, { document: doc, note }),
    onSuccess: (v) => {
      setEditing(null)
      setShownId(v.id)
      setReport(null)
      invalidate()
    },
  })
  const runValidate = (id: string) => apiSend<ValidationReport>('POST', `${base}/${id}/validate`)
  const validate = useMutation({ mutationFn: runValidate, onSuccess: (r, id) => setReport({ id, r }) })
  const publish = useMutation({
    mutationFn: (id: string) => apiSend<PlanVersion>('POST', `${base}/${id}/publish`),
    onSuccess: () => {
      setReleasing(null)
      setShownId(null)
      invalidate()
    },
  })
  const discard = useMutation({ mutationFn: (id: string) => apiSend<void>('DELETE', `${base}/${id}`), onSuccess: () => {
    setShownId(null)
    invalidate()
  } })
  const rollback = useMutation({
    mutationFn: (id: string) => apiSend<PlanVersion>('POST', `/routing-plans/${encodeURIComponent(planId)}/rollback`, { version_id: id }),
    onSuccess: () => {
      setReleasing(null)
      setShownId(null)
      invalidate()
    },
  })
  const remove = useMutation({
    mutationFn: () => apiSend<void>('DELETE', `/routing-plans/${encodeURIComponent(planId)}`),
    onSuccess: () => {
      invalidate()
      void navigate({ to: '/routing/plans' })
    },
  })

  const crumbs = [{ label: 'Routing', to: '/routing' }, { label: 'Plans', to: '/routing/plans' }]
  if (plan.isError) {
    const missing = plan.error instanceof ApiError && plan.error.status === 404
    return (
      <Stack gap="lg">
        <PageHeader title={missing ? 'Plan not found' : 'Routing plan'} crumbs={[...crumbs, { label: missing ? 'Not found' : 'Plan' }]} />
        {missing ? (
          <Stack gap="sm" align="flex-start">
            <Text>This plan doesn't exist. It may have been deleted.</Text>
            <Anchor component={Link} to="/routing/plans">
              Back to plans
            </Anchor>
          </Stack>
        ) : (
          <QueryError error={plan.error} retry={() => void plan.refetch()} />
        )}
      </Stack>
    )
  }
  if (!plan.data) return <Skeleton height={120} />
  const p = plan.data
  const items = versions.data?.items ?? []
  const published = items.find((v) => v.status === 'published')
  const draft = items.find((v) => v.status === 'draft')
  // Show the draft by default when there is one: it is what the person is working on.
  const shown = items.find((v) => v.id === shownId) ?? draft ?? published ?? items[0]
  const lanes = shown && cfg.ready ? planLanes(shown.document, cfg.maps) : []
  const liveLanes = published && cfg.ready ? planLanes(published.document, cfg.maps) : []
  const allLanes = cfg.ready ? items.filter((v) => v.status === 'published' || v.status === 'draft').flatMap((v) => planLanes(v.document, cfg.maps)) : []
  const usedBy = eff.data?.plans.find((x) => x.id === p.id)?.bindings ?? 0
  const current = tab ?? 'flow'
  const accountName = (id: string) => accountLabel(cfg.maps.accounts[id], cfg.maps.providers, id)
  const aliasName = (id: string) => cfg.maps.aliases[id]?.name ?? (id || 'nothing')
  const userName = (id: string) => (users.data?.items ?? []).find((u) => u.id === id)?.display_name ?? ''
  const firstAlias = lanes.find((l) => l.chain.length > 0)?.chain[0]?.name ?? (shown?.document.model_mappings[0]?.match ?? '')
  const edit = (v: PlanVersion) => setEditing({ version: v, doc: v.document, note: v.note, errors: report?.id === v.id ? report.r.errors : storedErrors(v) })
  const draftChanges = draft ? docDiff(published?.document, draft.document, aliasName) : []
  const draftUnchanged = draft && published ? sameDocument(draft.document, published.document) : false
  const draftReport = draft && report?.id === draft.id ? report.r : null
  // Once the server check confirms it, its warning replaces the local one.
  const confirmed = (draftReport?.warnings ?? []).some((w) => /blocks every request/.test(w.message))
  const draftHazards = draft && !confirmed ? engineHazards(draft.document) : []

  return (
    <Stack gap="lg">
      <PageHeader
        crumbs={[...crumbs, { label: p.spec.display_name || p.name }]}
        title={
          <Group gap="sm" wrap="wrap" component="span">
            <span>{p.spec.display_name || p.name}</span>
            {published ? <Badge tone="success">Live v{published.version}</Badge> : <Badge tone="warning">Not live</Badge>}
            {draft && <Badge tone="info">Draft v{draft.version}</Badge>}
            <ResourceBadges r={p} />
          </Group>
        }
        context={
          <>
            {p.spec.display_name && <span className="mono">{p.name} · </span>}
            <Anchor component={Link} to="/clients" size="sm" c="dimmed">
              Used by {plural(usedBy, 'client')}
            </Anchor>
            {p.spec.description && <> · {p.spec.description}</>}
          </>
        }
        actions={
          <Group gap="xs">
            {!draft && canWrite && (
              <Button variant="primary" icon={FilePlus2} onClick={() => setEditing({ doc: published?.document ?? defaultDocument(), note: '' })}>
                New draft
              </Button>
            )}
            {canWrite && (
              <Menu position="bottom-end" withinPortal>
                <Menu.Target>
                  <ActionIcon variant="default" size="lg" aria-label="More plan actions">
                    <MoreHorizontal size={16} />
                  </ActionIcon>
                </Menu.Target>
                <Menu.Dropdown>
                  <Menu.Item leftSection={<Pencil size={14} />} onClick={() => setEditingPlan(true)}>
                    Edit name and description
                  </Menu.Item>
                  <Tooltip label={`Used by ${plural(usedBy, 'client')}; move them to another plan first.`} disabled={usedBy === 0} withinPortal>
                    <div>
                      <Menu.Item color="red" leftSection={<Trash2 size={14} />} disabled={usedBy > 0} onClick={() => remove.mutate()}>
                        Delete plan
                      </Menu.Item>
                    </div>
                  </Tooltip>
                </Menu.Dropdown>
              </Menu>
            )}
          </Group>
        }
      />
      <Text size="md" fw={500}>
        {cfg.ready ? planSummary(published?.document, liveLanes) : ' '}
      </Text>
      {published && <PoliciesStrip doc={published.document} />}
      <MutationError error={discard.error ?? remove.error} />

      {draft && (
        <Alert color="blue" variant="light" title={`Draft v${draft.version} — not live yet`} aria-label="Draft">
          <Stack gap="xs">
            {draftUnchanged ? (
              <Text size="sm">Routes exactly like the live version; nothing to publish yet.</Text>
            ) : (
              <Stack gap={2}>
                <Text size="sm">Changes from {published ? `live v${published.version}` : 'nothing live'}:</Text>
                <List size="sm" spacing={2}>
                  {draftChanges.map((x) => (
                    <List.Item key={x}>{x}</List.Item>
                  ))}
                </List>
              </Stack>
            )}
            {draftHazards.map((h) => (
              <Text key={h} size="sm" c="red" fw={600}>
                <AlertTriangle size={14} style={{ verticalAlign: '-2px' }} /> {h}
              </Text>
            ))}
            {draftReport && <ReportView v={draft} r={draftReport} />}
            <MutationError error={validate.error} />
            <Group gap="xs">
              {canWrite && (
                <Button size="small" icon={Pencil} onClick={() => edit(draft)}>
                  Edit draft
                </Button>
              )}
              <Button size="small" icon={ShieldCheck} busy={validate.isPending} onClick={() => validate.mutate(draft.id)}>
                Check for problems
              </Button>
              <Button size="small" icon={Eye}
                onClick={() => void navigate({ to: '/routing/plans/$planId', params: { planId }, search: { tab: 'test' }, replace: true })}>
                Test a request
              </Button>
              {canPublish && (
                <Tooltip label="The draft routes exactly like live." disabled={!draftUnchanged} withinPortal>
                  <span>
                    <Button size="small" variant="primary" icon={Rocket} disabled={draftUnchanged} onClick={() => setReleasing({ kind: 'publish', v: draft })}>
                      Publish v{draft.version}
                    </Button>
                  </span>
                </Tooltip>
              )}
              {canWrite && (
                <ConfirmButton size="small" icon={Trash2} title={`Discard draft v${draft.version}?`} message="The draft and its changes are deleted."
                  confirmLabel="Discard" onConfirm={() => discard.mutate(draft.id)}>
                  Discard
                </ConfirmButton>
              )}
            </Group>
          </Stack>
        </Alert>
      )}

      <Tabs value={current} onChange={(v) => v && void navigate({ to: '/routing/plans/$planId', params: { planId }, search: { tab: v as 'flow' | 'versions' | 'test' }, replace: true })}>
        <Tabs.List aria-label="Plan sections">
          <Tabs.Tab value="flow">Flow</Tabs.Tab>
          <Tabs.Tab value="versions">History</Tabs.Tab>
          <Tabs.Tab value="test">Test a request</Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="flow" pt="md">
          {!shown ? (
            <Text c="dimmed">No versions yet. Create a draft to define where requests go.</Text>
          ) : !cfg.ready ? (
            <Skeleton height={120} />
          ) : (
            <Stack gap="md">
              {(draft || items.length > 1) && (
                <Group gap="sm" wrap="wrap">
                  <Text size="sm" c="dimmed">
                    Showing
                  </Text>
                  {draft && published ? (
                    <SegmentedControl size="xs" value={shown.id === draft.id ? 'draft' : shown.id === published.id ? 'live' : 'other'}
                      onChange={(v) => setShownId(v === 'draft' ? draft.id : published.id)}
                      data={[{ value: 'draft', label: `Draft v${draft.version}` }, { value: 'live', label: `Live v${published.version}` },
                        ...(shown.id !== draft.id && shown.id !== published.id ? [{ value: 'other', label: `v${shown.version}` }] : [])]} />
                  ) : (
                    <Select aria-label="Version" size="xs" w={200} allowDeselect={false} value={shown.id} onChange={setShownId}
                      data={items.map((v) => ({ value: v.id, label: `v${v.version} · ${statusLabel[v.status] ?? v.status}` }))} />
                  )}
                </Group>
              )}
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
            <Stack gap="sm">
              <MutationError error={rollback.error} />
              <Timeline bulletSize={20} lineWidth={2} active={-1}>
                {items.map((v) => {
                  const author = userName(v.published_by || v.created_by)
                  const hazards = engineHazards(v.document)
                  return (
                    <Timeline.Item
                      key={v.id}
                      bullet={v.status === 'published' ? <CheckCircle2 size={12} /> : undefined}
                      title={
                        <Group gap="xs">
                          <Text fw={600}>v{v.version}</Text>
                          <Badge tone={statusTone[v.status]}>{statusLabel[v.status] ?? v.status}</Badge>
                          {v.rollback_of && <Badge>Rollback</Badge>}
                          {hazards.length > 0 && <Badge tone="error">Blocks every request</Badge>}
                        </Group>
                      }
                    >
                      <Stack gap={6} mt={4}>
                        {v.note && <Text size="sm">{v.note}</Text>}
                        <Text size="xs" c="dimmed">
                          Created {fmtDateTime(v.created_at)}
                          {v.published_at ? ` · published ${fmtDateTime(v.published_at)}` : ''}
                          {author ? ` · by ${author}` : ''}
                        </Text>
                        {report?.id === v.id && <ReportView v={v} r={report.r} />}
                        <Group gap="xs">
                          <Button size="small" variant="ghost" icon={Eye}
                            onClick={() => {
                              setShownId(v.id)
                              void navigate({ to: '/routing/plans/$planId', params: { planId }, search: { tab: 'flow' }, replace: true })
                            }}>
                            View
                          </Button>
                          {v.status !== 'published' && (
                            <Button size="small" variant="ghost" icon={GitCompare} onClick={() => setComparing(v)}>
                              Compare with live
                            </Button>
                          )}
                          {(v.status === 'draft' || v.status === 'published') && (
                            <Button size="small" variant="ghost" icon={ShieldCheck} busy={validate.isPending && validate.variables === v.id} onClick={() => validate.mutate(v.id)}>
                              {v.status === 'draft' ? 'Check for problems' : 'Re-check against current configuration'}
                            </Button>
                          )}
                          {v.status === 'draft' && canWrite && (
                            <Button size="small" variant="ghost" icon={Pencil} onClick={() => edit(v)}>
                              Edit
                            </Button>
                          )}
                          {v.status === 'superseded' && canPublish && (
                            <Button size="small" variant="ghost" icon={History} onClick={() => setReleasing({ kind: 'rollback', v })}>
                              Roll back to this
                            </Button>
                          )}
                        </Group>
                      </Stack>
                    </Timeline.Item>
                  )
                })}
              </Timeline>
            </Stack>
          )}
        </Tabs.Panel>

        <Tabs.Panel value="test" pt="md">
          {items.length === 0 ? (
            <Text c="dimmed">Create a draft first; drafts can be tested before publishing.</Text>
          ) : cfg.ready ? (
            <section aria-label="Simulator">
              <Simulator key={planId} planId={planId} versions={items} accounts={laneAccounts(allLanes)} defaultModel={firstAlias} accountName={accountName}
                modelSuggestions={[...new Set([...(shown?.document.model_mappings ?? []).map((m) => m.match), ...Object.values(cfg.maps.aliases).map((a) => a.name)])]} />
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
      {releasing && (
        <ReleaseDialog
          open
          title={releasing.kind === 'publish' ? `Publish v${releasing.v.version}?` : `Roll back to v${releasing.v.version}?`}
          confirmLabel={releasing.kind === 'publish' ? 'Publish' : 'Roll back'}
          target={releasing.v}
          live={published}
          usedBy={usedBy}
          aliasName={aliasName}
          busy={publish.isPending || rollback.isPending}
          error={publish.error ?? rollback.error}
          validate={runValidate}
          onConfirm={() => (releasing.kind === 'publish' ? publish.mutate(releasing.v.id) : rollback.mutate(releasing.v.id))}
          onClose={() => {
            publish.reset()
            rollback.reset()
            setReleasing(null)
          }}
        />
      )}
      {comparing && <CompareDialog v={comparing} live={published} aliasName={aliasName} onClose={() => setComparing(null)} />}
      {editingPlan && <PlanDialog plan={p} onClose={() => setEditingPlan(false)} />}
    </Stack>
  )
}
