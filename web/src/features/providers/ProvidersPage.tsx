import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ActionIcon, Button, Drawer, Group, Menu, Paper, Stack, Text, Title } from '@mantine/core'
import { MoreHorizontal, Pencil, Plus, Server, Trash2 } from 'lucide-react'
import { Badge } from '@/components/ui'
import { ConfirmDialog, MutationError } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { ClassificationBadge, ResourceBadges } from '@/components/ResourceBadges'
import { ProviderIcon } from '@/components/ProviderIcon'
import { AccountDialog } from '@/features/config/AccountDialog'
import { EntitlementDialog } from '@/features/config/EntitlementDialog'
import { ProviderDialog } from '@/features/config/ProviderDialog'
import { providerTypes } from '@/features/config/constants'
import { AccountQuota, LimitStatusBadge, type Row as LimitRow, rowsFrom, WindowCell, billingName } from '@/features/limits/LimitsPage'
import { quotaBoardQuery, resourceListQuery, statsQuery } from '@/lib/api/queries'
import { useDeleteResource } from '@/lib/api/resources'
import type { AccountRes, EntitlementRes, ProviderRes } from '@/lib/api/types'
import { serverNow } from '@/lib/format'
import { useCan } from '@/lib/session'
import { type Column, DataTable, Empty, Fields, PageHeader, useCompact } from '@/ui/kit'
import { money, short } from '@/ui/fmt'

function useAccountStats() {
  const to = Math.ceil(serverNow() / 60_000) * 60_000
  const r = { from: new Date(to - 86_400_000).toISOString(), to: new Date(to).toISOString() }
  const q = useQuery({ ...statsQuery(r, 'account', 'hour'), refetchInterval: 60_000 })
  return Object.fromEntries((q.data?.groups ?? []).map((g) => [g.key, g]))
}

function credentialText(a: AccountRes): string {
  if (a.spec.credential_ref) return a.spec.credential_ref.startsWith('env:') ? 'From an environment variable' : 'From a file'
  return a.secrets?.credential?.set ? 'Saved (encrypted)' : 'None'
}

interface AccountRow {
  a: AccountRes
  limit?: LimitRow
}

