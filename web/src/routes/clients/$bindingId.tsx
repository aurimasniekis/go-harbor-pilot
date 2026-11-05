import { useCallback, useEffect, useState } from 'react'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ActionIcon, Alert, Button, Group, Menu, SimpleGrid, Stack, Switch, Tabs, Text, Tooltip } from '@mantine/core'
import { BookOpen, KeyRound, MoreHorizontal, Power, RefreshCw, Trash2, XCircle } from 'lucide-react'
import { z } from 'zod'
import { Badge, Skeleton } from '@/components/ui'
import { ConfirmDialog, MutationError } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { ResourceBadges } from '@/components/ResourceBadges'
import { BindingForm } from '@/features/config/BindingForm'
import { IssueCredentialDialog } from '@/features/config/IssueCredentialDialog'
import { RecentRequestsPanel } from '@/features/activity/ActivityPanels'
import { groupCost, useClientStats } from '@/features/clients/clients'
import { sortTokens } from '@/features/clients/status'
import { scopeLabel, toolLabel } from '@/features/harness/tools'
import { apiSend } from '@/lib/api/client'
import { credentialsQuery, resourceListQuery, resourceQuery } from '@/lib/api/queries'
import { byId, useDeleteResource, useSaveResource } from '@/lib/api/resources'
import type { BindingRes, BindingSpec, Credential, PlanRes, ProjectRes } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { fmtDateTime, fmtRelative } from '@/lib/format'
import { type Column, DataTable, Empty, PageHeader } from '@/ui/kit'
import { money, short } from '@/ui/fmt'

export const Route = createFileRoute('/clients/$bindingId')({
  validateSearch: z.object({ token: z.boolean().optional().catch(undefined), tab: z.enum(['overview', 'tokens', 'settings']).optional().catch(undefined) }).parse,
  component: ClientDetail,
})

function Tokens({ b, autoIssue, onAutoIssued }: { b: BindingRes; autoIssue: boolean; onAutoIssued: () => void }) {
  const qc = useQueryClient()
  const creds = useQuery(credentialsQuery(b.id))
  const canTokens = useCan('tokens.manage')
  const [issuing, setIssuing] = useState<{ auto?: boolean; replacing?: Credential } | null>(autoIssue ? { auto: true } : null)
  const [revoking, setRevoking] = useState<Credential | null>(null)
  const [showRevoked, setShowRevoked] = useState(false)
  useEffect(() => {
    if (autoIssue) onAutoIssued()
  }, [autoIssue, onAutoIssued])
  const revoke = useMutation({
    mutationFn: (id: string) => apiSend<void>('DELETE', `/client-bindings/${encodeURIComponent(b.id)}/credentials/${encodeURIComponent(id)}`),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['resources'] }),
  })
  const all = sortTokens(creds.data?.items ?? [])
  const revokedCount = all.filter((c) => c.revoked_at).length
  const rows = showRevoked ? all : all.filter((c) => !c.revoked_at)
  const cols: Column<Credential>[] = [
    {
      id: 'name',
      header: 'Token',
      mobile: 'title',
      cell: (cr) => (
        <Stack gap={0} miw={0} opacity={cr.revoked_at ? 0.6 : 1}>
          <Group gap={6} wrap="nowrap">
            <Text size="sm" fw={500} truncate>
              {cr.name}
            </Text>
            {cr.parent_id && <Badge tone="info">Run token</Badge>}
            {cr.revoked_at && <Badge tone="error">Revoked</Badge>}
          </Group>
          <Text size="xs" c="dimmed" ff="monospace">
            hpk_{cr.prefix}_••••
          </Text>
        </Stack>
      ),
    },
    { id: 'scopes', header: 'Permissions', mobile: 'meta', mobileLabel: 'Can', cell: (cr) => cr.scopes.map(scopeLabel).join(', ') },
    {
      id: 'used',
      header: 'Last used',
      mobile: 'meta',
      mobileLabel: 'Last used',
      cell: (cr) => (cr.revoked_at ? `Revoked ${fmtRelative(cr.revoked_at)}` : cr.last_used_at ? fmtRelative(cr.last_used_at) : 'Never'),
    },
    {
      id: 'expires',
      header: 'Expires',
      mobile: 'meta',
      mobileLabel: 'Expires',
      cell: (cr) => (cr.revoked_at ? '—' : cr.expires_at ? fmtDateTime(cr.expires_at) : 'Never'),
    },
    {
      id: 'actions',
      header: <span className="visually-hidden">Actions</span>,
      align: 'right',
      mobile: 'actions',
      width: 56,
      cell: (cr) =>
        !cr.revoked_at && canTokens && !cr.parent_id ? (
          <Menu position="bottom-end" withinPortal>
            <Menu.Target>
              <ActionIcon variant="subtle" color="gray" aria-label={`Actions for ${cr.name}`} data-row-action>
                <MoreHorizontal size={16} />
              </ActionIcon>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Item leftSection={<RefreshCw size={14} />} onClick={() => setIssuing({ replacing: cr })}>
                Replace with a new token…
              </Menu.Item>
              <Menu.Item color="red" leftSection={<XCircle size={14} />} onClick={() => setRevoking(cr)}>
                Revoke
              </Menu.Item>
            </Menu.Dropdown>
          </Menu>
        ) : null,
    },
  ]
  return (
    <Stack gap="md">
      <Group justify="space-between" gap="sm">
        <Text size="sm" c="dimmed">
          Tools send a token as a Bearer token or x-api-key. A token is shown once, when it is created.
        </Text>
        <Group gap="xs">
          {revokedCount > 0 && (
            <Switch size="sm" label={`Show revoked (${revokedCount})`} checked={showRevoked} onChange={(e) => setShowRevoked(e.currentTarget.checked)} />
          )}
          {canTokens && (
            <Button leftSection={<KeyRound size={14} />} onClick={() => setIssuing({})}>
              Create token
            </Button>
          )}
        </Group>
      </Group>
      <MutationError error={revoke.error} />
      {creds.isError ? (
        <QueryError error={creds.error} retry={() => void creds.refetch()} />
      ) : !creds.data ? (
        <TableSkeleton rows={2} />
      ) : (
        <DataTable label="Access tokens" rows={rows} columns={cols} rowKey={(cr) => cr.id}
          empty={
            <Empty title={revokedCount ? 'No active access tokens' : 'No access tokens yet'}
              action={canTokens && <Button onClick={() => setIssuing({})}>Create token</Button>}>
              The tool needs one to send requests. Create it, then follow the steps shown.
            </Empty>
          } />
      )}
      {issuing && <IssueCredentialDialog binding={b} auto={issuing.auto} replacing={issuing.replacing} onClose={() => setIssuing(null)} />}
      <ConfirmDialog
        open={!!revoking}
        title={`Revoke ${revoking?.name ?? ''}?`}
        message="Tools using this token stop working immediately. Run tokens created from it are revoked too."
        confirmLabel="Revoke"
        onCancel={() => setRevoking(null)}
        onConfirm={() => {
          if (revoking) revoke.mutate(revoking.id)
          setRevoking(null)
        }}
      />
    </Stack>
  )
}

