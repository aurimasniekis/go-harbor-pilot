import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Group, Text } from '@mantine/core'
import { Badge } from '@/components/ui'
import { NameCell, ResourceList } from '@/features/config/ResourceList'
import { PlanDialog } from '@/features/config/PlanDialog'
import { effectiveQuery, planVersionsQuery, resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { AccountRes, AliasRes, PlanRes, PoolRes, ProviderRes } from '@/lib/api/types'
import { planLanes } from '@/features/routing/graph'
import { planSummary } from '@/features/routing/flow'

export const Route = createFileRoute('/routing/plans/')({
  component: Plans,
})

function useMaps() {
  const aliases = useQuery(resourceListQuery<AliasRes>('model-aliases'))
  const pools = useQuery(resourceListQuery<PoolRes>('pools'))
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const providers = useQuery(resourceListQuery<ProviderRes>('providers'))
  const ready = !!(aliases.data && pools.data && accounts.data && providers.data)
  return {
    ready,
    maps: { aliases: byId(aliases.data?.items), pools: byId(pools.data?.items), accounts: byId(accounts.data?.items), providers: byId(providers.data?.items) },
  }
}

/** One plain sentence describing a plan's live version. */
function Summary({ planId, maps }: { planId: string; maps: ReturnType<typeof useMaps> }) {
  const v = useQuery(planVersionsQuery(planId))
  const live = v.data?.items.find((x) => x.status === 'published')
  if (!v.data || !maps.ready) return <Text size="sm" c="dimmed">…</Text>
  return (
    <Text size="sm" c={live ? undefined : 'dimmed'} lineClamp={2}>
      {planSummary(live?.document, live ? planLanes(live.document, maps.maps) : [])}
    </Text>
  )
}

function Plans() {
  const navigate = useNavigate()
  const eff = useQuery(effectiveQuery)
  const plans = byId(eff.data?.plans)
  const maps = useMaps()
  return (
    <ResourceList<PlanRes>
      plural="routing-plans"
      title="Routing plans"
      subtitle="A plan decides which account and model serve each request. Changes are drafted, then published; drafts never affect traffic."
      noun="plan"
      empty="Create a plan, then draft and publish its first version."
      nameCell={(r) => <NameCell name={r.name} display={r.spec.display_name} />}
      onRow={(r) => void navigate({ to: '/routing/plans/$planId', params: { planId: r.id } })}
      columns={[
        { header: 'What it does', mobile: 'line', cell: (r) => <Summary planId={r.id} maps={maps} /> },
        {
          header: 'Version',
          mobile: 'meta',
          cell: (r) => {
            const p = plans[r.id]
            return (
              <Group gap={4} wrap="nowrap">
                {p?.published_version ? <Badge tone="success">Live v{p.published_version}</Badge> : <Badge tone="warning">Not live</Badge>}
              </Group>
            )
          },
        },
        { header: 'Used by', mobile: 'meta', cell: (r) => `${plans[r.id]?.bindings ?? 0} ${(plans[r.id]?.bindings ?? 0) === 1 ? 'client' : 'clients'}` },
      ]}
      dialog={(r, close) => (
        <PlanDialog
          plan={r}
          onClose={close}
          onCreated={(created) => {
            close()
            void navigate({ to: '/routing/plans/$planId', params: { planId: created.id } })
          }}
        />
      )}
    />
  )
}
