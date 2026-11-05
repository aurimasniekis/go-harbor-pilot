import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { ProvidersPage } from '@/features/providers/ProvidersPage'

export const Route = createFileRoute('/providers/')({
  // The open account drawer is in the URL, so it survives reload and Back.
  validateSearch: z.object({ account: z.string().optional().catch(undefined) }).parse,
  component: ProvidersPage,
})
