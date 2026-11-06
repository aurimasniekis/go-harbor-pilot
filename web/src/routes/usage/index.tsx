import { useEffect, useMemo, useState } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { Accordion, Autocomplete, Badge, Button, Group, Paper, Pill, Progress, SegmentedControl, Select, Skeleton, Stack, Text, Tooltip } from '@mantine/core'
import { Coins, Download } from 'lucide-react'
import { QueryError } from '@/components/states'
import { externalUsageQuery, reconciliationQuery, resourceListQuery, usageRefGroupsQuery, usageSeriesQuery, usageSummaryQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { AccountRes, ReconciliationRow, UsageGroup, UsageGroupBy } from '@/lib/api/types'
import { presetRange } from '@/features/usage/range'
import { bucketFor, bucketize, stepFor } from '@/features/usage/buckets'
import { UsageChart } from '@/features/usage/UsageCharts'
import { REF_KEY, refKeyLabel, refLabel, withRef } from '@/features/refs/refs'
import { RefFilterButton, useRefKeyOptions } from '@/features/refs/RefPicker'
import { type Column, DataTable, Empty, Section, Stat, StatGrid, StatSkeleton, useCompact } from '@/ui/kit'
import { costKindHelp, money, short } from '@/ui/fmt'
import { groupChoice, rowSearch, RUN_KEY } from '@/features/usage/spendGroups'
import { DEFAULT_RANGE, Route as UsageRoute, type SpendSearch } from './route'

export const Route = createFileRoute('/usage/')({
  component: SpendPage,
})

const groupOptions: { value: UsageGroupBy; label: string }[] = [
  { value: 'account', label: 'Account' },
  { value: 'binding', label: 'Client' },
  { value: 'model', label: 'Model' },
  { value: 'project', label: 'Project' },
]

/** Group-by choices: the dimensions, then a run, then any other reference key. */
const groupChoices = [...groupOptions, { value: 'run', label: 'Run' }, { value: 'ref', label: 'Reference…' }]

const cap = (t: string) => t.charAt(0).toUpperCase() + t.slice(1)
const rangeName: Record<string, string> = { '1': 'last 24 hours', '7': 'last 7 days', '30': 'last 30 days', '90': 'last 90 days' }

/** Estimated cost of a group in its main currency (the one shown on the page). */
function estimate(g: UsageGroup, currency: string): number {
  return Number(g.costs.find((c) => c.kind === 'estimated_api_charge' && c.currency === currency)?.amount ?? 0)
}

function SpendPage() {
  const search = UsageRoute.useSearch()
  const navigate = useNavigate()
  const compact = useCompact()
  const rangeDays = search.range ?? DEFAULT_RANGE
  const preset = String(rangeDays)
  const groupBy = search.group ?? 'account'
  const refKey = groupBy.startsWith('ref:') ? groupBy.slice(4) : null
  const refs = search.ref
  const filtered = !!refs?.length
  const mode = search.view ?? 'cost'
  const set = (patch: Partial<SpendSearch>) => void navigate({ to: '/usage', search: { ...search, ...patch }, replace: true })
  const days = Number(preset)
  const bucket = bucketFor(days)
  const range = useMemo(() => presetRange(days), [days])
  // Headline numbers. Reference groups are paged, so the headline comes from
  // a small complete grouping instead.
  const summary = useQuery(usageSummaryQuery(range, refKey ? 'billing' : (groupBy as UsageGroupBy), refs))
  const refGroups = useInfiniteQuery({ ...usageRefGroupsQuery(range, groupBy, refs), enabled: !!refKey })
  // The chart, provider billing and fees can't be narrowed to a reference.
  const series = useQuery({ ...usageSeriesQuery(range, stepFor(bucket), refKey ? undefined : (groupBy as UsageGroupBy)), enabled: !filtered })
  const rec = useQuery({ ...reconciliationQuery(Math.min(days, 90)), enabled: !filtered })
  const rows = useMemo(() => (series.data ? bucketize(series.data.points ?? [], range.from, range.to, bucket) : []), [series.data, range, bucket])
  const groupNames = useMemo(() => Object.fromEntries((summary.data?.groups ?? []).map((g) => [g.key, g.label || g.key])), [summary.data])
  const stacked = useMemo(
    () =>
      (series.data?.groups ?? []).map((g) => ({
        label: g.key === 'other' ? 'Other' : g.key === '' ? 'Unattributed' : (groupNames[g.key] ?? g.key),
        rows: bucketize(g.points ?? [], range.from, range.to, bucket),
      })),
    [series.data, groupNames, range, bucket],
  )
  const s = summary.data
  const tokens = useMemo(() => {
    if (!filtered) return rows.reduce((t, r) => ({ input: t.input + r.input, output: t.output + r.output }), { input: 0, output: 0 })
    return (s?.groups ?? []).reduce((t, g) => ({ input: t.input + g.tokens.input, output: t.output + g.tokens.output }), { input: 0, output: 0 })
  }, [filtered, rows, s])
  const requests = (s?.groups ?? []).reduce((n, g) => n + g.attempts, 0)
  const est = s?.totals.find((c) => c.kind === 'estimated_api_charge')
  const currency = est?.currency ?? 'USD'
  const billed = useMemo(() => {
    if (filtered) return null
    let sum = 0
    let any = false
    for (const r of rec.data?.items ?? []) {
      if (r.provider_actual_cost && r.currency === currency) {
        sum += Number(r.provider_actual_cost)
        any = true
      }
    }
    return any ? sum : null
  }, [rec.data, currency, filtered])
  const fees = useMemo(
    () => (filtered ? 0 : (s?.subscription_fees ?? []).reduce((n, f) => n + (f.currency === currency ? Number(f.prorated) : 0), 0)),
    [s, currency, filtered],
  )
  const busyBuckets = rows.filter((r) => r.attempts > 0).length
  const breakdown = useMemo(() => (refKey ? (refGroups.data?.pages ?? []).flatMap((p) => p.groups ?? []) : (s?.groups ?? [])), [refKey, refGroups.data, s])
  const totalEst = refKey ? Number(est?.amount ?? 0) : breakdown.reduce((n, g) => n + estimate(g, currency), 0)
  // Reference groups arrive in key order a page at a time; keep that order so
  // "Load more" appends instead of reshuffling.
  const sortedGroups = useMemo(
    () => (refKey ? breakdown : [...breakdown].sort((a, b) => estimate(b, currency) - estimate(a, currency) || b.attempts - a.attempts)),
    [refKey, breakdown, currency],
  )
  const estAmount = est ? Number(est.amount) : 0
  const outside = billed !== null ? Math.max(0, billed - estAmount) : null
  const keyName = refKey ? refKeyLabel(refKey) : ''

  const groupLabel = refKey ? keyName : (groupOptions.find((g) => g.value === groupBy)?.label ?? 'Name')
  const groupName = (g: UsageGroup) => {
    if (refKey && !g.key) return <span className="muted">No {keyName.toLowerCase()}</span>
    return g.label || g.key || <span className="muted">Not set</span>
  }
  const columns: Column<UsageGroup>[] = [
    {
      id: 'name',
      header: groupLabel,
      mobile: 'title',
      cell: (g) => (
        <span style={{ display: 'inline-flex', gap: 6, alignItems: 'center', minWidth: 0 }}>
          <span className={refKey ? 'mono' : undefined} style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontWeight: 500 }}>
            {groupName(g)}
          </span>
          {g.billing === 'local' && groupBy === 'account' && (
            <Badge color="gray" size="xs">
              Local
            </Badge>
          )}
        </span>
      ),
    },
    { id: 'requests', header: 'Requests', align: 'right', only: 'desktop', cell: (g) => short(g.attempts) },
    {
      id: 'tokens',
      header: 'Tokens in / out',
      align: 'right',
      only: 'desktop',
      cell: (g) => (
        <span>
          {short(g.tokens.input)} <span className="muted">/</span> {short(g.tokens.output)}
          {g.tokens.unknown_usage_attempts > 0 && (
            <Tooltip label={`${g.tokens.unknown_usage_attempts} requests did not report usage`}>
              <span style={{ color: 'var(--warning)' }}> *</span>
            </Tooltip>
          )}
        </span>
      ),
    },
    {
      id: 'mobile-meta',
      header: 'Usage',
      only: 'mobile',
      mobile: 'meta',
      cell: (g) => `${short(g.attempts)} ${g.attempts === 1 ? 'request' : 'requests'} · ${short(g.tokens.input)} in · ${short(g.tokens.output)} out`,
    },
    {
      id: 'cost',
      header: 'Estimated cost',
      align: 'right',
      mobile: 'aside',
      cell: (g) => {
        const v = g.costs.find((c) => c.kind === 'estimated_api_charge' && c.currency === currency)
        return v ? money(v.amount, v.currency) : <span className="muted">—</span>
      },
    },
    {
      id: 'share',
      header: 'Share',
      width: 140,
      mobile: 'hidden',
      cell: (g) => {
        const pct = totalEst > 0 ? (estimate(g, currency) / totalEst) * 100 : 0
        return (
          <Group gap={8} wrap="nowrap">
            <Progress value={pct} size={6} radius="xl" style={{ flex: 1 }} aria-label={`${g.label || g.key || 'Not set'} share of cost`} />
            <Text size="xs" c="dimmed" w={34} ta="right" className="num">
              {Math.round(pct)}%
            </Text>
          </Group>
        )
      },
    },
  ]

  const setGroup = (choice: string) => {
    if (choice === 'run') set({ group: `ref:${RUN_KEY}` })
    else if (choice === 'ref') set({ group: `ref:${refKey && refKey !== RUN_KEY ? refKey : 'spaceman.task'}` })
    else set({ group: choice as SpendSearch['group'] })
  }
  const choice = groupChoice(groupBy)
  const breakdownError = refKey ? refGroups.error : summary.error
  const breakdownReady = refKey ? !!refGroups.data : !!s

  return (
    <Stack gap={28}>
      <Stack gap="xs">
        <Group gap={6} wrap="wrap" role="search" aria-label="Filter spend">
          {(refs ?? []).map((r) => {
            const label = refLabel(r)
            const rest = refs!.filter((x) => x !== r)
            return (
              <Pill key={r} withRemoveButton onRemove={() => set({ ref: rest.length ? rest : undefined })} removeButtonProps={{ 'aria-label': `Remove ${label}` }}>
                {label}
              </Pill>
            )
          })}
          <RefFilterButton existing={refs ?? []} onAdd={(r) => set({ ref: withRef(refs, r) })} size={compact ? 'md' : 'sm'} />
          {filtered && (
            <Button variant="subtle" size="compact-sm" color="gray" onClick={() => set({ ref: undefined })}>
              Clear
            </Button>
          )}
        </Group>
        {filtered && (
          <Text size="xs" c="dimmed">
            Showing only requests with {refs!.length === 1 ? 'this reference' : 'all these references'}. Provider billing, subscription fees and the chart over time
            can't be narrowed this way, so they are hidden.
          </Text>
        )}
      </Stack>

      {summary.isError ? (
        <QueryError error={summary.error} retry={() => void summary.refetch()} />
      ) : !s || (!filtered && !series.data) ? (
        <StatSkeleton count={4} />
      ) : (
        <Stack gap={6}>
          <Text size="xs" c="dimmed" fw={500}>
            {cap(rangeName[preset] ?? '')}
            {filtered ? ` · ${refs!.map(refLabel).join(' · ')}` : ''}
          </Text>
          <StatGrid
            cols={filtered ? 3 : (billed !== null ? 3 : 1) + (fees > 0 ? 1 : 0) + 1}
            label={`Spend for the ${rangeName[preset]}${filtered ? `, ${refs!.map(refLabel).join(', ')}` : ''}`}
          >
            {billed !== null && (
              <Stat
                label="Billed by providers"
                value={money(billed, currency)}
                hint="What tracked providers charged"
                info={`${costKindHelp.actual_api_charge} Only accounts whose quota tracking reports billing are included. It covers all their usage, also usage that did not go through Harbor Pilot.`}
              />
            )}
            <Stat
              label={billed !== null ? 'Through Harbor Pilot' : 'Estimated cost'}
              value={est ? money(est.amount, est.currency) : '—'}
              hint={billed !== null ? 'Estimated from gateway requests' : 'Gateway requests at list price'}
              info={costKindHelp.estimated_api_charge}
              spark={filtered ? undefined : rows.map((r) => r.costs[`estimated_api_charge|${currency}`] ?? 0)}
            />
            {outside !== null && (
              <Stat
                label="Not through Harbor Pilot"
                value={money(outside, currency)}
                tone={outside > 0 ? 'warning' : 'default'}
                hint={outside > 0 ? 'Billed, but not seen by the gateway' : 'Everything went through the gateway'}
                info="Billed by providers minus the gateway estimate. Usually other tools or keys using the same accounts. See Provider vs gateway below."
              />
            )}
            {fees > 0 && <Stat label="Subscription fees" value={money(fees, currency)} hint="Prorated to the range" info={costKindHelp.subscription_fee} />}
            {filtered && <Stat label="Requests" value={short(requests)} hint="Attempts, fallbacks included" />}
            <Stat
              label="Tokens"
              value={short(tokens.input + tokens.output)}
              hint={`${short(tokens.input)} in · ${short(tokens.output)} out`}
              spark={filtered ? undefined : rows.map((r) => r.input + r.output)}
            />
          </StatGrid>
        </Stack>
      )}

      {!filtered && (
        <Section
          title={`${bucket === 'hour' ? 'Hourly' : bucket === 'day' ? 'Daily' : 'Weekly'} ${mode === 'cost' ? 'cost' : 'tokens'}`}
          actions={
            <SegmentedControl
              size="xs"
              aria-label="Chart"
              value={mode}
              onChange={(v) => set({ view: v as 'cost' | 'tokens' })}
              data={[
                { value: 'cost', label: 'Cost' },
                { value: 'tokens', label: 'Tokens' },
              ]}
            />
          }
        >
          {series.isError ? (
            <QueryError error={series.error} retry={() => void series.refetch()} />
          ) : !series.data ? (
            <Skeleton height={240} radius="md" />
          ) : busyBuckets === 0 ? (
            <Empty icon={<Coins size={22} />} title="No usage in this range" />
          ) : (
            <Paper withBorder radius="md" p="md">
              <UsageChart rows={rows} bucket={bucket} mode={mode} groups={stacked.length > 1 ? stacked : undefined} />
            </Paper>
          )}
        </Section>
      )}

      <Section
        title="Breakdown"
        actions={
          <>
            {compact ? (
              <Select aria-label="Group by" size="md" w={150} allowDeselect={false} value={choice} onChange={(v) => v && setGroup(v)} data={groupChoices} />
            ) : (
              <SegmentedControl size="xs" aria-label="Group by" value={choice} onChange={setGroup} data={groupChoices} />
            )}
            {choice === 'ref' && refKey && <RefKeyInput value={refKey} onChange={(k) => set({ group: `ref:${k}` })} compact={compact} />}
          </>
        }
      >
        {breakdownError ? (
          <QueryError error={breakdownError} retry={() => void (refKey ? refGroups.refetch() : summary.refetch())} />
        ) : !breakdownReady ? (
          <Skeleton height={160} radius="md" />
        ) : (
          <Stack gap="xs">
            <DataTable
              label="Usage by group"
              rows={sortedGroups}
              rowKey={(g) => g.key || '(none)'}
              columns={columns}
              empty={<Empty title={filtered ? 'No matching usage in this range' : 'No usage in this range'} />}
              onRow={(g) => {
                const to = rowSearch(groupBy, g.key, rangeDays, refs)
                if (to) void navigate({ to: '/activity', search: to as never })
              }}
            />
            {refKey && (
              <Group justify="space-between" gap="xs">
                <Text size="xs" c="dimmed">
                  {sortedGroups.length.toLocaleString('en-US')} {sortedGroups.length === 1 ? keyName.toLowerCase() : `${keyName.toLowerCase()} values`} shown
                  {refGroups.hasNextPage ? ', more available' : ''}
                </Text>
                {refGroups.hasNextPage && (
                  <Button variant="default" size="xs" loading={refGroups.isFetchingNextPage} onClick={() => void refGroups.fetchNextPage()}>
                    Load more
                  </Button>
                )}
              </Group>
            )}
          </Stack>
        )}
      </Section>

      {!filtered && <ProviderVsGateway rows={rec.data?.items ?? []} loading={!rec.data} error={rec.error} days={Math.min(days, 90)} />}
    </Stack>
  )
}

