import type { AccountRes, ProviderRes } from '@/lib/api/types'

/**
 * One display rule for accounts across routing, privacy and captured
 * content: the account's display name, with the provider's name after it.
 * Falls back to the short name, then to the id.
 */
export function accountLabel(acc: AccountRes | undefined, providers: Record<string, ProviderRes | undefined> = {}, fallback = ''): string {
  if (!acc) return fallback
  const name = acc.spec.display_name || acc.name
  const prov = providers[acc.spec.provider_id]
  const provName = prov ? prov.spec.display_name || prov.name : ''
  return provName ? `${name} (${provName})` : name
}

/** Fallback steps in plain words. */
export const stepWords: Record<string, string> = {
  first: 'First choice',
  primary: 'First choice',
  retry: 'Retry the same account',
  alternate_account: 'Another account, same model',
  alternate_model: 'Another model in the group',
  alternate_provider: 'Another provider',
  wait: 'Wait for capacity',
}
