import { Component, type ErrorInfo, type ReactNode } from 'react'
import { AlertOctagon, Lock } from 'lucide-react'
import { Link } from '@tanstack/react-router'
import { ApiError } from '@/lib/api/client'
import { Button, EmptyState, Panel, Skeleton } from './ui'

export function QueryError({ error, retry }: { error: unknown; retry?: () => void }) {
  if (error instanceof ApiError && error.status === 401) {
    return (
      <Panel>
        <EmptyState icon={Lock} title="Sign in required" action={<Link to="/login">Sign in</Link>}>
          This view requires authentication.
        </EmptyState>
      </Panel>
    )
  }
  const msg = error instanceof Error ? error.message : 'Unexpected error'
  const rid = error instanceof ApiError ? error.requestId : undefined
  return (
    <Panel>
      <EmptyState icon={AlertOctagon} title="Could not load data" action={retry && <Button onClick={retry}>Retry</Button>}>
        {msg}
        {rid ? ` (request ${rid})` : ''}
      </EmptyState>
    </Panel>
  )
}

export function TableSkeleton({ rows = 6 }: { rows?: number }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14, padding: 20 }} aria-busy="true" aria-label="Loading">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} style={{ display: 'flex', gap: 16 }}>
          <Skeleton width="18%" height={16} />
          <Skeleton width="12%" height={16} />
          <Skeleton width="20%" height={16} />
          <Skeleton width="20%" height={16} />
          <Skeleton width="15%" height={16} />
        </div>
      ))}
    </div>
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
        <Panel>
          <EmptyState
            icon={AlertOctagon}
            title="Something went wrong rendering this view"
            action={<Button onClick={() => this.setState({ error: null })}>Try again</Button>}
          >
            {this.state.error.message}
          </EmptyState>
        </Panel>
      )
    }
    return this.props.children
  }
}
