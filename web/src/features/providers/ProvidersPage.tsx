import { useMemo, useState } from 'react'
import { Link, useNavigate, useSearch } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { ActionIcon, Alert, Button, Group, Menu, Paper, Stack, Text, Title } from '@mantine/core'
import { CheckCircle2, MoreHorizontal, Pencil, Plug, Plus, Server, Trash2 } from 'lucide-react'
import { Badge } from '@/components/ui'
import { ConfirmDialog, Dialog, FormDrawer, MutationError } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { ClassificationBadge, ResourceBadges } from '@/components/ResourceBadges'
import { ProviderIcon } from '@/components/ProviderIcon'
import { AccountDialog } from '@/features/config/AccountDialog'
import { EntitlementDialog } from '@/features/config/EntitlementDialog'
import { ProviderDialog } from '@/features/config/ProviderDialog'
import { AccountQuota, type Row as LimitRow, rowsFrom, WindowCell } from '@/features/limits/LimitsPage'
import { quotaBoardQuery, resourceListQuery, statsQuery } from '@/lib/api/queries'
import { useDeleteResource } from '@/lib/api/resources'
import type { AccountRes, EntitlementRes, ProviderRes } from '@/lib/api/types'
import { fmtRelative, serverNow } from '@/lib/format'
import { useCan } from '@/lib/session'
import { type Column, DataTable, Empty, Fields, PageHeader, StatusDot, useCompact } from '@/ui/kit'
import { money, short } from '@/ui/fmt'
import { type AccountCheck, accountChecksQuery, accountState, accountStateView, useTestAccount } from './checks'
import { accountLabel, billingLabel, credentialSource, hasCredential, isBridge, providerLabel, typeLabel } from './labels'

function useAccountStats() {
  const to = Math.ceil(serverNow() / 60_000) * 60_000
  const r = { from: new Date(to - 86_400_000).toISOString(), to: new Date(to).toISOString() }
  const q = useQuery({ ...statsQuery(r, 'account', 'hour'), refetchInterval: 60_000 })
  return Object.fromEntries((q.data?.groups ?? []).map((g) => [g.key, g]))
}

interface AccountRow {
  a: AccountRes
  p?: ProviderRes
  limit?: LimitRow
}

const windowUnits: Record<string, string> = { percent: '%', tokens: 'tokens', requests: 'requests', credits: 'credits', unknown: '' }

function windowText(seconds: number): string {
  if (seconds % 604_800 === 0) return `${seconds / 604_800}-week`
  if (seconds % 86_400 === 0) return `${seconds / 86_400}-day`
  if (seconds % 3_600 === 0) return `${seconds / 3_600}-hour`
  return `${Math.round(seconds / 60)}-minute`
}

/** Monthly fee of a subscription (yearly fees spread over 12 months). */
/** A YYYY-MM-DD date as "5 Nov 2026". */
function day(d: string): string {
  const t = new Date(`${d}T00:00:00`)
  return Number.isNaN(t.getTime()) ? d : t.toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' })
}

function monthly(e: EntitlementRes): number {
  const fee = Number(e.spec.fee) || 0
  return e.spec.period === 'yearly' ? fee / 12 : fee
}

