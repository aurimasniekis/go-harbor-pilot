import { useState } from 'react'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Anchor, Button, Group, Stack, Switch, Tabs, Text, Title } from '@mantine/core'
import { BookOpen, KeyRound, XCircle } from 'lucide-react'
import { z } from 'zod'
import { Badge, Skeleton } from '@/components/ui'
import { ConfirmButton, MutationError } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { ResourceBadges } from '@/components/ResourceBadges'
import { BindingForm } from '@/features/config/BindingForm'
import { IssueCredentialDialog } from '@/features/config/IssueCredentialDialog'
import { RecentRequestsPanel } from '@/features/activity/ActivityPanels'
import { groupCost, useClientStats } from '@/features/clients/clients'
import { apiSend } from '@/lib/api/client'
import { credentialsQuery, resourceListQuery, resourceQuery } from '@/lib/api/queries'
import { byId, useSaveResource } from '@/lib/api/resources'
import type { BindingRes, BindingSpec, Credential, PlanRes, ProjectRes } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { fmtDateTime, fmtRelative } from '@/lib/format'
import { type Column, DataTable, Empty, Fields } from '@/ui/kit'
import { harnessName, money, short } from '@/ui/fmt'

export const Route = createFileRoute('/clients/$bindingId')({
  validateSearch: z.object({ token: z.boolean().optional().catch(undefined), tab: z.enum(['overview', 'tokens', 'settings']).optional().catch(undefined) }).parse,
  component: ClientDetail,
})

const scopeName: Record<string, string> = { 'gateway:invoke': 'Send requests', 'gateway:models': 'List models', 'usage:read': 'Read own usage', 'tokens:mint': 'Create run tokens' }

