import { createFileRoute } from '@tanstack/react-router'
import { ChangesPanel } from '@/features/compat/Changes'

export const Route = createFileRoute('/compatibility/watch')({
  component: ChangesPanel,
})