/** Providers and their accounts: the one list operators think in. */
export function ProvidersPage() {
  const search = useSearch({ strict: false }) as { account?: string }
  const navigate = useNavigate()
  const providers = useQuery(resourceListQuery<ProviderRes>('providers'))
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const ents = useQuery(resourceListQuery<EntitlementRes>('subscription-entitlements'))
  const board = useQuery(quotaBoardQuery)
  const checks = useQuery(accountChecksQuery)
  const stats = useAccountStats()
  const canWrite = useCan('config.write')
  const canSecrets = useCan('secrets.write')
  const compact = useCompact()
  const [editingProvider, setEditingProvider] = useState<ProviderRes | 'new' | null>(null)
  const [editingAccount, setEditingAccount] = useState<{ a: AccountRes | null; provider?: string } | null>(null)
  const [deleting, setDeleting] = useState<{ kind: 'providers' | 'provider-accounts'; r: ProviderRes | AccountRes } | null>(null)
  const delProvider = useDeleteResource('providers')
  const delAccount = useDeleteResource('provider-accounts')
  const test = useTestAccount()
  const setOpen = (id: string | null) => void navigate({ to: '/providers', search: id ? { account: id } : {}, replace: true })

  const limits = useMemo(() => {
    const out: Record<string, LimitRow> = {}
    if (board.data) for (const r of rowsFrom(board.data.account_health, board.data.monitors, board.data.observed_accounts)) if (r.linked) out[r.key] = r
    return out
  }, [board.data])
  const checkBy = useMemo(() => Object.fromEntries((checks.data?.items ?? []).map((c) => [c.account_id, c])) as Record<string, AccountCheck>, [checks.data])

  if (providers.isError) return <QueryError error={providers.error} retry={() => void providers.refetch()} />
  const provItems = providers.data?.items ?? []
  const provBy = Object.fromEntries(provItems.map((p) => [p.id, p]))
  const accItems = accounts.data?.items ?? []
  const entItems = ents.data?.items ?? []
  const state = (a: AccountRes) => {
    const p = provBy[a.spec.provider_id]
    const st = stats[a.id]
    const l = limits[a.id]
    return accountState({
      enabled: a.spec.enabled,
      hasCredential: hasCredential(a),
      needsCredential: p?.spec.type !== 'ollama',
      check: checkBy[a.id],
      limitReached: l?.status === 'limit' || l?.status === 'paused',
      requests: st?.requests ?? 0,
      failed: (st?.errors ?? 0) + (st?.blocked ?? 0),
    })
  }
  const working = accItems.filter((a) => ['working', 'checked'].includes(state(a))).length
  const needAttention = accItems.filter((a) => ['no_credential', 'rejected', 'unreachable', 'failing'].includes(state(a))).length
  const monthlyFees = entItems.reduce((sum, e) => sum + monthly(e), 0)
  const feeCurrency = entItems[0]?.spec.currency ?? 'USD'
  const opened = accItems.find((a) => a.id === search.account)
  const openedProvider = opened ? provBy[opened.spec.provider_id] : undefined

  const cols: Column<AccountRow>[] = [
    {
      id: 'name',
      header: 'Account',
      mobile: 'title',
      width: '30%',
      cell: ({ a }) => (
        <Stack gap={0} miw={0}>
          <Group gap={6} wrap="nowrap" miw={0}>
            <Text size="sm" fw={500} truncate>
              {accountLabel(a)}
            </Text>
            <ResourceBadges r={a} />
          </Group>
          <Text size="xs" c="dimmed" truncate>
            {billingLabel(a.spec.billing)}
            {a.spec.display_name ? ` · ${a.name}` : ''}
          </Text>
        </Stack>
      ),
    },
    {
      id: 'status',
      header: 'Status',
      mobile: 'aside',
      width: '18%',
      cell: ({ a, limit }) => {
        const st = state(a)
        const v = accountStateView[st]
        return (
          <Stack gap={0} miw={0}>
            <StatusDot status={v.dot} label={v.label} />
            {st === 'limit' && limit?.statusDetail && (
              <Text size="xs" c="dimmed" truncate>
                {limit.statusDetail}
              </Text>
            )}
          </Stack>
        )
      },
    },
    { id: 'window', header: 'Tightest limit', mobile: 'line', width: '26%', cell: ({ limit }) => <WindowCell t={limit?.tightest} /> },
    {
      id: 'requests',
      header: 'Requests 24h',
      align: 'right',
      mobile: 'meta',
      mobileLabel: 'Requests',
      width: '12%',
      cell: ({ a }) => <span className="num">{short(stats[a.id]?.requests ?? 0)}</span>,
    },
    {
      id: 'cost',
      header: 'Cost 24h',
      align: 'right',
      mobile: 'meta',
      mobileLabel: 'Cost',
      width: '14%',
      cell: ({ a }) => {
        const c = stats[a.id]?.costs.find((x) => x.kind === 'estimated_api_charge') ?? stats[a.id]?.costs[0]
        return c ? <span className="num">{money(c.amount, c.currency)}</span> : <Text size="sm" c="dimmed" span>—</Text>
      },
    },
  ]

  const deletingProvider = deleting?.kind === 'providers' ? (deleting.r as ProviderRes) : null
  const dependants = deletingProvider ? accItems.filter((a) => a.spec.provider_id === deletingProvider.id) : []

  return (
    <Stack gap="lg">
      <PageHeader
        title="Providers"
        info="The services Harbor Pilot calls and the accounts it may use at each. Open an account for its status, quota, subscription and settings."
        actions={
          canWrite && (
            <Button leftSection={<Plus size={14} />} onClick={() => setEditingProvider('new')}>
              Connect a provider
            </Button>
          )
        }
      />
      {accounts.data && provItems.length > 0 && (
        <Text size="sm" c="dimmed">
          {provItems.length} {provItems.length === 1 ? 'provider' : 'providers'} · {accItems.length} {accItems.length === 1 ? 'account' : 'accounts'} · {working}{' '}
          working
          {needAttention ? ` · ${needAttention} need attention` : ''}
          {entItems.length ? ` · ${entItems.length} ${entItems.length === 1 ? 'subscription' : 'subscriptions'}${monthlyFees > 0 ? ` (${money(monthlyFees.toFixed(2), feeCurrency)}/month)` : ''}` : ''}
        </Text>
      )}
      <MutationError error={delProvider.error ?? delAccount.error} />
      {!providers.data || !accounts.data ? (
        <TableSkeleton />
      ) : provItems.length === 0 ? (
        <Empty icon={<Server size={22} />} title="No providers yet" action={canWrite && <Button onClick={() => setEditingProvider('new')}>Connect a provider</Button>}>
          Connect an API key, a local model server or a subscription through its CLI.
        </Empty>
      ) : (
        provItems.map((p) => {
          const rows = accItems.filter((a) => a.spec.provider_id === p.id).map((a) => ({ a, p, limit: limits[a.id] }))
          const type = typeLabel(p.spec.type)
          return (
            <section key={p.id} aria-labelledby={`prov-${p.id}`}>
              <Group justify="space-between" gap="sm" wrap="nowrap" mb="xs">
                <Group gap="sm" wrap="nowrap" miw={0}>
                  <ProviderIcon id={p.spec.type} family={p.spec.type} name={p.name} size={20} />
                  <Stack gap={0} miw={0}>
                    <Group gap={6} wrap="nowrap">
                      <Title order={2} id={`prov-${p.id}`}>
                        {providerLabel(p)}
                      </Title>
                      {p.spec.classification && p.spec.classification !== 'official' && <ClassificationBadge value={p.spec.classification} />}
                      <ResourceBadges r={p} />
                    </Group>
                    <Text size="xs" c="dimmed" truncate>
                      {type}
                      {!compact && p.spec.base_url ? ` · ${p.spec.base_url}` : ''}
                    </Text>
                  </Stack>
                </Group>
                <Group gap={4} wrap="nowrap">
                  {canSecrets && rows.length > 0 && (
                    <Button size="xs" variant="subtle" leftSection={<Plus size={12} />} onClick={() => setEditingAccount({ a: null, provider: p.id })}>
                      Add account
                    </Button>
                  )}
                  {canWrite && (
                    <Menu position="bottom-end" withinPortal>
                      <Menu.Target>
                        <ActionIcon variant="subtle" color="gray" aria-label={`Actions for ${providerLabel(p)}`}>
                          <MoreHorizontal size={16} />
                        </ActionIcon>
                      </Menu.Target>
                      <Menu.Dropdown>
                        <Menu.Item leftSection={<Pencil size={14} />} onClick={() => setEditingProvider(p)}>
                          Edit provider
                        </Menu.Item>
                        <Menu.Item color="red" leftSection={<Trash2 size={14} />} onClick={() => setDeleting({ kind: 'providers', r: p })}>
                          Delete provider
                        </Menu.Item>
                      </Menu.Dropdown>
                    </Menu>
                  )}
                </Group>
              </Group>
              {rows.length === 0 ? (
                <Paper withBorder radius="md" p="md">
                  <Group justify="space-between" gap="sm">
                    <Text size="sm" c="dimmed">
                      {isBridge(p.spec.type)
                        ? 'No accounts yet. Add one with the sign-in token of a subscription.'
                        : 'No accounts yet. Add one with an API key to use this provider.'}
                    </Text>
                    {canSecrets && (
                      <Button size="xs" leftSection={<Plus size={12} />} onClick={() => setEditingAccount({ a: null, provider: p.id })}>
                        Add account
                      </Button>
                    )}
                  </Group>
                </Paper>
              ) : (
                <DataTable label={`${providerLabel(p)} accounts`} rows={rows} columns={cols} rowKey={(r) => r.a.id} onRow={(r) => setOpen(r.a.id)} fixed />
              )}
            </section>
          )
        })
      )}

      <FormDrawer
        open={!!opened}
        onClose={() => setOpen(null)}
        size="lg"
        title={
          opened && (
            <Group gap="sm">
              <Text fw={600}>{accountLabel(opened)}</Text>
              <StatusDot status={accountStateView[state(opened)].dot} label={accountStateView[state(opened)].label} />
            </Group>
          )
        }
      >
        {opened && (
          <Stack gap="lg">
            <Group gap="xs">
              {canWrite && (
                <Button size="xs" leftSection={<Plug size={12} />} loading={test.isPending && test.variables === opened.id} onClick={() => test.mutate(opened.id)}>
                  Test connection
                </Button>
              )}
              {canSecrets && (
                <Button size="xs" variant="default" leftSection={<Pencil size={12} />} onClick={() => setEditingAccount({ a: opened })}>
                  Edit account
                </Button>
              )}
              {canWrite && (
                <Button size="xs" variant="subtle" color="red" leftSection={<Trash2 size={12} />} onClick={() => setDeleting({ kind: 'provider-accounts', r: opened })}>
                  Delete
                </Button>
              )}
            </Group>
            <CheckResult check={checkBy[opened.id]} error={test.variables === opened.id ? test.error : null} />
            <Fields
              items={[
                ['Provider', providerLabel(openedProvider)],
                ['How it is paid', billingLabel(opened.spec.billing)],
                ['Credential', credentialSource(opened)],
                ['Requests 24h', short(stats[opened.id]?.requests ?? 0)],
                [
                  'Limits',
                  opened.spec.max_concurrency || opened.spec.requests_per_minute
                    ? [opened.spec.max_concurrency ? `${opened.spec.max_concurrency} at once` : '', opened.spec.requests_per_minute ? `${opened.spec.requests_per_minute} per minute` : '']
                        .filter(Boolean)
                        .join(' · ')
                    : 'None',
                ],
                ['Models', opened.spec.allowed_models.length ? opened.spec.allowed_models.join(', ') : 'All of the provider’s models'],
              ]}
            />
            <Stack gap="sm">
              <Title order={3}>Quota</Title>
              {limits[opened.id] && (limits[opened.id]!.monitors.length > 0 || limits[opened.id]!.observed.length > 0) ? (
                <AccountQuota row={limits[opened.id]!} />
              ) : (
                <DeclaredWindows ents={entItems.filter((e) => e.spec.account_id === opened.id)} />
              )}
            </Stack>
            <Subscriptions account={opened} ents={entItems.filter((e) => e.spec.account_id === opened.id)} />
          </Stack>
        )}
      </FormDrawer>

      {editingProvider && (
        <ProviderDialog
          provider={editingProvider === 'new' ? null : editingProvider}
          onClose={() => setEditingProvider(null)}
          onCreated={(p, acc) => {
            setEditingProvider(null)
            if (acc) {
              setOpen(acc.id)
              test.mutate(acc.id)
            } else if (canSecrets) setEditingAccount({ a: null, provider: p.id })
          }}
        />
      )}
      {editingAccount && (
        <AccountDialog
          account={editingAccount.a}
          providerID={editingAccount.provider}
          onClose={() => setEditingAccount(null)}
          onSaved={(a, created) => {
            setEditingAccount(null)
            if (created) {
              setOpen(a.id)
              test.mutate(a.id)
            }
          }}
        />
      )}
      <ConfirmDialog
        open={!!deleting && (deleting.kind !== 'providers' || dependants.length === 0)}
        title={`Delete ${deleting?.kind === 'providers' ? 'provider' : 'account'} ${deleting ? (deleting.kind === 'providers' ? providerLabel(deleting.r as ProviderRes) : accountLabel(deleting.r as AccountRes)) : ''}?`}
        message={
          deleting?.kind === 'providers'
            ? 'Routing stops using it. Fallback groups that list its models must be changed first.'
            : 'Routing stops using this account and its saved credential is removed. Fallback groups that list it must be changed first.'
        }
        confirmLabel="Delete"
        onCancel={() => setDeleting(null)}
        onConfirm={() => {
          if (deleting?.kind === 'providers') delProvider.mutate({ id: deleting.r.id, revision: deleting.r.revision })
          else if (deleting) delAccount.mutate({ id: deleting.r.id, revision: deleting.r.revision }, { onSuccess: () => setOpen(null) })
          setDeleting(null)
        }}
      />
      <Dialog
        open={!!deletingProvider && dependants.length > 0}
        title={`${providerLabel(deletingProvider ?? undefined)} still has accounts`}
        onClose={() => setDeleting(null)}
        footer={
          <Button variant="default" onClick={() => setDeleting(null)}>
            Close
          </Button>
        }
      >
        <Text size="sm">
          Delete its {dependants.length === 1 ? 'account' : `${dependants.length} accounts`} first: {dependants.map(accountLabel).join(', ')}.
        </Text>
      </Dialog>
    </Stack>
  )
}

