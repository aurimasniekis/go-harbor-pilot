import { createFileRoute } from '@tanstack/react-router'
import { RequestPage } from '@/features/activity/RequestDetail'

export const Route = createFileRoute('/activity/$requestId')({
  component: function RequestRoute() {
    const { requestId } = Route.useParams()
    return <RequestPage id={requestId} />
  },
})
