import { lazy, Suspense } from 'react'
import { Navigate, Outlet, createRootRouteWithContext, useRouterState } from '@tanstack/react-router'
import { type QueryClient, useQuery } from '@tanstack/react-query'
import { Compass } from 'lucide-react'
import { Shell } from '@/shell/Shell'
import { ErrorBoundary, QueryError } from '@/components/states'
import { EmptyState, Panel } from '@/components/ui'
import { useSessionSync } from '@/lib/session'
import { metaQuery } from '@/lib/api/queries'
import { setServerTime } from '@/lib/format'

const Devtools = import.meta.env.DEV
  ? lazy(() =>
      Promise.all([import('@tanstack/react-router-devtools'), import('@tanstack/react-query-devtools')]).then(([r, q]) => ({
        default: () => (
          <>
            <r.TanStackRouterDevtools position="bottom-right" />
            <q.ReactQueryDevtools buttonPosition="bottom-left" />
          </>
        ),
      })),
    )
  : () => null

/** Routes rendered without the application shell and without a session. */
export const PUBLIC_PATHS = ['/login', '/setup', '/recover', '/accept']

function Root() {
  const session = useSessionSync()
  const meta = useQuery(metaQuery)
  const pathname = useRouterState({ select: (r) => r.location.pathname })
  if (meta.data?.server_time) setServerTime(meta.data.server_time)

  if (PUBLIC_PATHS.includes(pathname)) {
    return <Outlet />
  }
  if (!session) return null
  if (!session.authenticated) {
    return <Navigate to="/login" search={{ next: pathname }} replace />
  }
  return (
    <Shell>
      <ErrorBoundary>
        <Outlet />
      </ErrorBoundary>
      <Suspense>
        <Devtools />
      </Suspense>
    </Shell>
  )
}

export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  component: Root,
  errorComponent: ({ error, reset }) => <QueryError error={error} retry={reset} />,
  notFoundComponent: () => (
    <Panel>
      <EmptyState icon={Compass} title="Page not found">
        The page you requested does not exist.
      </EmptyState>
    </Panel>
  ),
})
