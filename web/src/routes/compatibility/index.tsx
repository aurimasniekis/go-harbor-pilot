import { createFileRoute } from '@tanstack/react-router'
import { BreakersPanel } from '@/features/compat/Breakers'
import { DegradationsPanel } from '@/features/compat/Degradations'

export const Route = createFileRoute('/compatibility/')({
  component: Breakers,
})

function Breakers() {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-4)' }}>
      <DegradationsPanel />
      <BreakersPanel />
    </div>
  )
}
