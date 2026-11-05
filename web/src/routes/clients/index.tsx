import { useState } from 'react'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { useQueries, useQuery } from '@tanstack/react-query'
import { Button, Group, Stack, Text } from '@mantine/core'
import { BookOpen, Laptop, Plus } from 'lucide-react'
import { QueryError, TableSkeleton } from '@/components/states'
import { ResourceBadges } from '@/components/ResourceBadges'
import { AddClientDialog } from '@/features/config/BindingForm'
import { groupCost, useClientStats } from '@/features/clients/clients'
import { type ClientStatus, clientStatus } from '@/features/clients/status'
import { toolLabel } from '@/features/harness/tools'
import { credentialsQuery, resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { BindingRes, PlanRes } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { type Column, DataTable, Empty, PageHeader, StatusDot } from '@/ui/kit'
import { money, short } from '@/ui/fmt'

export const Route = createFileRoute('/clients/')({
  component: Clients,
})

const statusView: Record<ClientStatus, { dot: string; label: string }> = {
  off: { dot: 'stale', label: 'Off' },
  not_set_up: { dot: 'warning', label: 'Not set up' },
  failing: { dot: 'error', label: 'Mostly failing' },
  working: { dot: 'ok', label: 'Working' },
  idle: { dot: 'idle', label: 'No requests today' },
  unknown: { dot: 'idle', label: '…' },
}

function Clients() {
  const navigate = useNavigate()
  const list = useQuery(resourceListQuery<BindingRes>('client-bindings'))
  const plans = byId(useQuery(resourceListQuery<PlanRes>('routing-plans')).data?.items)
  const { byClient } = useClientStats()
  const canWrite = useCan('config.write')
  const canTokens = useCan('config.read')
  const [creating, setCreating] = useState(false)
  const [dialogKey, setDialogKey] = useState(0)
  const items = list.data?.items ?? []
  const tokens = useQueries({ queries: items.map((b) => ({ ...credentialsQuery(b.id), enabled: canTokens })) })
  const activeTokens = (i: number) => {
    const d = tokens[i]?.data
    return d ? d.items.filter((c) => !c.revoked_at).length : undefined
  }
  const index = Object.fromEntries(items.map((b, i) => [b.id, i]))
  const status = (b: BindingRes) => {
    const st = byClient[b.id]
    return clientStatus(b, activeTokens(index[b.id] ?? -1), st?.requests ?? 0, (st?.errors ?? 0) + (st?.blocked ?? 0))
  }
  const active = items.filter((b) => (byClient[b.id]?.requests ?? 0) > 0).length
  const notSetUp = items.filter((b) => status(b) === 'not_set_up').length

  const cols: Column<BindingRes>[] = [
    {
      id: 'client',
      header: 'Client',
      mobile: 'title',
      width: '34%',
      cell: (b) => {
        const plan = plans[b.spec.plan_id]
        return (
          <Stack gap={0} miw={0}>
            <Group gap={6} wrap="nowrap" miw={0}>
              <Link to="/clients/$bindingId" params={{ bindingId: b.id }}>
                <Text size="sm" fw={500} truncate component="span">
                  {b.spec.display_name || b.name}
                </Text>
              </Link>
              <ResourceBadges r={b} />
            </Group>
            <Text size="xs" c="dimmed" truncate>
              {toolLabel(b.spec.expected_harness)}
              {plan ? ` · ${plan.spec.display_name || plan.name}` : ''}
            </Text>
          </Stack>
        )
      },
    },
    {
      id: 'status',
      header: 'Status',
      mobile: 'aside',
      width: '20%',
      cell: (b) => {
        const v = statusView[status(b)]
        return <StatusDot status={v.dot} label={v.label} />
      },
    },
    {
      id: 'requests',
      header: 'Requests 24h',
      align: 'right',
      mobile: 'meta',
      mobileLabel: 'Requests',
      cell: (b) => <span className="num">{short(byClient[b.id]?.requests ?? 0)}</span>,
    },
    {
      id: 'errors',
      header: 'Failed 24h',
      align: 'right',
      mobile: 'meta',
      mobileLabel: 'Failed',
      cell: (b) => {
        const e = (byClient[b.id]?.errors ?? 0) + (byClient[b.id]?.blocked ?? 0)
        return e ? <Text size="sm" c="var(--error-text)" className="num" span>{e}</Text> : <Text size="sm" c="dimmed" className="num" span>0</Text>
      },
    },
    {
      id: 'cost',
      header: 'Cost 24h',
      align: 'right',
      mobile: 'meta',
      mobileLabel: 'Cost',
      cell: (b) => {
        const c = groupCost(byClient[b.id])
        return c ? <span className="num">{money(c.amount, c.currency)}</span> : <Text size="sm" c="dimmed" span>—</Text>
      },
    },
  ]

  const openDialog = () => {
    setDialogKey((k) => k + 1)
    setCreating(true)
  }
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
              <Button leftSection={<Plus size={14} />} onClick={openDialog}>
                Add client
              </Button>
            )}
          </>
        }
      />
      {list.data && items.length > 0 && (
        <Text size="sm" c="dimmed">
          {items.length} {items.length === 1 ? 'client' : 'clients'} · {active} active in the last 24 hours
          {notSetUp ? ` · ${notSetUp} not set up` : ''}
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
          fixed
          rowKey={(b) => b.id}
          onRow={(b) => void navigate({ to: '/clients/$bindingId', params: { bindingId: b.id } })}
          empty={
            <Empty icon={<Laptop size={22} />} title="Connect your first tool" action={canWrite && <Button onClick={openDialog}>Add client</Button>}>
              A client is a tool or app that sends requests, such as Claude Code on a laptop. Add one and you get its access token and setup steps.
            </Empty>
          }
        />
      )}
      <AddClientDialog
        key={dialogKey}
        open={creating}
        onClose={() => setCreating(false)}
        onCreated={(b) => {
          setCreating(false)
          void navigate({ to: '/clients/$bindingId', params: { bindingId: b.id }, search: { token: true } })
        }}
      />
    </Stack>
  )
}
