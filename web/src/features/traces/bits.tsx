import { Link } from '@tanstack/react-router'
import { Lock } from 'lucide-react'
import { Badge, EmptyState } from '@/components/ui'
import type { TraceMode } from '@/lib/api/types'
import { traceModeLabel } from '@/features/privacy/privacy'

/** The same words as the capture rule's "What to keep". */
export function TraceModeBadge({ mode }: { mode: TraceMode | string }) {
  return <Badge tone={mode === 'unredacted' ? 'error' : mode === 'masked_full' ? 'info' : 'neutral'}>{traceModeLabel(mode)}</Badge>
}

/** Explains the explicit grants trace content needs. */
export function GrantNeeded({ perm, what }: { perm: string; what: string }) {
  return (
    <EmptyState icon={Lock} title={`${what} needs an explicit grant`}>
      Captured content is never implied by a role. An administrator can grant <span className="mono">{perm}</span> to you under <GrantLink />; every
      access is audited.
    </EmptyState>
  )
}

/** Where an administrator grants content access. */
export function GrantLink() {
  return (
    <Link to="/settings/users" style={{ textDecoration: 'underline' }}>
      Settings → People
    </Link>
  )
}

/** 1640 → "1.6 KB"; one formatter for every capture size. */
export function fmtSize(b: number): string {
  if (b < 1024) return `${b} B`
  if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} KB`
  return `${(b / 1024 / 1024).toFixed(1)} MB`
}