/** The latest "Test connection" result, in plain words. */
function CheckResult({ check, error }: { check?: AccountCheck; error: unknown }) {
  if (error) return <MutationError error={error} />
  if (!check) {
    return (
      <Text size="sm" c="dimmed">
        Not tested yet. Test the connection to check the address and credential without sending a request to a model.
      </Text>
    )
  }
  const ok = check.status === 'ok'
  const color = ok ? 'green' : check.status === 'not_verified' ? 'blue' : 'red'
  const title = ok ? 'Connection works' : check.status === 'not_verified' ? 'Ready to use' : 'Connection failed'
  return (
    <Alert color={color} variant="light" title={title} icon={ok ? <CheckCircle2 size={16} /> : undefined}>
      <Text size="sm">
        {check.message.charAt(0).toUpperCase() + check.message.slice(1)}.{check.latency_ms ? ` Answered in ${check.latency_ms} ms.` : ''}
      </Text>
      <Text size="xs" c="dimmed" mt={4}>
        Checked {fmtRelative(check.checked_at)}
      </Text>
    </Alert>
  )
}

/** Quota windows a subscription declares, shown until a tracker reports live usage. */
function DeclaredWindows({ ents }: { ents: EntitlementRes[] }) {
  const windows = ents.flatMap((e) => e.spec.windows ?? [])
  return (
    <Stack gap={6}>
      {windows.length > 0 ? (
        <Text size="sm">
          Declared by its subscription:{' '}
          {windows.map((w) => `${windowText(w.seconds)} window${w.limit ? ` (${w.limit}${windowUnits[w.unit] ?? ''})` : ''}`).join(', ')}. Live usage is not tracked
          yet.
        </Text>
      ) : (
        <Text size="sm" c="dimmed">
          No quota is tracked for this account.
        </Text>
      )}
      <Group>
        <Button component={Link} to="/quotas/monitors" size="xs" variant="default" leftSection={<Plus size={12} />}>
          Add quota tracking
        </Button>
      </Group>
    </Stack>
  )
}

