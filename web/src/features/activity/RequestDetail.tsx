import { type ReactNode, useEffect, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Accordion, Alert, Badge, Breadcrumbs, Button, Code, Group, SegmentedControl, Skeleton, Stack, Text, Timeline, Title, Tooltip } from '@mantine/core'
import { Check, CircleAlert, CircleCheck, CircleDashed, CircleX, History, ScrollText, X } from 'lucide-react'
import { QueryError } from '@/components/states'
import { FormDrawer, MutationError } from '@/components/form'
import { apiSend, ApiError } from '@/lib/api/client'
import { requestQuery, resourceListQuery, tracesQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type {
  AccountRes,
  Attempt,
  BindingRes,
  Decision,
  LedgerEntry,
  PlanRes,
  ProjectRes,
  ProviderRes,
  ReplayResult,
  RequestDetail as Detail,
  TranslationLoss,
  WorkspaceRes,
} from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { fmtDateTime, fmtRelative, getDisplayTimeZone, isKnown } from '@/lib/format'
import { asPrivacySummary } from '@/features/privacy/PrivacySummaryPanel'
import { detectorLabel } from '@/features/privacy/privacy'
import { CopyText, Empty, Fields, InfoTip, PageHeader, Unknown, useCompact } from '@/ui/kit'
import { costKindHelp, costKindName, harnessName, human, money, ms, protocolName, short, statusName } from '@/ui/fmt'
import s from './activity.module.css'

const NOT_REPORTED = 'Not reported by the provider'

function asDecision(v: unknown): Decision {
  return v && typeof v === 'object' ? (v as Decision) : {}
}

function asLosses(v: unknown): TranslationLoss[] {
  return Array.isArray(v) ? (v as TranslationLoss[]) : []
}

function useNames() {
  const providers = useQuery(resourceListQuery<ProviderRes>('providers'))
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const bindings = useQuery(resourceListQuery<BindingRes>('client-bindings'))
  const projects = useQuery(resourceListQuery<ProjectRes>('projects'))
  const workspaces = useQuery(resourceListQuery<WorkspaceRes>('workspaces'))
  const plans = useQuery(resourceListQuery<PlanRes>('routing-plans'))
  const pick = (m: Record<string, { name: string }>) => (id: string | undefined) => (id ? m[id]?.name ?? id : '')
  return {
    provider: pick(byId(providers.data?.items)),
    account: pick(byId(accounts.data?.items)),
    client: pick(byId(bindings.data?.items)),
    project: pick(byId(projects.data?.items)),
    workspace: pick(byId(workspaces.data?.items)),
    plan: pick(byId(plans.data?.items)),
  }
}

type Names = ReturnType<typeof useNames>

function clock(iso: string): string {
  return new Intl.DateTimeFormat('en-GB', { timeZone: getDisplayTimeZone(), hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }).format(new Date(iso))
}

const statusColor: Record<string, string> = { ok: 'green', error: 'red', blocked: 'yellow', canceled: 'gray', in_progress: 'teal' }

/** One request, answered: where it went and why. */
export function RequestPage({ id }: { id: string }) {
  const q = useQuery({ ...requestQuery(id), retry: false })
  const model = q.data?.requested_model
  useEffect(() => {
    if (!model) return
    document.title = `${model} · Request · Harbor Pilot`
    return () => {
      document.title = 'Harbor Pilot'
    }
  }, [model])
  const names = useNames()
  const compact = useCompact()
  const [replayOpen, setReplayOpen] = useState(false)
  if (q.isError) {
    if (q.error instanceof ApiError && q.error.status === 404) {
      return (
        <>
          <PageHeader title="Request not found" crumbs={[{ label: 'Requests', to: '/activity' }, { label: 'Not found' }]} />
          <Empty
            title="This request is not here"
            action={
              <Button component={Link} to="/activity" variant="light">
                Back to requests
              </Button>
            }
          >
            The ID is unknown, or the record has expired.
          </Empty>
        </>
      )
    }
    return <QueryError error={q.error} retry={() => void q.refetch()} />
  }
  if (!q.data) return <Skeleton height={400} radius="md" />
  const r = q.data
  const d = asDecision(r.decision)
  const losses = asLosses(r.losses)
  const served = r.final_model && r.final_model !== r.requested_model ? r.final_model : ''
  const fellBack = r.status === 'ok' && r.attempt_list.length > 1
  const replayButton = (
    <Button variant="default" leftSection={<History size={15} />} onClick={() => setReplayOpen(true)}>
      Replay with current config
    </Button>
  )
  return (
    <>
      <Stack gap={6}>
        <Breadcrumbs separator="/" className={s.crumbs}>
          <Link to="/activity">Requests</Link>
          <span>
            {clock(r.created_at)} {r.binding_id ? names.client(r.binding_id) : ''}
          </span>
        </Breadcrumbs>
        <Group justify="space-between" align="flex-start" gap="sm" wrap="wrap">
          <Group gap="sm" wrap="wrap" style={{ minWidth: 0 }}>
            <Title order={1} className={s.reqTitle}>
              {r.requested_model || 'Request'}
              {served && <span className={s.arrow}> → </span>}
              {served}
            </Title>
            <Badge color={fellBack ? 'yellow' : (statusColor[r.status] ?? 'gray')} size="lg">
              {statusName[r.status] ?? r.status}
              {fellBack ? ' · Fallback' : ''}
            </Badge>
          </Group>
          {!compact && replayButton}
        </Group>
      </Stack>

      <Text className={s.answer}>
        <Answer r={r} d={d} names={names} />
      </Text>

      <Facts r={r} names={names} />

      {r.mode === 'passthrough' && (
        <Alert color="yellow" variant="light" title="Sent without translation">
          A protocol issue was open, so the tool’s request went to the provider unchanged.
          {losses.length > 0 && ` Lost: ${losses.map((l) => l.feature).join(', ')}.`}
        </Alert>
      )}

      <Block title="Route">
        <RoutePath r={r} d={d} names={names} />
      </Block>

      <Block title="Attempts" info="A backup is only tried before any output reaches the tool, and only as the routing plan allows.">
        <Attempts attempts={r.attempt_list} names={names} />
      </Block>

      <Block title="Candidates">
        <Candidates d={d} />
      </Block>

      <Block title="Cost">
        <CostLine r={r} />
      </Block>

      <Checklist r={r} losses={losses} />

      <Accordion variant="separated" multiple chevronPosition="right" className={s.details}>
        <Accordion.Item value="verified">
          <Accordion.Control>Verified context</Accordion.Control>
          <Accordion.Panel>
            <Verified r={r} d={d} names={names} />
          </Accordion.Panel>
        </Accordion.Item>
        <Accordion.Item value="reported">
          <Accordion.Control>Reported by the tool</Accordion.Control>
          <Accordion.Panel>
            <Reported r={r} />
          </Accordion.Panel>
        </Accordion.Item>
        <Accordion.Item value="ledger">
          <Accordion.Control>Accounting ledger</Accordion.Control>
          <Accordion.Panel>
            <Ledger ledger={r.ledger} names={names} />
          </Accordion.Panel>
        </Accordion.Item>
        <Accordion.Item value="ids">
          <Accordion.Control>IDs</Accordion.Control>
          <Accordion.Panel>
            <Fields
              items={[
                ['Request', <CopyText value={r.id} />],
                ['Credential', r.credential_id ? <CopyText value={r.credential_id} /> : <span className="muted">—</span>],
                ['Session', r.session_id ? <CopyText value={r.session_id} /> : <span className="muted">—</span>],
                ['Plan version', d.plan_version_id ? <CopyText value={d.plan_version_id} /> : <span className="muted">—</span>],
              ]}
            />
          </Accordion.Panel>
        </Accordion.Item>
      </Accordion>

      {compact && replayButton}

      <ReplayDrawer r={r} d={d} opened={replayOpen} onClose={() => setReplayOpen(false)} />
    </>
  )
}

function Block({ title, info, children }: { title: string; info?: ReactNode; children: ReactNode }) {
  return (
    <section aria-label={title} className={s.block}>
      <Group gap={6} mb={10}>
        <Title order={2}>{title}</Title>
        {info && <InfoTip>{info}</InfoTip>}
      </Group>
      {children}
    </section>
  )
}

function attemptProblem(a: Attempt): string {
  const what = a.error_kind ? human(a.error_kind).toLowerCase() : 'failed'
  return `${what}${a.http_status ? ` (${a.http_status})` : ''}`
}

/** The one-sentence answer, built from the recorded decision. */
function Answer({ r, d, names }: { r: Detail; d: Decision; names: Names }) {
  const failed = r.attempt_list.filter((a) => a.status !== 'ok')
  const skipped = (d.excluded ?? []).slice(0, 2)
  const parts: ReactNode[] = []
  if (r.final_account_id && r.status === 'ok') {
    parts.push(
      <span key="served">
        Served by <b>{names.account(r.final_account_id)}</b> · <b>{r.final_model}</b>
        {failed.length > 0 && (
          <>
            {' '}
            after{' '}
            {failed.map((a, i) => (
              <span key={a.id}>
                {i > 0 && ' and '}
                <b>{names.account(a.account_id)}</b> was {attemptProblem(a)}
              </span>
            ))}
          </>
        )}
        .
      </span>,
    )
  } else if (r.status === 'blocked') {
    parts.push(<span key="blocked">Blocked{r.error_message ? `: ${r.error_message}` : ' by policy'}.</span>)
  } else if (r.status === 'error') {
    parts.push(<span key="err">Not served{r.error_message ? `: ${r.error_message}` : ''}.</span>)
  } else {
    parts.push(<span key="other">{statusName[r.status] ?? r.status}.</span>)
  }
  for (const x of skipped) {
    parts.push(
      <span key={`skip-${x.account_id}-${x.model}`}>
        {' '}
        {x.account} was skipped: {human(x.reason).toLowerCase()}.
      </span>,
    )
  }
  return <>{parts}</>
}

function Facts({ r, names }: { r: Detail; names: Names }) {
  const canTraces = useCan('traces.metadata.read')
  const traces = useQuery({ ...tracesQuery({ request_id: r.id }), enabled: canTraces })
  const trace = traces.data?.items?.[0]
  const planId = r.plan_id
  return (
    <dl className={s.facts}>
      <div>
        <dt>Client</dt>
        <dd>
          {r.binding_id ? (
            <Link to="/clients/$bindingId" params={{ bindingId: r.binding_id }}>
              {names.client(r.binding_id)}
            </Link>
          ) : (
            <span className="muted">—</span>
          )}
        </dd>
      </div>
      <div>
        <dt>Tool</dt>
        <dd>{r.harness ? harnessName[r.harness] ?? r.harness : <span className="muted">Unknown</span>}</dd>
      </div>
      <div>
        <dt>Plan</dt>
        <dd>
          {planId ? (
            <Link to="/routing/plans/$planId" params={{ planId }}>
              {names.plan(planId)}
              {r.plan_version ? ` v${r.plan_version}` : ''}
            </Link>
          ) : (
            <span className="muted">—</span>
          )}
        </dd>
      </div>
      <div>
        <dt>Latency</dt>
        <dd>
          {!isKnown(r.duration_ms) ? (
            <Unknown reason="Not finished" />
          ) : r.duration_ms === 0 ? (
            <Unknown reason="Timing was not recorded for this request" />
          ) : (
            ms(r.duration_ms)
          )}
        </dd>
      </div>
      <div>
        <dt>First token</dt>
        <dd>{isKnown(r.ttft_ms) && r.ttft_ms > 0 ? ms(r.ttft_ms) : <span className="muted">—</span>}</dd>
      </div>
      <div>
        <dt>Cost</dt>
        <dd>
          {r.cost ? (
            <Tooltip label={costKindHelp[r.cost.kind] ?? ''}>
              <span>
                {money(r.cost.amount, r.cost.currency)}
                {r.cost.kind === 'estimated_api_charge' && <span className="muted"> est.</span>}
              </span>
            </Tooltip>
          ) : (
            <Unknown reason="No price configured, or usage not reported" />
          )}
        </dd>
      </div>
      <div>
        <dt>When</dt>
        <dd>
          <Tooltip label={fmtDateTime(r.created_at, { seconds: true })}>
            <span>
              {clock(r.created_at)} <span className="muted">({fmtRelative(r.created_at)})</span>
            </span>
          </Tooltip>
          {trace && (
            <>
              {' · '}
              <Link to="/traces/$traceId" params={{ traceId: trace.id }}>
                <ScrollText size={12} aria-hidden="true" /> Captured content
              </Link>
            </>
          )}
        </dd>
      </div>
    </dl>
  )
}

function matchedRule(resolution: string): string {
  if (resolution.startsWith('mapping:')) return `Rule ${resolution.slice(8)}`
  if (resolution === 'alias_name') return 'Asked by model name'
  if (resolution === 'default_alias') return 'Plan default'
  return '—'
}

/** Requested → Rule → Model name → Fallback group → Served by. */
function RoutePath({ r, d, names }: { r: Detail; d: Decision; names: Names }) {
  const compact = useCompact()
  const steps: { label: string; value: string; hint?: string }[] = [
    { label: 'Requested', value: r.requested_model || '—' },
    { label: 'Matched', value: matchedRule(r.resolution || d.resolution || '') },
    {
      label: 'Model name',
      value: (d.alias_chain?.length ?? 0) > 1 ? d.alias_chain!.join(' → ') : d.alias || '—',
      hint: 'A name your tools ask for that Harbor Pilot maps to real models.',
    },
    { label: 'Fallback group', value: d.pool || '—', hint: 'The ordered list of account and model pairs tried in turn.' },
    {
      label: r.mode === 'translated' ? 'Served by · translated' : 'Served by',
      value: r.final_account_id ? `${names.account(r.final_account_id)} · ${r.final_model}` : 'Not served',
      hint: r.mode === 'translated' ? 'Translated: the request was converted to the provider’s format.' : r.mode === 'native' ? 'Direct: no conversion was needed.' : undefined,
    },
  ]
  if (compact) {
    return (
      <Timeline bulletSize={12} lineWidth={2} active={steps.length - 1}>
        {steps.map((st) => (
          <Timeline.Item
            key={st.label}
            title={
              <Text size="xs" c="dimmed">
                {st.label}
              </Text>
            }
          >
            <Text size="sm" className={s.ellipsis}>
              {st.value}
            </Text>
          </Timeline.Item>
        ))}
      </Timeline>
    )
  }
  return (
    <div className={s.path}>
      {steps.map((st, i) => (
        <span key={st.label} className={s.pathItem}>
          {i > 0 && <span className={s.arrow}>→</span>}
          <Tooltip label={st.hint ?? st.value} openDelay={300}>
            <span className={s.step} data-last={i === steps.length - 1 || undefined}>
              <span>{st.label}</span>
              <span>{st.value}</span>
            </span>
          </Tooltip>
        </span>
      ))}
    </div>
  )
}

const attemptIcon = (status: string) => (status === 'ok' ? <CircleCheck size={14} /> : status === 'error' ? <CircleX size={14} /> : <CircleDashed size={14} />)

function Attempts({ attempts, names }: { attempts: Attempt[]; names: Names }) {
  if (attempts.length === 0) {
    return (
      <Text size="sm" c="dimmed">
        No provider was contacted.
      </Text>
    )
  }
  return (
    <Timeline bulletSize={22} lineWidth={2} active={attempts.length - 1}>
      {attempts.map((a) => (
        <Timeline.Item
          key={a.id}
          bullet={attemptIcon(a.status)}
          color={a.status === 'ok' ? 'green' : a.status === 'error' ? 'red' : 'gray'}
          title={
            <Group gap={6} wrap="wrap">
              <Text fw={600} size="sm">
                #{a.n} {names.account(a.account_id)}
              </Text>
              <Text size="sm" c="dimmed">
                · {a.status === 'ok' ? 'OK' : attemptProblem(a).replace(/^./, (c) => c.toUpperCase())}
                {a.retry_after_s ? ` · retry after ${a.retry_after_s}s` : ''}
                {a.status === 'ok' && isKnown(a.duration_ms) ? ` · ${ms(a.duration_ms)}` : ''}
              </Text>
              {a.failover_reason && (
                <Badge color="yellow" variant="light" size="sm">
                  {human(a.failover_reason)}
                </Badge>
              )}
            </Group>
          }
        >
          <Text size="xs" c="dimmed">
            {a.model} · {names.provider(a.provider_id)} · {protocolName[a.upstream_protocol] ?? a.upstream_protocol}
            {isKnown(a.ttft_ms) && a.status === 'ok' ? ` · first token ${ms(a.ttft_ms)}` : ''}
          </Text>
          {a.error_message && (
            <Text size="sm" c="red" mt={2}>
              {a.error_message}
            </Text>
          )}
          {a.executed && a.status !== 'ok' && (
            <Text size="xs" c="dimmed" mt={2}>
              The provider may have processed this attempt, so it was not retried automatically.
            </Text>
          )}
        </Timeline.Item>
      ))}
    </Timeline>
  )
}

function Candidates({ d }: { d: Decision }) {
  const candidates = d.candidates ?? []
  const excluded = d.excluded ?? []
  if (candidates.length === 0 && excluded.length === 0) {
    return (
      <Text size="sm" c="dimmed">
        No candidates were recorded.
      </Text>
    )
  }
  return (
    <ol className={s.candidates}>
      {candidates.map((c) => (
        <li key={`ok-${c.account_id}-${c.model}`}>
          <Check size={15} color="var(--success)" aria-label="Eligible" />
          <span className={s.candName}>
            {c.account} <span className="muted">· {c.model}</span>
          </span>
          <Badge color="gray" variant="light" size="sm">
            {c.native ? 'Direct' : 'Translated'}
          </Badge>
        </li>
      ))}
      {excluded.map((x) => (
        <li key={`x-${x.account_id}-${x.model}-${x.stage}`}>
          <X size={15} color="var(--error)" aria-label="Skipped" />
          <span className={s.candName}>
            {x.account} <span className="muted">· {x.model}</span>
          </span>
          <Tooltip label={x.detail || human(x.reason)} disabled={!x.detail}>
            <Badge color="red" variant="light" size="sm">
              Skipped: {human(x.reason).toLowerCase()}
            </Badge>
          </Tooltip>
        </li>
      ))}
    </ol>
  )
}

function CostLine({ r }: { r: Detail }) {
  const t = r.tokens
  const tokens = [
    isKnown(t.input) ? `${short(t.input)} in` : undefined,
    isKnown(t.output) ? `${short(t.output)} out` : undefined,
    isKnown(t.cache_read) && t.cache_read > 0 ? `${short(t.cache_read)} cached` : undefined,
    isKnown(t.reasoning) && t.reasoning > 0 ? `${short(t.reasoning)} reasoning` : undefined,
  ].filter(Boolean)
  return (
    <Text size="md">
      {r.cost ? (
        <Tooltip label={costKindHelp[r.cost.kind] ?? ''}>
          <b>
            {money(r.cost.amount, r.cost.currency)} {r.cost.kind === 'estimated_api_charge' ? 'estimated' : (costKindName[r.cost.kind] ?? r.cost.kind).toLowerCase()}
          </b>
        </Tooltip>
      ) : (
        <span className="muted">No cost recorded</span>
      )}
      {' · '}
      {tokens.length ? tokens.join(' / ') : <Unknown reason={NOT_REPORTED} />}
    </Text>
  )
}

/** Tools, translation and masking: one row each, detail only when something happened. */
function Checklist({ r, losses }: { r: Detail; losses: TranslationLoss[] }) {
  const p = asPrivacySummary(r.privacy)
  const rows: { ok: boolean; title: string; detail?: ReactNode }[] = []
  rows.push(
    r.tools_offered === 0 && r.tool_calls === 0
      ? { ok: true, title: 'No tools used' }
      : {
          ok: r.tool_result_errors === 0,
          title: `${r.tool_calls} tool ${r.tool_calls === 1 ? 'call' : 'calls'}, ${r.tools_offered} offered`,
          detail: `${r.tool_results_seen} results seen${r.tool_result_errors ? `, ${r.tool_result_errors} with errors` : ''}${r.tool_names.length ? ` · ${r.tool_names.join(', ')}` : ''}`,
        },
  )
  rows.push(
    losses.length === 0
      ? { ok: true, title: 'Nothing lost in translation' }
      : { ok: false, title: `${losses.length} ${losses.length === 1 ? 'feature' : 'features'} lost in translation`, detail: losses.map((l) => `${l.feature} (${l.severity})`).join(', ') },
  )
  if (!p) rows.push({ ok: true, title: 'No masking applied' })
  else if (p.blocked) rows.push({ ok: false, title: 'Blocked by the privacy policy' })
  else if (p.unmasked_fallback) rows.push({ ok: false, title: 'Sent unmasked: masking could not run and the policy allows it' })
  else
    rows.push({
      ok: true,
      title: p.masked ? `${p.masked} ${p.masked === 1 ? 'value' : 'values'} masked before sending` : 'Checked for secrets: nothing to mask',
      detail: p.rules.length ? p.rules.map((x) => `${detectorLabel(x.rule)} ${x.count}`).join(', ') : undefined,
    })
  return (
    <section aria-label="Checks" className={s.block}>
      <ul className={s.checks}>
        {rows.map((row) => (
          <li key={row.title}>
            {row.ok ? <CircleCheck size={15} color="var(--success)" aria-label="OK" /> : <CircleAlert size={15} color="var(--warning)" aria-label="Note" />}
            <span>
              {row.title}
              {row.detail && <span className="muted"> · {row.detail}</span>}
            </span>
          </li>
        ))}
      </ul>
    </section>
  )
}

const confidenceName: Record<string, string> = { high: 'high confidence', medium: 'medium confidence', low: 'low confidence', unknown: 'unknown' }

function Verified({ r, d, names }: { r: Detail; d: Decision; names: Names }) {
  const refs = Object.entries(r.trusted_refs ?? {})
  return (
    <Fields
      items={[
        ['Client', r.binding_id ? names.client(r.binding_id) : '—'],
        ['Project', r.project_id ? names.project(r.project_id) : '—'],
        ['Workspace', r.workspace_id ? names.workspace(r.workspace_id) : '—'],
        [
          'Session',
          r.session_id ? (
            <Group gap={6} wrap="nowrap" component="span">
              <Link to="/activity" search={{ session_id: r.session_id }}>
                Other requests in this session
              </Link>
              {r.session_confidence === 'low' && <InfoTip>Session detected with low confidence.</InfoTip>}
            </Group>
          ) : (
            'Not identified'
          ),
        ],
        ['Purpose', d.intent ? `${human(d.intent)}${d.intent_source ? ` (${d.intent_source})` : ''}` : '—'],
        ['Protocol', `${protocolName[r.inbound_protocol] ?? r.inbound_protocol}${r.stream ? ', streamed' : ''}`],
        ['Required capabilities', r.required_capabilities.length ? r.required_capabilities.join(', ') : 'None'],
        ...refs.map(([k, v]) => [k, v] as [ReactNode, ReactNode]),
      ]}
    />
  )
}

function Reported({ r }: { r: Detail }) {
  const claims = Object.entries(r.claims ?? {})
  return (
    <Stack gap="sm">
      <Text size="xs" c="dimmed">
        Hints the tool sent. They are recorded for context and never grant access.
      </Text>
      <Fields
        items={[
          ['Tool', r.harness ? `${harnessName[r.harness] ?? r.harness}${r.harness_version ? ` ${r.harness_version}` : ''} (${confidenceName[r.harness_confidence] ?? r.harness_confidence})` : '—'],
          ...claims.map(([k, v]) => [claimLabel(k), v] as [ReactNode, ReactNode]),
        ]}
      />
    </Stack>
  )
}

/** Plain label for a tool-reported hint such as "session:x-claude-code-session-id". */
export function claimLabel(key: string): ReactNode {
  const [kind, header] = key.includes(':') ? (key.split(/:(.*)/s) as [string, string]) : [key, '']
  const names: Record<string, string> = { session: 'Session', project: 'Project', intent: 'Purpose', external_ref: 'Reference', run: 'Run' }
  const label = names[kind] ? `${names[kind]} (from tool)` : human(kind)
  return header ? (
    <Tooltip label={`Header ${header}`}>
      <span>{label}</span>
    </Tooltip>
  ) : (
    label
  )
}

const sourceName: Record<string, string> = {
  upstream_reported: 'Reported by provider',
  provider_reported: 'Reported by provider',
  estimated: 'Estimated',
  client_reported: 'Reported by tool',
  unknown: 'Not reported',
}

function Ledger({ ledger, names }: { ledger: LedgerEntry[]; names: Names }) {
  if (ledger.length === 0) {
    return (
      <Text size="sm" c="dimmed">
        Nothing was recorded: the request did not reach a provider.
      </Text>
    )
  }
  return (
    <Stack gap="sm">
      {ledger.map((l) => (
        <div key={l.id}>
          <Text size="sm">
            <span className="muted">{clock(l.at)} · {human(l.kind)} · </span>
            {names.account(l.account_id)} <span className="muted">· {l.model}</span>
          </Text>
          <Text size="xs" c="dimmed">
            {[
              isKnown(l.tokens.input) && `${short(l.tokens.input)} in`,
              isKnown(l.tokens.output) && `${short(l.tokens.output)} out`,
              isKnown(l.tokens.cache_read) && `${short(l.tokens.cache_read)} cache read`,
              isKnown(l.tokens.cache_write) && `${short(l.tokens.cache_write)} cache write`,
            ]
              .filter(Boolean)
              .join(' · ')}{' '}
            · {sourceName[l.usage_source] ?? human(l.usage_source)}
            {l.cost && l.cost.kind !== 'none' ? ` · ${money(l.cost.amount, l.cost.currency)} ${(costKindName[l.cost.kind] ?? '').toLowerCase()}` : ''}
            {l.cost?.price_version ? ` · prices ${l.cost.price_version}` : ''}
          </Text>
        </div>
      ))}
      <RawJson
        value={ledger.map((l) => ({ kind: l.kind, at: l.at, billing: l.billing, provenance: l.provenance }))}
      />
    </Stack>
  )
}

/** Raw record behind a toggle, for support and debugging. */
function RawJson({ value }: { value: unknown }) {
  const [open, setOpen] = useState(false)
  return (
    <div>
      <Button variant="subtle" size="compact-xs" color="gray" onClick={() => setOpen((o) => !o)} aria-expanded={open}>
        {open ? 'Hide raw record' : 'Show raw record'}
      </Button>
      {open && (
        <Code block mt={6} style={{ fontSize: 'var(--text-xs)', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>
          {JSON.stringify(value, null, 2)}
        </Code>
      )}
    </div>
  )
}

const healthModeText: Record<string, string> = {
  current: 'today’s account health',
  assume_available: 'every account available',
}

function firstChoice(d: Decision | null | undefined, refused?: boolean): string {
  if (refused) return 'refused'
  const c = d?.candidates?.[0]
  return c ? `${c.account} · ${c.model}` : 'no eligible account'
}

/** Re-runs routing for this request against today's configuration. */
function ReplayDrawer({ r, d, opened, onClose }: { r: Detail; d: Decision; opened: boolean; onClose: () => void }) {
  const names = useNames()
  const [health, setHealth] = useState<'current' | 'assume_available'>('current')
  const run = useMutation({
    mutationFn: () => apiSend<ReplayResult>('POST', `/activity/requests/${encodeURIComponent(r.id)}/replay`, { health_mode: health }),
  })
  const res = run.data
  const failed = r.attempt_list.filter((a) => a.status !== 'ok')
  const servedThen = r.final_account_id
    ? `${names.account(r.final_account_id)} · ${r.final_model}${failed.length > 0 ? ` (after ${failed.map((a) => attemptProblem(a)).join(', ')})` : ''}`
    : 'not served'
  return (
    <FormDrawer open={opened} onClose={onClose} title="Replay with current config" size="md" dirty={false}>
      <Stack gap="md" aria-label="Replay" role="region">
        <Text size="sm" c="dimmed">
          Runs this request’s routing again against today’s plan. Nothing is sent to a provider. Choose whether to use today’s account health or to
          assume every account is available.
        </Text>
        <SegmentedControl
          value={health}
          onChange={(v) => {
            setHealth(v as typeof health)
            run.reset()
          }}
          data={[
            { value: 'current', label: 'Live account health' },
            { value: 'assume_available', label: 'All accounts available' },
          ]}
          aria-label="Account health for replay"
        />
        <Button leftSection={<History size={15} />} loading={run.isPending} onClick={() => run.mutate()}>
          Replay
        </Button>
        <MutationError error={run.error} />
        {res && (
          <Alert
            variant="light"
            color={res.replay.error ? 'red' : res.changed ? 'yellow' : 'green'}
            title={res.replay.error ? 'Would now be refused' : res.changed ? 'Routing would change' : 'Same routing today'}
            role="status"
          >
            <Stack gap={4}>
              <Text size="sm">
                <b>Served then:</b> {servedThen}
              </Text>
              <Text size="sm">
                <b>First choice then:</b> {firstChoice(d)}
              </Text>
              <Text size="sm">
                <b>First choice now:</b> {firstChoice(res.replay.decision, Boolean(res.replay.error))}
                {res.replay.version ? ` (plan v${res.replay.version}, ${healthModeText[health]})` : ` (${healthModeText[health]})`}
              </Text>
              {res.replay.error && <Text size="sm">{res.replay.error}</Text>}
            </Stack>
          </Alert>
        )}
        {res && res.notes.length > 0 && (
          <Stack gap={4}>
            {res.notes.map((n) => (
              <Text key={n} size="xs" c="dimmed">
                {n.replace(/\bassume_available\b/g, 'every account available').replace(/\bcurrent\b state/g, 'today’s state')}
              </Text>
            ))}
          </Stack>
        )}
      </Stack>
    </FormDrawer>
  )
}
