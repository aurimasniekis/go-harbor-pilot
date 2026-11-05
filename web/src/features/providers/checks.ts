import { queryOptions, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiGet, apiSend } from '@/lib/api/client'
import type { components } from '@/lib/api/schema.gen'

export type AccountCheck = components['schemas']['AccountCheck']

export const accountChecksQuery = queryOptions({
  queryKey: ['resources', 'account-checks'],
  queryFn: ({ signal }) => apiGet<{ items: AccountCheck[] }>('/account-checks', undefined, signal),
})

/** Runs "Test connection" for an account and refreshes the stored results. */
export function useTestAccount() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => apiSend<AccountCheck>('POST', `/provider-accounts/${encodeURIComponent(id)}/test`),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['resources', 'account-checks'] }),
  })
}

export type AccountState = 'off' | 'no_credential' | 'rejected' | 'unreachable' | 'limit' | 'failing' | 'working' | 'checked' | 'not_tested' | 'bridge_unverified'

/** One honest status for an account: green only for observed success. */
export function accountState(o: {
  enabled: boolean
  hasCredential: boolean
  needsCredential: boolean
  check?: { status: string }
  limitReached: boolean
  requests: number
  failed: number
}): AccountState {
  if (!o.enabled) return 'off'
  if (o.needsCredential && !o.hasCredential) return 'no_credential'
  if (o.check?.status === 'rejected') return 'rejected'
  if (o.check?.status === 'unreachable') return 'unreachable'
  if (o.limitReached) return 'limit'
  if (o.requests > 0) return o.failed * 2 > o.requests ? 'failing' : 'working'
  if (o.check?.status === 'ok') return 'checked'
  if (o.check?.status === 'not_verified') return 'bridge_unverified'
  return 'not_tested'
}

export const accountStateView: Record<AccountState, { dot: string; label: string }> = {
  off: { dot: 'stale', label: 'Off' },
  no_credential: { dot: 'error', label: 'No credential' },
  rejected: { dot: 'error', label: 'Credential rejected' },
  unreachable: { dot: 'error', label: 'Unreachable' },
  limit: { dot: 'warning', label: 'Limit reached' },
  failing: { dot: 'error', label: 'Mostly failing' },
  working: { dot: 'ok', label: 'Working' },
  checked: { dot: 'ok', label: 'Connection OK' },
  bridge_unverified: { dot: 'idle', label: 'Not used yet' },
  not_tested: { dot: 'idle', label: 'Not tested' },
}
