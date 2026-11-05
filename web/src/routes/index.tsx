import { createFileRoute } from '@tanstack/react-router'
import { Overview } from '@/features/overview/Overview'

export const Route = createFileRoute('/')({
  component: Overview,
})