function Tokens({ b }: { b: BindingRes }) {
  const qc = useQueryClient()
  const creds = useQuery(credentialsQuery(b.id))
  const canTokens = useCan('tokens.manage')
  const { token } = Route.useSearch()
  const navigate = useNavigate()
  const [issuing, setIssuing] = useState(!!token)
  const revoke = useMutation({
    mutationFn: (id: string) => apiSend<void>('DELETE', `/client-bindings/${encodeURIComponent(b.id)}/credentials/${encodeURIComponent(id)}`),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['resources'] }),
  })
  const cols: Column<Credential>[] = [
    {
      id: 'name',
      header: 'Token',
      mobile: 'title',
      cell: (cr) => (
        <Stack gap={0} miw={0}>
          <Group gap={6} wrap="nowrap">
            <Text size="sm" fw={500} truncate>
              {cr.name}
            </Text>
            {cr.parent_id && <Badge tone="info">Run token</Badge>}
            {cr.revoked_at && <Badge tone="error">Revoked</Badge>}
          </Group>
          <Text size="xs" c="dimmed" ff="monospace">
            hpk_{cr.prefix}_…
          </Text>
        </Stack>
      ),
    },
    { id: 'scopes', header: 'Can', mobile: 'meta', cell: (cr) => cr.scopes.map((sc) => scopeName[sc] ?? sc).join(', ') },
    { id: 'used', header: 'Last used', mobile: 'meta', cell: (cr) => (cr.last_used_at ? fmtRelative(cr.last_used_at) : 'Never') },
    { id: 'expires', header: 'Expires', mobile: 'meta', cell: (cr) => (cr.expires_at ? fmtDateTime(cr.expires_at) : 'Never') },
    {
      id: 'revoke',
      header: <span className="visually-hidden">Actions</span>,
      align: 'right',
      mobile: 'aside',
      cell: (cr) =>
        !cr.revoked_at && canTokens ? (
          <ConfirmButton size="small" icon={XCircle} title={`Revoke ${cr.name}?`} label={`Revoke ${cr.name}`}
            message="Tools using this token stop working immediately. Run tokens created from it are revoked too." confirmLabel="Revoke"
            onConfirm={() => revoke.mutate(cr.id)}>
            Revoke
          </ConfirmButton>
        ) : null,
    },
  ]
  return (
    <Stack gap="md">
      <Group justify="space-between" gap="sm">
        <Text size="sm" c="dimmed">
          Tools send a token as a Bearer token or x-api-key. It is shown once when created.
        </Text>
        {canTokens && (
          <Button leftSection={<KeyRound size={14} />} onClick={() => setIssuing(true)}>
            Create token
          </Button>
        )}
      </Group>
      <MutationError error={revoke.error} />
      {creds.isError ? (
        <QueryError error={creds.error} retry={() => void creds.refetch()} />
      ) : !creds.data ? (
        <TableSkeleton rows={2} />
      ) : (
        <DataTable label="Access tokens" rows={creds.data.items} columns={cols} rowKey={(cr) => cr.id}
          empty={<Empty title="No access tokens yet">Create one and give it to the tool with its setup command.</Empty>} />
      )}
      {issuing && (
        <IssueCredentialDialog
          bindingID={b.id}
          onClose={() => {
            setIssuing(false)
            if (token) void navigate({ to: '/clients/$bindingId', params: { bindingId: b.id }, search: { tab: 'tokens' }, replace: true })
          }}
        />
      )}
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
  const { byClient } = useClientStats()
  const canWrite = useCan('config.write')
  const canActivity = useCan('activity.read')
  const save = useSaveResource<BindingSpec, BindingRes>('client-bindings')
  if (binding.isError) return <QueryError error={binding.error} retry={() => void binding.refetch()} />
  if (!binding.data) return <Skeleton height={160} />
  const b = binding.data
  const st = byClient[b.id]
  const cost = groupCost(st)
  const plan = plans[b.spec.plan_id]
  const current = tab ?? (token ? 'tokens' : 'overview')

  return (
    <Stack gap="lg">
      <Stack gap={4}>
        <Text size="sm">
          <Anchor component={Link} to="/clients" c="dimmed">
            Clients
          </Anchor>{' '}
          <Text span c="dimmed">
            /
          </Text>
        </Text>
        <Group justify="space-between" align="flex-end" gap="sm" wrap="wrap">
          <Stack gap={0} miw={0}>
            <Group gap="sm" wrap="nowrap">
              <Title order={1}>{b.spec.display_name || b.name}</Title>
              <ResourceBadges r={b} />
              {!b.spec.enabled && <Badge>Off</Badge>}
            </Group>
            {b.spec.display_name && (
              <Text size="sm" c="dimmed" ff="monospace">
                {b.name}
              </Text>
            )}
          </Stack>
          <Group gap="sm">
            {canWrite && (
              <Switch
                label="Enabled"
                checked={b.spec.enabled}
                disabled={save.isPending}
                onChange={(e) =>
                  save.mutate(
                    { existing: b, body: { name: b.name, spec: { ...b.spec, enabled: e.currentTarget.checked } } },
                    { onSuccess: () => void qc.invalidateQueries({ queryKey: ['resources'] }) },
                  )
                }
              />
            )}
            <Button leftSection={<BookOpen size={14} />} onClick={() => void navigate({ to: '/clients/setup', search: { binding: b.id } })}>
              Set up a tool
            </Button>
          </Group>
        </Group>
      </Stack>
      <MutationError error={save.error} />
      <Fields
        cols={3}
        items={[
          ['Tool', harnessName[b.spec.expected_harness] ?? 'Any tool'],
          ['Routing plan', plan ? <Link to="/routing/plans/$planId" params={{ planId: plan.id }}>{plan.spec.display_name || plan.name}</Link> : '—'],
          ['Project', b.spec.project_id ? (projects[b.spec.project_id]?.name ?? '—') : 'None'],
          ['Requests 24h', <span className="num">{short(st?.requests ?? 0)}</span>],
          ['Failed 24h', <span className="num">{(st?.errors ?? 0) + (st?.blocked ?? 0)}</span>],
          ['Cost 24h', cost ? money(cost.amount, cost.currency) : '—'],
        ]}
      />
      <Tabs value={current} onChange={(v) => v && void navigate({ to: '/clients/$bindingId', params: { bindingId: b.id }, search: { tab: v as 'overview' | 'tokens' | 'settings' }, replace: true })}>
        <Tabs.List aria-label="Client sections">
          <Tabs.Tab value="overview">Overview</Tabs.Tab>
          <Tabs.Tab value="tokens">Access tokens</Tabs.Tab>
          <Tabs.Tab value="settings">Settings</Tabs.Tab>
        </Tabs.List>
        <Tabs.Panel value="overview" pt="md">
          {canActivity ? <RecentRequestsPanel bindingId={b.id} /> : <Text c="dimmed">You can’t view requests.</Text>}
        </Tabs.Panel>
        <Tabs.Panel value="tokens" pt="md">
          <Tokens b={b} />
        </Tabs.Panel>
        <Tabs.Panel value="settings" pt="md">
          <BindingForm key={b.revision} binding={b} onSaved={() => void qc.invalidateQueries({ queryKey: ['resources'] })} />
        </Tabs.Panel>
      </Tabs>
    </Stack>
  )
}
