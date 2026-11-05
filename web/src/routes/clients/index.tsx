import { useState } from 'react'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Button, Group, Stack, Text } from '@mantine/core'
import { BookOpen, Laptop, Plus } from 'lucide-react'
import { Badge } from '@/components/ui'
import { QueryError, TableSkeleton } from '@/components/states'
import { ResourceBadges } from '@/components/ResourceBadges'
import { AddClientDialog } from '@/features/config/BindingForm'
import { groupCost, useClientStats } from '@/features/clients/clients'
import { resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { BindingRes, PlanRes } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { type Column, DataTable, Empty, PageHeader } from '@/ui/kit'
import { harnessName, money, short } from '@/ui/fmt'

export const Route = createFileRoute('/clients/')({
  component: Clients,
})

function Clients() {
  const navigate = useNavigate()
  const list = useQuery(resourceListQuery<BindingRes>('client-bindings'))
  const plans = byId(useQuery(resourceListQuery<PlanRes>('routing-plans')).data?.items)
  const { byClient } = useClientStats()
  const canWrite = useCan('config.write')
  const [creating, setCreating] = useState(false)
  const items = list.data?.items ?? []
  const active = items.filter((b) => (byClient[b.id]?.requests ?? 0) > 0).length
  const off = items.filter((b) => !b.spec.enabled).length

  const cols: Column<BindingRes>[] = [
    {
      id: 'client',
      header: 'Client',
      mobile: 'title',
      cell: (b) => (
        <Stack gap={0} miw={0}>
          <Group gap={6} wrap="nowrap" miw={0}>
            <Link to="/clients/$bindingId" params={{ bindingId: b.id }} onClick={(e) => e.stopPropagation()}>
              <Text size="sm" fw={500} truncate component="span">
                {b.spec.display_name || b.name}
              </Text>
            </Link>
            <ResourceBadges r={b} />
          </Group>
          <Text size="xs" c="dimmed" truncate>
            {harnessName[b.spec.expected_harness] ?? 'Any tool'}
            {b.spec.display_name ? ` · ${b.name}` : ''}
          </Text>
        </Stack>
      ),
    },
    { id: 'plan', header: 'Routing plan', mobile: 'meta', cell: (b) => plans[b.spec.plan_id]?.spec.display_name || plans[b.spec.plan_id]?.name || '—' },
    {
      id: 'requests',
      header: 'Requests 24h',
      align: 'right',
      mobile: 'aside',
      cell: (b) => <span className="num">{short(byClient[b.id]?.requests ?? 0)}</span>,
    },
    {
      id: 'errors',
      header: 'Failed 24h',
      align: 'right',
      mobile: 'meta',
      cell: (b) => {
        const e = (byClient[b.id]?.errors ?? 0) + (byClient[b.id]?.blocked ?? 0)
        return e ? <Text size="sm" c="var(--error-text)" className="num" span>{e} failed</Text> : <Text size="sm" c="dimmed" span>—</Text>
      },
    },
    {
      id: 'cost',
      header: 'Cost 24h',
      align: 'right',
      mobile: 'meta',
      cell: (b) => {
        const c = groupCost(byClient[b.id])
        return c ? <span className="num">{money(c.amount, c.currency)}</span> : <Text size="sm" c="dimmed" span>—</Text>
      },
    },
    { id: 'status', header: '', mobile: 'meta', width: 60, cell: (b) => (b.spec.enabled ? null : <Badge>Off</Badge>) },
  ]

  return (
    <Stack gap="lg">
      <PageHeader
        title="Clients"
        info="Tools and apps that send requests through Harbor Pilot. Each client has a routing plan, limits and its own access tokens."
        actions={
          <>
            <Button component={Link} to="/clients/setup" variant="default" leftSection={<BookOpen size={14} />}>
              Set up a tool
            </Button>
            {canWrite && (
              <Button leftSection={<Plus size={14} />} onClick={() => setCreating(true)}>
                Add client
              </Button>
            )}
          </>
        }
      />
      {list.data && items.length > 0 && (
        <Text size="sm" c="dimmed">
          {items.length} {items.length === 1 ? 'client' : 'clients'} · {active} active in the last 24 hours{off ? ` · ${off} off` : ''}
        </Text>
      )}
      {list.isError ? (
        <QueryError error={list.error} retry={() => void list.refetch()} />
      ) : !list.data ? (
        <TableSkeleton />
      ) : (
        <DataTable
          label="Client list"
          rows={items}
          columns={cols}
          rowKey={(b) => b.id}
          onRow={(b) => void navigate({ to: '/clients/$bindingId', params: { bindingId: b.id } })}
          empty={
            <Empty icon={<Laptop size={22} />} title="No clients yet" action={canWrite && <Button onClick={() => setCreating(true)}>Add client</Button>}>
              A client is a tool or app that sends requests. Add one, then set the tool up with its access token.
            </Empty>
          }
        />
      )}
      {creating && (
        <AddClientDialog
          onClose={() => setCreating(false)}
          onCreated={(b) => {
            setCreating(false)
            void navigate({ to: '/clients/$bindingId', params: { bindingId: b.id }, search: { token: true } })
          }}
        />
      )}
    </Stack>
  )
}
