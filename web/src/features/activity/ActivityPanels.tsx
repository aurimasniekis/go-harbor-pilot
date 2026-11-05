import { Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Group, Stack, Text } from '@mantine/core'
import { Skeleton } from '@/components/ui'
import { QueryError } from '@/components/states'
import { requestsQuery } from '@/lib/api/queries'
import { fmtDateTime, fmtRelative } from '@/lib/format'
import { Ms, StatusBadge } from './bits'
import s from './activity.module.css'

/** Most recent requests of one client: one line each. */
export function RecentRequestsPanel({ bindingId }: { bindingId: string }) {
  const q = useQuery(requestsQuery({ binding_id: bindingId, limit: 8 }))
  const items = q.data?.items ?? []
  return (
    <Stack gap="sm">
      <Group justify="space-between" gap="xs">
        <Text size="sm" fw={600}>
          Latest requests
        </Text>
        <Link to="/activity" search={{ binding_id: bindingId }}>
          See all in Requests
        </Link>
      </Group>
      {q.isError ? (
        <QueryError error={q.error} retry={() => void q.refetch()} />
      ) : !q.data ? (
        <Skeleton height={80} />
      ) : items.length === 0 ? (
        <Text size="sm" c="dimmed">
          No requests yet. Set up a tool with an access token to see them here.
        </Text>
      ) : (
        <ul className={s.recent} aria-label="Latest requests">
          {items.map((r) => (
            <li key={r.id}>
              <Link to="/activity/$requestId" params={{ requestId: r.id }} className={s.recentRow}>
                <span className={s.recentModel}>
                  {r.requested_model || 'unknown model'}
                  {r.final_model && r.final_model !== r.requested_model && <span className="muted"> → {r.final_model}</span>}
                </span>
                <span className={s.recentMeta}>
                  {r.status !== 'ok' && <StatusBadge status={r.status} />}
                  <Ms v={r.duration_ms} />
                  <span title={fmtDateTime(r.created_at, { seconds: true })}>{fmtRelative(r.created_at)}</span>
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </Stack>
  )
}
