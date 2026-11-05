import { useMemo, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Accordion, Badge, Button, Group, Paper, Progress, SegmentedControl, Skeleton, Stack, Text, Tooltip } from '@mantine/core'
import { Coins, Download } from 'lucide-react'
import { QueryError } from '@/components/states'
import { externalUsageQuery, reconciliationQuery, resourceListQuery, usageSeriesQuery, usageSummaryQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { AccountRes, ReconciliationRow, UsageGroup, UsageGroupBy } from '@/lib/api/types'
import { presetRange } from '@/features/usage/range'
import { bucketFor, bucketize, stepFor } from '@/features/usage/buckets'
import { UsageChart } from '@/features/usage/UsageCharts'
import { type Column, DataTable, Empty, Section, Stat, StatGrid, StatSkeleton } from '@/ui/kit'
import { costKindHelp, money, short } from '@/ui/fmt'
import { Route as UsageRoute } from './route'

export const Route = createFileRoute('/usage/')({
  component: SpendPage,
})

const groupOptions: { value: UsageGroupBy; label: string }[] = [
  { value: 'account', label: 'Account' },
  { value: 'binding', label: 'Client' },
  { value: 'model', label: 'Model' },
  { value: 'project', label: 'Project' },
]

const cap = (t: string) => t.charAt(0).toUpperCase() + t.slice(1)
const rangeName: Record<string, string> = { '1': 'last 24 hours', '7': 'last 7 days', '30': 'last 30 days', '90': 'last 90 days' }

/** Estimated cost of a group in its main currency (the one shown on the page). */
function estimate(g: UsageGroup, currency: string): number {
  return Number(g.costs.find((c) => c.kind === 'estimated_api_charge' && c.currency === currency)?.amount ?? 0)
}

function SpendPage() {
  const { range: rangeDays = 30 } = UsageRoute.useSearch()
  const preset = String(rangeDays)
  const [groupBy, setGroupBy] = useState<UsageGroupBy>('account')
  const [mode, setMode] = useState<'cost' | 'tokens'>('cost')
  const days = Number(preset)
  const bucket = bucketFor(days)
  const range = useMemo(() => presetRange(days), [days])
  const summary = useQuery(usageSummaryQuery(range, groupBy))
  const series = useQuery(usageSeriesQuery(range, stepFor(bucket), groupBy))
  const rec = useQuery(reconciliationQuery(Math.min(days, 90)))
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
  const tokens = useMemo(() => rows.reduce((t, r) => ({ input: t.input + r.input, output: t.output + r.output }), { input: 0, output: 0 }), [rows])
  const s = summary.data
  const est = s?.totals.find((c) => c.kind === 'estimated_api_charge')
  const currency = est?.currency ?? 'USD'
  const billed = useMemo(() => {
    let sum = 0
    let any = false
    for (const r of rec.data?.items ?? []) {
      if (r.provider_actual_cost && r.currency === currency) {
        sum += Number(r.provider_actual_cost)
        any = true
      }
    }
    return any ? sum : null
  }, [rec.data, currency])
  const fees = useMemo(() => (s?.subscription_fees ?? []).reduce((n, f) => n + (f.currency === currency ? Number(f.prorated) : 0), 0), [s, currency])
  const busyBuckets = rows.filter((r) => r.attempts > 0).length
  const totalEst = (s?.groups ?? []).reduce((n, g) => n + estimate(g, currency), 0)

  const groupLabel = groupOptions.find((g) => g.value === groupBy)?.label ?? 'Name'
  const columns: Column<UsageGroup>[] = [
    {
      id: 'name',
      header: groupLabel,
      mobile: 'title',
      cell: (g) => (
        <span style={{ display: 'inline-flex', gap: 6, alignItems: 'center', minWidth: 0 }}>
          <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontWeight: 500 }}>{g.label || <span className="muted">Not set</span>}</span>
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
            <Progress value={pct} size={6} radius="xl" style={{ flex: 1 }} aria-label={`${g.label} share of cost`} />
            <Text size="xs" c="dimmed" w={34} ta="right" className="num">
              {Math.round(pct)}%
            </Text>
          </Group>
        )
      },
    },
  ]

  return (
    <Stack gap={28}>
      {summary.isError ? (
        <QueryError error={summary.error} retry={() => void summary.refetch()} />
      ) : !s || !series.data ? (
        <StatSkeleton count={4} />
      ) : (
        <StatGrid cols={fees > 0 ? 4 : 3} label={`Spend for the ${rangeName[preset]}`}>
          <Stat label="Estimated cost" value={est ? money(est.amount, est.currency) : '—'} hint={cap(rangeName[preset] ?? '')} info={costKindHelp.estimated_api_charge} spark={rows.map((r) => r.costs[`estimated_api_charge|${currency}`] ?? 0)} />
          <Stat
            label="Billed by providers"
            value={billed === null ? '—' : money(billed, currency)}
            hint={billed === null ? 'No provider billing data' : cap(rangeName[preset] ?? '')}
            info={`${costKindHelp.actual_api_charge} Only accounts with quota tracking that reports billing are included.`}
          />
          {fees > 0 && <Stat label="Subscription fees" value={money(fees, currency)} hint="Prorated to the range" info={costKindHelp.subscription_fee} />}
          <Stat label="Tokens" value={short(tokens.input + tokens.output)} hint={`${short(tokens.input)} in · ${short(tokens.output)} out`} spark={rows.map((r) => r.input + r.output)} />
        </StatGrid>
      )}

      <Section
        title={mode === 'cost' ? `Cost per ${bucket}` : `Tokens per ${bucket}`}
        actions={
          <SegmentedControl
            size="xs"
            aria-label="Chart"
            value={mode}
            onChange={(v) => setMode(v as typeof mode)}
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
        ) : busyBuckets < 3 ? (
          <Paper withBorder radius="md" p="md">
            <Text size="sm" c="dimmed">
              Not enough data for a trend yet: usage on {busyBuckets} {busyBuckets === 1 ? bucket : `${bucket}s`} so far.
            </Text>
          </Paper>
        ) : (
          <Paper withBorder radius="md" p="md">
            <UsageChart rows={rows} bucket={bucket} mode={mode} groups={stacked.length > 1 ? stacked : undefined} />
          </Paper>
        )}
      </Section>

      <Section
        title="Breakdown"
        actions={
          <SegmentedControl size="xs" aria-label="Group by" value={groupBy} onChange={(v) => setGroupBy(v as UsageGroupBy)} data={groupOptions} />
        }
      >
        {!s ? (
          <Skeleton height={160} radius="md" />
        ) : (
          <DataTable label="Usage by group" rows={s.groups} rowKey={(g) => g.key || '(none)'} columns={columns} empty={<Empty title="No usage in this range" />} />
        )}
      </Section>

      <ProviderVsGateway rows={rec.data?.items ?? []} loading={!rec.data} error={rec.error} days={Math.min(days, 90)} />
    </Stack>
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
