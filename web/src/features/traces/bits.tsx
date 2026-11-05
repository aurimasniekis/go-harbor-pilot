import { Link } from '@tanstack/react-router'
import { Lock } from 'lucide-react'
import { Badge, EmptyState } from '@/components/ui'
import type { TraceMode } from '@/lib/api/types'

export function TraceModeBadge({ mode }: { mode: TraceMode | string }) {
  if (mode === 'unredacted') return <Badge tone="error">unredacted</Badge>
  if (mode === 'masked_full') return <Badge tone="info">masked full</Badge>
  return <Badge>masked diagnostic</Badge>
}

/** Explains the explicit grants trace content needs. */
export function GrantNeeded({ perm, what }: { perm: string; what: string }) {
  return (
    <EmptyState icon={Lock} title={`${what} needs an explicit grant`}>
      Trace content is never implied by a role. An administrator can grant <span className="mono">{perm}</span> to your user under{' '}
      <Link to="/settings/users" style={{ textDecoration: 'underline' }}>Settings → Users</Link>; every content access is audited.
    </EmptyState>
  )
}
