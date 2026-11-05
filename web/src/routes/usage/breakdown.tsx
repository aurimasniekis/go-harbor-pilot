import { useMemo, useState } from 'react'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Group, Paper, SegmentedControl, Select, Skeleton, Stack, Switch, Text, UnstyledButton } from '@mantine/core'
import { ArrowDown, BarChart3 } from 'lucide-react'
import { Banner } from '@/components/ui'
import { QueryError, TableSkeleton } from '@/components/states'
import { statsQuery } from '@/lib/api/queries'
import type { StatsGroup, StatsLatency } from '@/lib/api/types'
import { presetRange, stepFor } from '@/features/usage/range'
import { StatsChart } from '@/features/stats/StatsChart'
import { dimensions, errorRate, findCost, metrics, type Metric } from '@/features/stats/stats'
import { type Column, DataTable, Empty, Section, Stat, StatGrid, StatSkeleton, useCompact } from '@/ui/kit'
import { costKindName, money, ms, short } from '@/ui/fmt'
import { plural } from '@/ui/glossary'
import { DEFAULT_RANGE, Route as UsageRoute, type SpendSearch } from './route'

export const Route = createFileRoute('/usage/breakdown')({
  component: BreakdownPage,
})

type Dim = (typeof dimensions)[number]['value']
type SortKey = 'requests' | 'errors' | 'fallbacks' | 'tokens' | 'cost' | 'p95'

/** Most sessions shown in the session breakdown. */
export const SESSION_CAP = 50

const rangeName: Record<number, string> = { 1: 'last 24 hours', 7: 'last 7 days', 30: 'last 30 days', 90: 'last 90 days' }

function lat(l: StatsLatency, p: 'p50' | 'p95'): string {
  return ms(p === 'p50' ? l.p50_ms : l.p95_ms)
}

function pct(v: number | null): string {
  if (v === null) return '—'
  return `${v.toFixed(v > 0 && v < 10 ? 1 : 0)}%`
}

/** Estimated cost of a group in the given currency (0 when unpriced). */
function estimated(g: StatsGroup, currency: string): number {
  return Number(findCost(g.costs, 'estimated_api_charge', currency)?.amount ?? 0)
}

export function sortGroups(groups: StatsGroup[], key: SortKey, currency: string): StatsGroup[] {
  const val = (g: StatsGroup): number => {
    switch (key) {
      case 'errors':
        return g.errors
      case 'fallbacks':
        return g.failovers
      case 'tokens':
        return g.input_tokens + g.output_tokens
      case 'cost':
        return estimated(g, currency)
      case 'p95':
        return g.duration.p95_ms ?? -1
      default:
        return g.requests
    }
  }
  return [...groups].sort((a, b) => val(b) - val(a) || a.label.localeCompare(b.label))
}

