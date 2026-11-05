import { createFileRoute, Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Compass } from 'lucide-react'
import { EmptyState, Panel, Skeleton } from '@/components/ui'
import { QueryError } from '@/components/states'
import { WorkItemDetail } from '@/features/compat/WorkItems'
import { ApiError } from '@/lib/api/client'
import { workItemQuery } from '@/lib/api/queries'

export const Route = createFileRoute('/compatibility/work-items/$itemId')({
  component: WorkItemPage,
})

function WorkItemPage() {
  const { itemId } = Route.useParams()
  const q = useQuery({ ...workItemQuery(itemId), retry: false })
  const back = (
    <p>
      <Link to="/compatibility/work-items">← All work items</Link>
    </p>
  )
  if (q.isError) {
    return (
      <>
        {back}
        {q.error instanceof ApiError && q.error.status === 404 ? (
          <Panel>
            <EmptyState icon={Compass} title="Work item not found">This work item id is unknown.</EmptyState>
          </Panel>
        ) : (
          <QueryError error={q.error} retry={() => void q.refetch()} />
        )}
      </>
    )
  }
  if (!q.data) return <>{back}<Skeleton height={240} /></>
  return (
    <>
      {back}
      <h2 style={{ fontSize: 'var(--text-lg)', marginBottom: 'var(--space-3)' }}>{q.data.title}</h2>
      <WorkItemDetail w={q.data} />
    </>
  )
}
