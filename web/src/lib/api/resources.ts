import { useMutation, useQueryClient } from '@tanstack/react-query'
import { apiSend } from './client'
import type { ResourcePlural } from './queries'

/** Body of a create/replace request. */
export interface WriteBody<S> {
  name: string
  spec: S
  secrets?: Record<string, string>
}

/**
 * Create (no existing) or replace (existing with revision) a managed resource.
 * Replacement always sends If-Match with the revision the form was loaded from.
 */
export function useSaveResource<S, R = unknown>(plural: ResourcePlural) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ existing, body }: { existing?: { id: string; revision: number }; body: WriteBody<S> }) =>
      existing
        ? apiSend<R>('PUT', `/${plural}/${encodeURIComponent(existing.id)}`, body, { ifMatch: existing.revision })
        : apiSend<R>('POST', `/${plural}`, body),
    onSuccess: () => invalidateAfterWrite(qc, plural),
  })
}

/** Resource writes also change derived views (quota board, budgets, sign-in list). */
function invalidateAfterWrite(qc: ReturnType<typeof useQueryClient>, plural: ResourcePlural) {
  void qc.invalidateQueries({ queryKey: ['resources'] })
  if (plural === 'quota-monitors' || plural === 'provider-accounts') void qc.invalidateQueries({ queryKey: ['quota'] })
  if (plural === 'budgets') void qc.invalidateQueries({ queryKey: ['budgets'] })
}

export function useDeleteResource(plural: ResourcePlural) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, revision }: { id: string; revision: number }) =>
      apiSend<void>('DELETE', `/${plural}/${encodeURIComponent(id)}`, undefined, { ifMatch: revision }),
    onSuccess: () => invalidateAfterWrite(qc, plural),
  })
}

/** Index a list of resources by id. */
export function byId<T extends { id: string }>(items: T[] | undefined | null): Record<string, T> {
  const out: Record<string, T> = {}
  for (const it of items ?? []) out[it.id] = it
  return out
}