/** The reference key to group by: suggestions, or any valid key typed in. */
function RefKeyInput({ value, onChange, compact }: { value: string; onChange: (key: string) => void; compact: boolean }) {
  const options = useRefKeyOptions().filter((o) => o.value !== RUN_KEY)
  const [text, setText] = useState(value)
  useEffect(() => setText(value), [value])
  const commit = (k: string) => {
    const key = k.trim()
    if (REF_KEY.test(key) && key !== value) onChange(key)
    else setText(value)
  }
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        commit(text)
      }}
    >
      <Autocomplete
        aria-label="Reference key"
        placeholder="spaceman.task"
        size={compact ? 'md' : 'xs'}
        w={compact ? 200 : 190}
        value={text}
        onChange={setText}
        onOptionSubmit={commit}
        onBlur={() => commit(text)}
        data={options}
        error={text.trim() && !REF_KEY.test(text.trim()) ? true : undefined}
        comboboxProps={{ withinPortal: true }}
      />
    </form>
  )
}

/** Provider-reported usage next to what the gateway saw, for the page range. */
function ProviderVsGateway({ rows, loading, error, days }: { rows: ReconciliationRow[]; loading: boolean; error: Error | null; days: number }) {
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const ext = useQuery(externalUsageQuery(days))
  const acc = byId(accounts.data?.items)
  const bypass = rows.reduce((n, r) => n + (r.unattributed_input_tokens ?? 0), 0)
  const columns: Column<ReconciliationRow>[] = [
    { id: 'day', header: 'Day', mobile: 'aside', cell: (r) => <span style={{ whiteSpace: 'nowrap' }}>{r.day.slice(0, 10)}{r.partial && <span className="muted"> · so far</span>}</span> },
    { id: 'account', header: 'Account', mobile: 'title', cell: (r) => acc[r.account_id]?.name ?? r.account_id },
    { id: 'provider', header: 'Provider reported', align: 'right', only: 'desktop', cell: (r) => `${short(r.provider_input_tokens)} in` },
    { id: 'gateway', header: 'Gateway saw', align: 'right', only: 'desktop', cell: (r) => `${short(r.gateway_input_tokens)} in` },
    {
      id: 'gap',
      header: (
        <Tooltip label="Input tokens the provider reported that did not go through Harbor Pilot.">
          <span>Not seen by gateway</span>
        </Tooltip>
      ),
      align: 'right',
      mobile: 'line',
      cell: (r) =>
        r.unattributed_input_tokens === null ? (
          <span className="muted">Unknown</span>
        ) : r.unattributed_input_tokens > 0 ? (
          <span style={{ color: 'var(--warning)' }}>{short(r.unattributed_input_tokens)} in</span>
        ) : (
          <span className="muted">None</span>
        ),
    },
    { id: 'billed', header: 'Billed', align: 'right', mobile: 'meta', cell: (r) => (r.provider_actual_cost ? money(r.provider_actual_cost, r.currency) : <span className="muted">—</span>) },
  ]
  const exportCsv = () => {
    const items = ext.data?.items ?? []
    const head = ['interval_start', 'interval_end', 'monitor_id', 'requests', 'input_tokens', 'output_tokens', 'actual_cost', 'estimated_cost', 'currency', 'quality']
    const lines = [head.join(',')].concat(items.map((r) => head.map((k) => JSON.stringify((r as Record<string, unknown>)[k] ?? '')).join(',')))
    const url = URL.createObjectURL(new Blob([lines.join('\n')], { type: 'text/csv' }))
    const a = document.createElement('a')
    a.href = url
    a.download = `provider-usage-${days}d.csv`
    a.click()
    URL.revokeObjectURL(url)
  }
  return (
    <Accordion variant="separated" chevronPosition="right">
      <Accordion.Item value="pvg">
        <Accordion.Control>
          <Stack gap={0}>
            <Text fw={600}>Provider vs gateway</Text>
            <Text size="xs" c={bypass > 0 ? 'yellow' : 'dimmed'}>
              {loading
                ? 'Comparing…'
                : rows.length === 0
                  ? 'No provider reports usage for a linked account.'
                  : bypass > 0
                    ? `Providers reported ${short(bypass)} input tokens the gateway never saw. Usage may be bypassing Harbor Pilot.`
                    : 'Everything providers reported went through the gateway.'}
            </Text>
          </Stack>
        </Accordion.Control>
        <Accordion.Panel>
          {error ? (
            <QueryError error={error} />
          ) : (
            <Stack gap="sm">
              {rows.length > 0 && <DataTable label="Provider vs gateway" rows={rows} rowKey={(r) => r.monitor_id + r.day} columns={columns} sticky={false} />}
              {(ext.data?.items ?? []).length > 0 && (
                <Group justify="flex-end">
                  <Button variant="subtle" size="xs" leftSection={<Download size={14} />} onClick={exportCsv}>
                    Export provider-reported usage (CSV)
                  </Button>
                </Group>
              )}
            </Stack>
          )}
        </Accordion.Panel>
      </Accordion.Item>
    </Accordion>
  )
}
