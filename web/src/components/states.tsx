import { Component, type ErrorInfo, type ReactNode } from 'react'
import { Stack, Text } from '@mantine/core'
import { AlertOctagon, Lock, SearchX } from 'lucide-react'
import { Link } from '@tanstack/react-router'
import { ApiError } from '@/lib/api/client'
import { summarizeError } from '@/lib/api/errors'
import { Button, EmptyState, Skeleton } from './ui'

export function QueryError({ error, retry, back }: {
  error: unknown
  retry?: () => void
  /** Where to go when the object no longer exists (404). */
  back?: { label: string; to: string }
}) {
  if (error instanceof ApiError && error.status === 401) {
    return (
      <EmptyState icon={Lock} title="Sign in required" action={<Link to="/login">Sign in</Link>}>
        This view requires authentication.
      </EmptyState>
    )
  }
  if (error instanceof ApiError && error.status === 404) {
    return (
      <EmptyState icon={SearchX} title="Not found" action={back && <Link to={back.to}>{back.label}</Link>}>
        It may have been deleted or renamed.
      </EmptyState>
    )
  }
  const s = summarizeError(error)
  return (
    <EmptyState icon={AlertOctagon} title="Could not load this" action={retry && <Button onClick={retry}>Try again</Button>}>
      {s.title}
      {s.requestId && (
        <Text size="xs" c="dimmed" mt={4}>
          Reference {s.requestId}
        </Text>
      )}
    </EmptyState>
  )
}

export function TableSkeleton({ rows = 6 }: { rows?: number }) {
  return (
    <Stack gap="sm" py="sm" aria-busy="true" aria-label="Loading">
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} height={18} />
      ))}
    </Stack>
  )
}

export class ErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  override state = { error: null as Error | null }

  static getDerivedStateFromError(error: Error) {
    return { error }
  }

  override componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('UI error', error, info.componentStack)
  }

  override render() {
    if (this.state.error) {
      return (
        <EmptyState
          icon={AlertOctagon}
          title="Something went wrong rendering this view"
          action={<Button onClick={() => this.setState({ error: null })}>Try again</Button>}
        >
          {this.state.error.message}
        </EmptyState>
      )
    }
    return this.props.children
  }
}