/** Subscriptions recorded for one account (fee, plan, windows). */
function Subscriptions({ account, ents }: { account: AccountRes; ents: EntitlementRes[] }) {
  const canWrite = useCan('config.write')
  const [editing, setEditing] = useState<EntitlementRes | 'new' | null>(null)
  return (
    <Stack gap="sm">
      <Group justify="space-between" gap="xs">
        <Title order={3}>Subscription</Title>
        {canWrite && (
          <Button size="xs" variant="default" leftSection={<Plus size={12} />} onClick={() => setEditing('new')}>
            Add subscription
          </Button>
        )}
      </Group>
      {ents.length === 0 ? (
        <Text size="sm" c="dimmed">
          None recorded. Record a subscription to count its fee separately from usage charges.
        </Text>
      ) : (
        ents.map((e) => (
          <Paper key={e.id} withBorder radius="md" p="sm">
            <Group justify="space-between" gap="sm" wrap="nowrap">
              <Stack gap={0}>
                <Group gap={6}>
                  <Text size="sm" fw={500}>
                    {e.spec.plan_name || e.name}
                  </Text>
                  {e.spec.ends_on && e.spec.ends_on < new Date().toISOString().slice(0, 10) && <Badge>Ended</Badge>}
                </Group>
                <Text size="xs" c="dimmed">
                  {money(e.spec.fee, e.spec.currency)} per {e.spec.period === 'yearly' ? 'year' : 'month'} · since {day(e.spec.starts_on)}
                  {e.spec.ends_on ? ` until ${day(e.spec.ends_on)}` : ''}
                </Text>
              </Stack>
              {canWrite && (
                <ActionIcon variant="subtle" color="gray" aria-label={`Edit ${e.spec.plan_name || e.name}`} onClick={() => setEditing(e)}>
                  <Pencil size={14} />
                </ActionIcon>
              )}
            </Group>
          </Paper>
        ))
      )}
      {editing && <EntitlementDialog ent={editing === 'new' ? null : editing} accountID={account.id} onClose={() => setEditing(null)} />}
    </Stack>
  )
}
