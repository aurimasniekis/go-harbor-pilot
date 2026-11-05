import { createFileRoute } from '@tanstack/react-router'
import { Sandbox } from '@/features/privacy/Sandbox'

export const Route = createFileRoute('/privacy/sandbox')({
  component: Sandbox,
})
