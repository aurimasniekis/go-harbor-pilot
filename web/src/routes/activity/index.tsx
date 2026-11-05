import { createFileRoute } from '@tanstack/react-router'
import { Stack } from '@mantine/core'
import { ActivityStats, RequestsPage, requestSearch } from '@/features/activity/RequestsPage'

export const Route = createFileRoute('/activity/')({
  validateSearch: (s) => requestSearch.parse(s),
  component: () => (
    <Stack gap="lg">
      <ActivityStats />
      <RequestsPage />
    </Stack>
  ),
})