function BreakdownPage() {
  const search = UsageRoute.useSearch()
  const navigate = useNavigate()
  const days = search.range ?? DEFAULT_RANGE
  const by: Dim = search.by ?? 'client'
  const metric: Metric = search.metric ?? 'requests'
  const set = (patch: Partial<SpendSearch>) => void navigate({ to: '/usage/breakdown', search: { ...search, ...patch }, replace: true })
  const range = useMemo(() => presetRange(days), [days])
  const step = stepFor(days)
  const series = useQuery(statsQuery(range, 'period', step))
  const breakdown = useQuery(statsQuery(range, by, step))
  const t = series.data?.totals
  const currency = t?.costs?.find((c) => c.kind === 'estimated_api_charge')?.currency ?? 'USD'
  const busy = series.data?.groups.length ?? 0
  return (
    <Stack gap={28}>
      {(series.data?.truncated || breakdown.data?.truncated) && (
        <Banner tone="warning" role="status">
          More than 200,000 requests matched, so these figures cover only the oldest part of the range. Pick a shorter range.
        </Banner>
      )}
      {series.isError ? (
        <QueryError error={series.error} retry={() => void series.refetch()} />
      ) : !series.data || !t ? (
        <StatSkeleton count={4} />
      ) : (
        <Stack gap={6}>
          <Text size="xs" c="dimmed" fw={500}>
            {(rangeName[days] ?? '').replace(/^./, (c) => c.toUpperCase())}
          </Text>
          <StatGrid cols={4} label={`Activity in the ${rangeName[days]}`}>
            <Stat label="Requests" value={short(t.requests)} hint={`${short(t.errors)} failed · ${short(t.blocked)} blocked`} />
            <Stat
              label="Error rate"
              value={pct(errorRate(t))}
              tone={t.errors > 0 ? 'error' : 'default'}
              hint={t.errors > 0 ? plural(t.errors, 'failed request') : 'No failed requests'}
              info="Requests that failed or were cancelled. Requests refused by policy are counted as blocked, not as errors."
            />
            <Stat
              label="Fallbacks"
              value={short(t.failovers)}
              tone={t.failovers > 0 ? 'warning' : 'default'}
              hint={t.failovers > 0 ? `${short(series.data.account_switches)} switched account · ${short(series.data.fallbacks)} changed model` : 'None needed'}
              info="Requests that needed more than one attempt: another account of the same model, or a fallback model."
            />
            <Stat label="Latency p95" value={lat(t.duration, 'p95')} hint={`Median ${lat(t.duration, 'p50')} · first token ${lat(t.ttft, 'p50')}`} />
          </StatGrid>
          {t.unknown_usage > 0 && (
            <Text size="xs" c="yellow">
              {plural(t.unknown_usage, 'request')} did not report token usage; their tokens and cost are unknown, not zero.
            </Text>
          )}
        </Stack>
      )}

      <Section
        title={`${metrics.find((m) => m.value === metric)?.label ?? ''} per ${step}`}
        info="Costs are drawn per category and currency and never added together."
        actions={<SegmentedControl size="xs" aria-label="Chart" value={metric} onChange={(v) => set({ metric: v as Metric })} data={metrics} />}
      >
        {series.isError ? null : !series.data ? (
          <Skeleton height={300} radius="md" />
        ) : busy === 0 ? (
          <Empty icon={<BarChart3 size={22} />} title="No requests in this range" />
        ) : metric === 'cost' && series.data.groups.every((g) => (g.costs ?? []).length === 0) ? (
          <Empty icon={<BarChart3 size={22} />} title="No priced requests in this range">
            Costs appear when the served model has a price. Subscription usage is on the Limits page.
          </Empty>
        ) : (
          <Paper withBorder radius="md" p="md">
            <StatsChart groups={series.data.groups} metric={metric} range={range} step={step} />
          </Paper>
        )}
      </Section>

      {series.data && series.data.unattributed_ledger_entries > 0 && (
        <Text size="xs" c="dimmed">
          {plural(series.data.unattributed_ledger_entries, 'ledger entry', 'ledger entries')} in this range have no client (for example health probes) and
          are not in these figures.
        </Text>
      )}

      <Section
        title="Breakdown"
        actions={
          <Select
            size="xs"
            w={190}
            aria-label="Group by"
            leftSection={
              <Text size="xs" c="dimmed" pl={8}>
                By
              </Text>
            }
            leftSectionWidth={34}
            allowDeselect={false}
            value={by}
            onChange={(v) => v && set({ by: v as Dim })}
            data={dimensions}
          />
        }
      >
        {breakdown.isError ? (
          <QueryError error={breakdown.error} retry={() => void breakdown.refetch()} />
        ) : !breakdown.data ? (
          <TableSkeleton />
        ) : breakdown.data.groups.length === 0 ? (
          <Empty icon={<BarChart3 size={22} />} title="No requests in this range" />
        ) : (
          <BreakdownTable by={by} groups={breakdown.data.groups} currency={currency} />
        )}
      </Section>
    </Stack>
  )
}

/** Where a breakdown row leads: the matching requests. */
function rowLink(by: Dim, key: string): Record<string, string | number> | null {
  switch (by) {
    case 'client':
      return { binding_id: key }
    case 'account':
      return { account_id: key }
    case 'project':
      return { project_id: key }
    case 'session':
      return { session_id: key, range: 'all' }
    case 'harness':
      return { harness: key }
    case 'model':
      return { q: key }
    default:
      return null
  }
}

function SortHeader({ label, k, sort, onSort }: { label: string; k: SortKey; sort: SortKey; onSort: (k: SortKey) => void }) {
  const active = sort === k
  return (
    <UnstyledButton onClick={() => onSort(k)} aria-label={`Sort by ${label}`} aria-pressed={active} style={{ font: 'inherit', color: 'inherit' }}>
      <Group gap={2} wrap="nowrap" justify="flex-end">
        {label}
        {active && <ArrowDown size={12} aria-hidden />}
      </Group>
    </UnstyledButton>
  )
}

