import { lazy, Suspense } from 'react'
import { Link, Navigate, Outlet, createRootRouteWithContext, useRouterState } from '@tanstack/react-router'
import { type QueryClient, useQuery } from '@tanstack/react-query'
import { Button, Group } from '@mantine/core'
import { Compass, House, Search } from 'lucide-react'
import { spotlight } from '@mantine/spotlight'
import { Shell } from '@/shell/Shell'
import { ErrorBoundary, QueryError } from '@/components/states'
import { useSessionSync } from '@/lib/session'
import { metaQuery } from '@/lib/api/queries'
import { setServerTime } from '@/lib/format'
import { Empty, PageHeader } from '@/ui/kit'

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
  // Path, query and hash, so a deep link survives the sign-in detour.
  const href = useRouterState({ select: (r) => r.location.href })
  if (meta.data?.server_time) setServerTime(meta.data.server_time)

  if (PUBLIC_PATHS.includes(pathname)) {
    return <Outlet />
  }
  if (!session) return null
  if (!session.authenticated) {
    return <Navigate to="/login" search={{ next: href }} replace />
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
  notFoundComponent: NotFound,
})

/** Unknown address: a real heading, what happened, and ways back. */
function NotFound() {
  const href = useRouterState({ select: (r) => r.location.pathname })
  return (
    <>
      <PageHeader title="Page not found" />
      <Empty
        icon={<Compass size={28} aria-hidden="true" />}
        title="Nothing lives at this address"
        action={
          <Group gap="sm" mt="sm">
            <Button component={Link} to="/" leftSection={<House size={16} />}>
              Go to Home
            </Button>
            <Button variant="default" leftSection={<Search size={16} />} onClick={() => spotlight.open()}>
              Search pages
            </Button>
          </Group>
        }
      >
        <span className="mono">{href}</span> is not a Harbor Pilot page. It may have moved, or the link may be mistyped.
      </Empty>
    </>
  )
}

