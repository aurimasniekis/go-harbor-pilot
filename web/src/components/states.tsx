import { Component, type ErrorInfo, type ReactNode } from 'react'
import { Stack } from '@mantine/core'
import { AlertOctagon, Lock } from 'lucide-react'
import { Link } from '@tanstack/react-router'
import { ApiError } from '@/lib/api/client'
import { Button, EmptyState, Skeleton } from './ui'

export function QueryError({ error, retry }: { error: unknown; retry?: () => void }) {
  if (error instanceof ApiError && error.status === 401) {
    return (
      <EmptyState icon={Lock} title="Sign in required" action={<Link to="/login">Sign in</Link>}>
        This view requires authentication.
      </EmptyState>
    )
  }
  const msg = error instanceof Error ? error.message : 'Unexpected error'
  const rid = error instanceof ApiError ? error.requestId : undefined
  return (
    <EmptyState icon={AlertOctagon} title="Could not load data" action={retry && <Button onClick={retry}>Retry</Button>}>
      {msg}
      {rid ? ` (request ${rid})` : ''}
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
