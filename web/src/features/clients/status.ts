import type { Credential } from '@/lib/api/types'

export type ClientStatus = 'off' | 'not_set_up' | 'failing' | 'working' | 'idle' | 'unknown'

/** Whether a client is usable and in use, from its tokens and last day of traffic. */
export function clientStatus(b: { spec: { enabled: boolean } }, activeTokens: number | undefined, requests: number, failed: number): ClientStatus {
  if (!b.spec.enabled) return 'off'
  if (activeTokens === undefined) return 'unknown'
  if (activeTokens === 0) return 'not_set_up'
  if (requests > 0 && failed * 2 > requests) return 'failing'
  return requests > 0 ? 'working' : 'idle'
}

/** Active tokens first (newest first), revoked ones last. */
export function sortTokens(items: Credential[]): Credential[] {
  return [...items].sort((a, b) => Number(!!a.revoked_at) - Number(!!b.revoked_at) || b.created_at.localeCompare(a.created_at))
}
