import type { AccountRes, ProviderRes } from '@/lib/api/types'
import { providerTypes } from '@/features/config/constants'

/** How an account is paid for; one wording in lists, drawers and dialogs. */
export const billingLabels: Record<string, { label: string; hint: string }> = {
  metered_api: { label: 'Pay per use', hint: 'An API key billed for each request' },
  subscription: { label: 'Subscription', hint: 'A flat-fee plan, such as Claude Max or ChatGPT Pro' },
  free: { label: 'Free', hint: 'No charge' },
  local: { label: 'Local model', hint: 'Runs on your own hardware' },
}

export function billingLabel(v: string): string {
  return billingLabels[v]?.label ?? v
}

export function isBridge(type: string | undefined): boolean {
  return type === 'claude_code' || type === 'codex'
}

export function providerLabel(p: ProviderRes | undefined): string {
  return p ? p.spec.display_name || p.name : 'Unknown provider'
}

export function typeLabel(type: string): string {
  return providerTypes.find((t) => t.value === type)?.label ?? type
}

export function accountLabel(a: AccountRes): string {
  return a.spec.display_name || a.name
}

/** Where the account's credential comes from, naming the variable or file (not secret). */
export function credentialSource(a: AccountRes): string {
  const ref = a.spec.credential_ref
  if (ref?.startsWith('env:')) return `Environment variable ${ref.slice(4)}`
  if (ref?.startsWith('file:')) return `File ${ref.slice(5)}`
  return a.secrets?.credential?.set ? 'Saved in Harbor Pilot (encrypted)' : 'None'
}

export function hasCredential(a: AccountRes): boolean {
  return !!a.spec.credential_ref || !!a.secrets?.credential?.set
}

/** A free name: base, base-2, base-3 … not in taken. */
export function freeName(base: string, taken: Set<string>): string {
  if (!taken.has(base)) return base
  for (let i = 2; ; i++) if (!taken.has(`${base}-${i}`)) return `${base}-${i}`
}
