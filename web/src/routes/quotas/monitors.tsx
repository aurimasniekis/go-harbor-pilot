import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { z } from 'zod'
import { Stack, Text } from '@mantine/core'
import { Badge, TierBadge } from '@/components/ui'
import { ResourceList } from '@/features/config/ResourceList'
import { TrackingLine, trackingOf } from '@/features/limits/LimitsPage'
import { MonitorDialog } from '@/features/quota/MonitorDialog'
import { LoginCallback, LoginPanel } from '@/features/quota/LoginPanel'
import { quotaAdaptersQuery, quotaBoardQuery, resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { AccountRes, QuotaMonitorRes, Tier } from '@/lib/api/types'
import q from '@/features/quota/quota.module.css'

const search = z.object({
  login: z.string().optional().catch(undefined),
  state: z.string().optional().catch(undefined),
  code: z.string().optional().catch(undefined),
})

export const Route = createFileRoute('/quotas/monitors')({
  validateSearch: (s) => search.parse(s),
  component: Monitors,
})

function Monitors() {
  const { login, state, code } = Route.useSearch()
  const navigate = useNavigate()
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const adapters = useQuery(quotaAdaptersQuery)
  const board = useQuery(quotaBoardQuery)
  const acc = byId(accounts.data?.items)
  const ad = byId(adapters.data?.items)
  const health = Object.fromEntries((board.data?.monitors ?? []).map((m) => [m.id, m]))
  return (
    <div className={q.stack}>
      {login && state && code && (
        <LoginCallback monitorID={login} state={state} code={code} onDone={() => void navigate({ to: '/quotas/monitors', search: {} })} />
      )}
      <ResourceList<QuotaMonitorRes>
        plural="quota-monitors"
        title="Trackers"
        subtitle="Each tracker reads one provider's quota or usage API with its own write-only credential. Link it to an account so routing avoids the account when a limit is reached."
        noun="quota tracker"
        empty="Track a provider's quota windows, balance or budget. Link the tracker to a provider account so routing avoids it when a limit is reached."
        nameCell={(r) => (
          <Stack gap={0} miw={0}>
            <Text size="sm" fw={500} truncate>
              {r.name}
            </Text>
            <Text size="xs" c="dimmed" truncate>
              {ad[r.spec.adapter]?.display_name ?? r.spec.adapter}
            </Text>
          </Stack>
        )}
        columns={[
          {
            header: 'Routes for',
            mobile: 'meta',
            cell: (r) => (r.spec.account_id ? <span>{acc[r.spec.account_id]?.name ?? r.spec.account_id}</span> : <span className="muted">Not linked</span>),
          },
          {
            header: 'Checks',
            mobile: 'meta',
            cell: (r) => (r.spec.poll_interval_secs ? `Every ${Math.round(r.spec.poll_interval_secs / 60)} min` : <span className="muted">Provider default</span>),
          },
          {
            header: 'Status',
            mobile: 'line',
            cell: (r) => (
              <span className={q.badges}>
                {!r.spec.enabled ? (
                  <Badge>Off</Badge>
                ) : health[r.id] ? (
                  <TrackingLine t={trackingOf([health[r.id]!])} />
                ) : (
                  <Text size="xs" c="dimmed">
                    Not checked yet
                  </Text>
                )}
                {ad[r.spec.adapter] && ad[r.spec.adapter]!.tier !== 'official' && <TierBadge tier={ad[r.spec.adapter]!.tier as Tier} />}
              </span>
            ),
          },
        ]}
        dialog={(r, close) => <MonitorDialog monitor={r} onClose={close} />}
      />
      <LoginPanel />
    </div>
  )
}