function BreakdownTable({ by, groups, currency }: { by: Dim; groups: StatsGroup[]; currency: string }) {
  const navigate = useNavigate()
  const compact = useCompact()
  const [sort, setSort] = useState<SortKey>('requests')
  const [all, setAll] = useState(false)
  const sorted = useMemo(() => sortGroups(groups, sort, currency), [groups, sort, currency])
  const rows = by === 'session' ? sorted.slice(0, SESSION_CAP) : sorted
  const name = dimensions.find((d) => d.value === by)?.label ?? 'Name'
  const sh = (label: string, k: SortKey) => <SortHeader label={label} k={k} sort={sort} onSort={setSort} />
  const columns: Column<StatsGroup>[] = [
    {
      id: 'name',
      header: name,
      mobile: 'title',
      cell: (g) => {
        if (!g.key) return <span className="muted">Not set</span>
        const to = rowLink(by, g.key)
        const text = <span className={by === 'model' || by === 'session' ? 'mono' : undefined}>{g.label || g.key}</span>
        return to ? (
          <Link to="/activity" search={to as never} title={`Requests for ${g.label || g.key}`} style={{ fontWeight: 500 }}>
            {text}
          </Link>
        ) : (
          <span style={{ fontWeight: 500 }}>{text}</span>
        )
      },
    },
    { id: 'requests', header: sh('Requests', 'requests'), align: 'right', mobileLabel: 'Requests', cell: (g) => short(g.requests) },
    {
      id: 'errors',
      header: sh('Errors', 'errors'),
      align: 'right',
      mobileLabel: 'Errors',
      cell: (g) => (g.errors > 0 ? <span style={{ color: 'var(--error-text)' }}>{`${short(g.errors)} · ${pct(errorRate(g))}`}</span> : '0'),
    },
    { id: 'fallbacks', header: sh('Fallbacks', 'fallbacks'), align: 'right', mobileLabel: 'Fallbacks', cell: (g) => short(g.failovers) },
    {
      id: 'tokens',
      header: sh('Tokens in / out', 'tokens'),
      align: 'right',
      mobileLabel: 'Tokens',
      cell: (g) => `${short(g.input_tokens)} / ${short(g.output_tokens)}`,
    },
    {
      id: 'cost',
      header: sh('Estimated cost', 'cost'),
      align: 'right',
      mobile: 'aside',
      cell: (g) => {
        const c = findCost(g.costs, 'estimated_api_charge', currency)
        return c ? money(c.amount, c.currency) : <span className="muted">—</span>
      },
    },
    { id: 'latency', header: sh('Latency p50 / p95', 'p95'), align: 'right', mobileLabel: 'Latency', cell: (g) => `${lat(g.duration, 'p50')} / ${lat(g.duration, 'p95')}` },
  ]
  if (all) {
    columns.push(
      { id: 'blocked', header: 'Blocked', align: 'right', cell: (g) => short(g.blocked) },
      {
        id: 'unknown',
        header: 'No usage reported',
        align: 'right',
        cell: (g) => (g.unknown_usage > 0 ? <span style={{ color: 'var(--warning)' }}>{short(g.unknown_usage)}</span> : '0'),
      },
      {
        id: 'other-costs',
        header: 'Other costs',
        align: 'right',
        cell: (g) => {
          const other = (g.costs ?? []).filter((c) => !(c.kind === 'estimated_api_charge' && c.currency === currency))
          return other.length ? other.map((c) => `${money(c.amount, c.currency)} ${costKindName[c.kind]?.toLowerCase() ?? c.kind}`).join(' · ') : <span className="muted">—</span>
        },
      },
      { id: 'tools', header: 'Tool calls', align: 'right', cell: (g) => short(g.tool_calls) },
    )
    if (by === 'intent') {
      columns.push({
        id: 'classifier',
        header: 'Classifier runs',
        align: 'right',
        cell: (g) => (g.classifier_runs > 0 ? `${short(g.classifier_runs)} · ${short(g.classifier_tokens)} tokens` : '0'),
      })
    }
  }
  return (
    <Stack gap="xs">
      <Group justify="space-between" wrap="nowrap">
        <Text size="xs" c="dimmed">
          {by === 'session' && sorted.length > SESSION_CAP
            ? `The ${SESSION_CAP} busiest of ${sorted.length} sessions`
            : `${plural(sorted.length, name.toLowerCase())} · select a row to see its requests`}
        </Text>
        {!compact && <Switch size="xs" label="More columns" checked={all} onChange={(e) => setAll(e.currentTarget.checked)} />}
      </Group>
      <DataTable
        label={`Breakdown by ${name.toLowerCase()}`}
        rows={rows}
        rowKey={(g) => g.key || '(none)'}
        columns={columns}
        onRow={(g) => {
          const to = g.key ? rowLink(by, g.key) : null
          if (to) void navigate({ to: '/activity', search: to as never })
        }}
      />
    </Stack>
  )
}