/** One label-over-value fact; two per row on phones. */
function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <Stack gap={2} miw={0}>
      <Text size="xs" c="dimmed">
        {label}
      </Text>
      <Text size="sm" component="div" style={{ overflowWrap: 'anywhere' }}>
        {children}
      </Text>
    </Stack>
  )
}

function ClientDetail() {
  const { bindingId } = Route.useParams()
  const { tab, token } = Route.useSearch()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const binding = useQuery(resourceQuery<BindingRes>('client-bindings', bindingId))
  const plans = byId(useQuery(resourceListQuery<PlanRes>('routing-plans')).data?.items)
  const projects = byId(useQuery(resourceListQuery<ProjectRes>('projects')).data?.items)
  const creds = useQuery(credentialsQuery(bindingId))
  const { byClient } = useClientStats()
  const canWrite = useCan('config.write')
  const canActivity = useCan('activity.read')
  const save = useSaveResource<BindingSpec, BindingRes>('client-bindings')
  const del = useDeleteResource('client-bindings')
  const [formDirty, setFormDirty] = useState(false)
  const [confirm, setConfirm] = useState<'off' | 'delete' | null>(null)
  // The one-time token request from "Add client" is consumed at once, so a
  // reload or Back does not issue another token.
  const [autoIssue, setAutoIssue] = useState(!!token)
  const clearToken = useCallback(() => {
    setAutoIssue(false)
    void navigate({ to: '/clients/$bindingId', params: { bindingId }, search: { tab: 'tokens' }, replace: true })
  }, [navigate, bindingId])
  if (binding.isError) return <QueryError error={binding.error} retry={() => void binding.refetch()} back={{ to: '/clients', label: 'Back to clients' }} />
  if (!binding.data) return <Skeleton height={160} />
  const b = binding.data
  const st = byClient[b.id]
  const cost = groupCost(st)
  const plan = plans[b.spec.plan_id]
  const failed = (st?.errors ?? 0) + (st?.blocked ?? 0)
  const activeTokens = (creds.data?.items ?? []).filter((c) => !c.revoked_at).length
  const current = tab ?? (token ? 'tokens' : 'overview')
  const setEnabled = (on: boolean) =>
    save.mutate({ existing: b, body: { name: b.name, spec: { ...b.spec, enabled: on } } }, { onSuccess: () => void qc.invalidateQueries({ queryKey: ['resources'] }) })

  return (
    <Stack gap="lg">
      <PageHeader
        crumbs={[{ label: 'Clients', to: '/clients' }, { label: b.spec.display_name || b.name }]}
        title={
          <Group gap="sm" wrap="wrap" component="span">
            <span>{b.spec.display_name || b.name}</span>
            <ResourceBadges r={b} />
            {!b.spec.enabled && <Badge tone="warning">Off</Badge>}
          </Group>
        }
        context={b.spec.display_name ? <span className="mono">{b.name}</span> : undefined}
        actions={
          <Group gap="xs">
            <Button leftSection={<BookOpen size={14} />} onClick={() => void navigate({ to: '/clients/setup', search: { binding: b.id } })}>
              Set up a tool
            </Button>
            {canWrite && (
              <Menu position="bottom-end" withinPortal>
                <Menu.Target>
                  <ActionIcon variant="default" size="lg" aria-label={`More actions for ${b.spec.display_name || b.name}`}>
                    <MoreHorizontal size={16} />
                  </ActionIcon>
                </Menu.Target>
                <Menu.Dropdown>
                  <Tooltip label="Save or discard your settings changes first" disabled={!formDirty} position="left">
                    <div>
                      <Menu.Item leftSection={<Power size={14} />} disabled={formDirty || save.isPending}
                        onClick={() => (b.spec.enabled ? setConfirm('off') : setEnabled(true))}>
                        {b.spec.enabled ? 'Turn off' : 'Turn on'}
                      </Menu.Item>
                    </div>
                  </Tooltip>
                  <Menu.Item color="red" leftSection={<Trash2 size={14} />} onClick={() => setConfirm('delete')}>
                    Delete client
                  </Menu.Item>
                </Menu.Dropdown>
              </Menu>
            )}
          </Group>
        }
      />
      {!b.spec.enabled && (
        <Alert color="yellow" variant="light" title="This client is off">
          Requests from its tools are rejected until you turn it back on.
        </Alert>
      )}
      {b.spec.enabled && creds.data && activeTokens === 0 && current !== 'tokens' && (
        <Alert color="blue" variant="light" title="Not set up yet">
          It has no access token, so no tool can use it.{' '}
          <Link to="/clients/$bindingId" params={{ bindingId: b.id }} search={{ tab: 'tokens' }}>
            Create a token
          </Link>
          .
        </Alert>
      )}
      <MutationError error={save.error ?? del.error} />
      <SimpleGrid cols={{ base: 2, sm: 3 }} spacing="md" verticalSpacing="sm">
        <Fact label="Tool">{toolLabel(b.spec.expected_harness)}</Fact>
        <Fact label="Routing plan">
          {plan ? <Link to="/routing/plans/$planId" params={{ planId: plan.id }}>{plan.spec.display_name || plan.name}</Link> : 'None'}
        </Fact>
        <Fact label="Project">{b.spec.project_id ? (projects[b.spec.project_id]?.name ?? 'Unknown') : 'None'}</Fact>
        <Fact label="Requests 24h">
          <span className="num">{short(st?.requests ?? 0)}</span>
        </Fact>
        <Fact label="Failed 24h">
          {failed ? <Text span size="sm" c="var(--error-text)" className="num">{failed}</Text> : <span className="num">0</span>}
        </Fact>
        <Fact label="Cost 24h">{cost ? money(cost.amount, cost.currency) : '—'}</Fact>
      </SimpleGrid>
      <Tabs value={current} onChange={(v) => v && void navigate({ to: '/clients/$bindingId', params: { bindingId: b.id }, search: { tab: v as 'overview' | 'tokens' | 'settings' }, replace: true })}>
        <Tabs.List aria-label="Client sections">
          <Tabs.Tab value="overview">Activity</Tabs.Tab>
          <Tabs.Tab value="tokens">Access tokens{creds.data ? ` (${activeTokens})` : ''}</Tabs.Tab>
          <Tabs.Tab value="settings">Settings</Tabs.Tab>
        </Tabs.List>
        <Tabs.Panel value="overview" pt="md">
          {canActivity ? <RecentRequestsPanel bindingId={b.id} /> : <Text c="dimmed">You can’t view requests.</Text>}
        </Tabs.Panel>
        <Tabs.Panel value="tokens" pt="md">
          <Tokens b={b} autoIssue={autoIssue} onAutoIssued={clearToken} />
        </Tabs.Panel>
        <Tabs.Panel value="settings" pt="md">
          <BindingForm key={b.revision} binding={b} onDirtyChange={setFormDirty} onSaved={() => void qc.invalidateQueries({ queryKey: ['resources'] })} />
        </Tabs.Panel>
      </Tabs>
      <ConfirmDialog
        open={confirm === 'off'}
        title={`Turn off ${b.spec.display_name || b.name}?`}
        message="Requests from its tools are rejected until you turn it back on. Its tokens and settings are kept."
        confirmLabel="Turn off"
        onCancel={() => setConfirm(null)}
        onConfirm={() => {
          setConfirm(null)
          setEnabled(false)
        }}
      />
      <ConfirmDialog
        open={confirm === 'delete'}
        title={`Delete ${b.spec.display_name || b.name}?`}
        message={`Its ${activeTokens === 1 ? 'access token stops' : `${activeTokens} access tokens stop`} working immediately and its settings are removed. Past requests stay in Requests.`}
        confirmLabel="Delete client"
        onCancel={() => setConfirm(null)}
        onConfirm={() => {
          setConfirm(null)
          del.mutate({ id: b.id, revision: b.revision }, { onSuccess: () => void navigate({ to: '/clients' }) })
        }}
      />
    </Stack>
  )
}