/** Providers and their accounts: the one list operators think in. */
export function ProvidersPage() {
  const providers = useQuery(resourceListQuery<ProviderRes>('providers'))
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const ents = useQuery(resourceListQuery<EntitlementRes>('subscription-entitlements'))
  const board = useQuery(quotaBoardQuery)
  const stats = useAccountStats()
  const canWrite = useCan('config.write')
  const canSecrets = useCan('secrets.write')
  const compact = useCompact()
  const [editingProvider, setEditingProvider] = useState<ProviderRes | 'new' | null>(null)
  const [editingAccount, setEditingAccount] = useState<{ a: AccountRes | null; provider?: string } | null>(null)
  const [deleting, setDeleting] = useState<{ kind: 'providers' | 'provider-accounts'; r: ProviderRes | AccountRes } | null>(null)
  const [open, setOpen] = useState<string | null>(null)
  const delProvider = useDeleteResource('providers')
  const delAccount = useDeleteResource('provider-accounts')

  const limits = useMemo(() => {
    const out: Record<string, LimitRow> = {}
    if (board.data) for (const r of rowsFrom(board.data.account_health, board.data.monitors, board.data.observed_accounts)) if (r.linked) out[r.key] = r
    return out
  }, [board.data])

  if (providers.isError) return <QueryError error={providers.error} retry={() => void providers.refetch()} />
  const provItems = providers.data?.items ?? []
  const accItems = accounts.data?.items ?? []
  const available = accItems.filter((a) => limits[a.id]?.status === 'available').length
  const opened = accItems.find((a) => a.id === open)

  const cols: Column<AccountRow>[] = [
    {
      id: 'name',
      header: 'Account',
      mobile: 'title',
      cell: ({ a }) => (
        <Stack gap={0} miw={0}>
          <Group gap={6} wrap="nowrap" miw={0}>
            <Text size="sm" fw={500} truncate>
              {a.spec.display_name || a.name}
            </Text>
            <ResourceBadges r={a} />
          </Group>
          <Text size="xs" c="dimmed" truncate>
            {billingName[a.spec.billing] ?? a.spec.billing}
            {a.spec.display_name ? ` · ${a.name}` : ''}
          </Text>
        </Stack>
      ),
    },
    {
      id: 'status',
      header: 'Status',
      mobile: 'aside',
      cell: ({ a, limit }) => (!a.spec.enabled ? <Badge>Off</Badge> : limit ? <LimitStatusBadge row={limit} /> : <Text size="xs" c="dimmed">—</Text>),
    },
    { id: 'window', header: 'Tightest window', mobile: 'line', width: '28%', cell: ({ limit }) => <WindowCell t={limit?.tightest} /> },
    { id: 'requests', header: 'Requests 24h', align: 'right', mobile: 'meta', cell: ({ a }) => <span className="num">{short(stats[a.id]?.requests ?? 0)}</span> },
    {
      id: 'cost',
      header: 'Cost 24h',
      align: 'right',
      mobile: 'meta',
      cell: ({ a }) => {
        const c = stats[a.id]?.costs.find((x) => x.kind === 'estimated_api_charge') ?? stats[a.id]?.costs[0]
        return c ? <span className="num">{money(c.amount, c.currency)}</span> : <Text size="sm" c="dimmed" span>—</Text>
      },
    },
  ]

  return (
    <Stack gap="lg">
      <PageHeader
        title="Providers"
        info="The services Harbor Pilot calls and the accounts it may use at each. Open an account for its quota, subscription and settings."
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
          {provItems.length} {provItems.length === 1 ? 'provider' : 'providers'} · {accItems.length} {accItems.length === 1 ? 'account' : 'accounts'}
          {board.data ? ` · ${available === accItems.length ? 'all available' : `${available} available`}` : ''}
        </Text>
      )}
      <MutationError error={delProvider.error ?? delAccount.error} />
      {!providers.data || !accounts.data ? (
        <TableSkeleton />
      ) : provItems.length === 0 ? (
        <Empty icon={<Server size={22} />} title="No providers yet" action={canWrite && <Button onClick={() => setEditingProvider('new')}>Connect a provider</Button>}>
          Connect an API, a local model server or a subscription through its CLI.
        </Empty>
      ) : (
        provItems.map((p) => {
          const rows = accItems.filter((a) => a.spec.provider_id === p.id).map((a) => ({ a, limit: limits[a.id] }))
          const type = providerTypes.find((t) => t.value === p.spec.type)
          return (
            <section key={p.id} aria-labelledby={`prov-${p.id}`}>
              <Group justify="space-between" gap="sm" wrap="nowrap" mb="xs">
                <Group gap="sm" wrap="nowrap" miw={0}>
                  <ProviderIcon id={p.spec.type} family={p.spec.type} name={p.name} size={20} />
                  <Stack gap={0} miw={0}>
                    <Group gap={6} wrap="nowrap">
                      <Title order={2} id={`prov-${p.id}`}>
                        {p.spec.display_name || p.name}
                      </Title>
                      {p.spec.classification && p.spec.classification !== 'official' && <ClassificationBadge value={p.spec.classification} />}
                      <ResourceBadges r={p} />
                    </Group>
                    <Text size="xs" c="dimmed" truncate>
                      {type?.label ?? p.spec.type}
                      {p.spec.base_url && p.spec.base_url !== type?.defaultBaseURL ? ` · ${p.spec.base_url}` : ''}
                    </Text>
                  </Stack>
                </Group>
                {(canWrite || canSecrets) && (
                  <Menu position="bottom-end" withinPortal>
                    <Menu.Target>
                      <ActionIcon variant="subtle" color="gray" aria-label={`Actions for ${p.name}`}>
                        <MoreHorizontal size={16} />
                      </ActionIcon>
                    </Menu.Target>
                    <Menu.Dropdown>
                      {canSecrets && (
                        <Menu.Item leftSection={<Plus size={14} />} onClick={() => setEditingAccount({ a: null, provider: p.id })}>
                          Add account
                        </Menu.Item>
                      )}
                      {canWrite && (
                        <Menu.Item leftSection={<Pencil size={14} />} onClick={() => setEditingProvider(p)}>
                          Edit provider
                        </Menu.Item>
                      )}
                      {canWrite && (
                        <Menu.Item color="red" leftSection={<Trash2 size={14} />} onClick={() => setDeleting({ kind: 'providers', r: p })}>
                          Delete provider
                        </Menu.Item>
                      )}
                    </Menu.Dropdown>
                  </Menu>
                )}
              </Group>
              {rows.length === 0 ? (
                <Paper withBorder radius="md" p="md">
                  <Group justify="space-between" gap="sm">
                    <Text size="sm" c="dimmed">
                      No accounts yet.
                    </Text>
                    {canSecrets && (
                      <Button size="xs" variant="default" leftSection={<Plus size={12} />} onClick={() => setEditingAccount({ a: null, provider: p.id })}>
                        Add account
                      </Button>
                    )}
                  </Group>
                </Paper>
              ) : (
                <DataTable label={`${p.name} accounts`} rows={rows} columns={cols} rowKey={(r) => r.a.id} onRow={(r) => setOpen(r.a.id)} />
              )}
            </section>
          )
        })
      )}

      <Drawer
        opened={!!opened}
        onClose={() => setOpen(null)}
        position={compact ? 'bottom' : 'right'}
        size={compact ? '100%' : 600}
        closeButtonProps={{ 'aria-label': 'Close' }}
        title={
          opened && (
            <Group gap="sm">
              <Text fw={600}>{opened.spec.display_name || opened.name}</Text>
              {limits[opened.id] && <LimitStatusBadge row={limits[opened.id]!} />}
            </Group>
          )
        }
      >
        {opened && (
          <Stack gap="lg">
            <Group gap="xs">
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
            <Fields
              items={[
                ['Provider', provItems.find((p) => p.id === opened.spec.provider_id)?.name ?? '—'],
                ['How it is paid', billingName[opened.spec.billing] ?? opened.spec.billing],
                ['Credential', credentialText(opened)],
                ['Requests 24h', short(stats[opened.id]?.requests ?? 0)],
                ['Limits', `${opened.spec.max_concurrency || 'Unlimited'} at once · ${opened.spec.requests_per_minute ? `${opened.spec.requests_per_minute}/min` : 'no rate limit'}`],
                ['Models', opened.spec.allowed_models.length ? opened.spec.allowed_models.join(', ') : 'All of the provider’s models'],
              ]}
            />
            <Stack gap="sm">
              <Title order={3}>Quota</Title>
              {limits[opened.id] ? <AccountQuota row={limits[opened.id]!} /> : <Text size="sm" c="dimmed">No quota data.</Text>}
            </Stack>
            <Subscriptions account={opened} ents={(ents.data?.items ?? []).filter((e) => e.spec.account_id === opened.id)} />
          </Stack>
        )}
      </Drawer>

      {editingProvider && <ProviderDialog provider={editingProvider === 'new' ? null : editingProvider} onClose={() => setEditingProvider(null)} />}
      {editingAccount && <AccountDialog account={editingAccount.a} providerID={editingAccount.provider} onClose={() => setEditingAccount(null)} />}
      <ConfirmDialog
        open={!!deleting}
        title={`Delete ${deleting?.kind === 'providers' ? 'provider' : 'account'} ${deleting?.r.name ?? ''}?`}
        message="This cannot be undone. Anything that still uses it must be changed first."
        confirmLabel="Delete"
        onCancel={() => setDeleting(null)}
        onConfirm={() => {
          if (deleting?.kind === 'providers') delProvider.mutate({ id: deleting.r.id, revision: deleting.r.revision })
          else if (deleting) {
            delAccount.mutate({ id: deleting.r.id, revision: deleting.r.revision })
            setOpen(null)
          }
          setDeleting(null)
        }}
      />
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
                <Text size="sm" fw={500}>
                  {e.spec.plan_name || e.name}
                </Text>
                <Text size="xs" c="dimmed">
                  {money(e.spec.fee, e.spec.currency)} per {e.spec.period === 'yearly' ? 'year' : 'month'} · since {e.spec.starts_on}
                  {e.spec.ends_on ? ` until ${e.spec.ends_on}` : ''}
                </Text>
              </Stack>
              {canWrite && (
                <ActionIcon variant="subtle" color="gray" aria-label={`Edit ${e.name}`} onClick={() => setEditing(e)}>
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
