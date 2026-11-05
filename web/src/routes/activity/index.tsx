import { createFileRoute } from '@tanstack/react-router'
import { Stack } from '@mantine/core'
import { ActivityStats, RequestsPage, requestSearch, StatsCaption } from '@/features/activity/RequestsPage'

export const Route = createFileRoute('/activity/')({
  validateSearch: (s) => requestSearch.parse(s),
  component: () => (
    <Stack gap="lg">
      <Stack gap={6}>
        <StatsCaption />
        <ActivityStats />
      </Stack>
      <RequestsPage />
    </Stack>
  ),
})
