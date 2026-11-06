import { z } from 'zod'

/**
 * Trusted references (D-041): `key:value` pairs a client or a run token
 * carries, such as `spaceman.run:run_42`. Usage and activity can be filtered
 * by up to four of them and usage can be grouped by one key.
 */

/** Same rule as the server's reference keys. */
export const REF_KEY = /^[a-z0-9][a-z0-9._-]{0,63}$/

/** How many references one filter may combine. */
export const MAX_REFS = 4

/** Longest reference value, in bytes. */
const MAX_VALUE_BYTES = 256

export interface Ref {
  key: string
  value: string
}

/** Splits `key:value` at the first colon; null when it is not a valid reference. */
export function parseRef(raw: string): Ref | null {
  const i = raw.indexOf(':')
  if (i <= 0) return null
  const key = raw.slice(0, i)
  const value = raw.slice(i + 1)
  if (!REF_KEY.test(key) || !validValue(value)) return null
  return { key, value }
}

export function formatRef(r: Ref): string {
  return `${r.key}:${r.value}`
}

function validValue(v: string): boolean {
  return v.length > 0 && new TextEncoder().encode(v).length <= MAX_VALUE_BYTES
}

/** Why a key and value can't be used as a filter, or null when they can. */
export function refProblem(key: string, value: string, existing: readonly string[] = []): string | null {
  if (!REF_KEY.test(key)) return 'Use a lowercase key such as spaceman.run'
  if (!validValue(value.trim())) return 'Enter a value (up to 256 bytes)'
  const next = formatRef({ key, value: value.trim() })
  if (existing.includes(next)) return 'Already filtered by this reference'
  if (existing.filter((r) => !r.startsWith(`${key}:`)).length >= MAX_REFS) return `Up to ${MAX_REFS} references at once`
  return null
}

/**
 * Adds a reference to a filter list. A reference with the same key replaces
 * the old one: two values of one key would match nothing.
 */
export function withRef(list: readonly string[] | undefined, r: Ref): string[] {
  const rest = (list ?? []).filter((x) => !x.startsWith(`${r.key}:`))
  return [...rest, formatRef(r)].slice(-MAX_REFS)
}

/** Plain names for well-known keys; others read as their last segment. */
const names: Record<string, string> = {
  session: 'Session',
  project_id: 'Project',
}

/** "spaceman.run" → "Run", "git.repo" → "Repo", "ci.provider" → "Provider". */
export function refKeyLabel(key: string): string {
  if (names[key]) return names[key]!
  const last = key.split('.').pop() || key
  const t = last.replaceAll(/[_-]+/g, ' ').trim()
  return t ? t.charAt(0).toUpperCase() + t.slice(1) : key
}

/** "Run: run_42". */
export function refLabel(raw: string): string {
  const r = parseRef(raw)
  return r ? `${refKeyLabel(r.key)}: ${r.value}` : raw
}

/**
 * Keys offered as suggestions: the run, task and space keys an orchestrator
 * such as SpaceMan binds to run tokens, then the keys set on clients.
 */
export const commonRefKeys = ['spaceman.run', 'spaceman.task', 'spaceman.space', 'session'] as const

/** Suggested keys: the common ones first, then other keys seen on clients. */
export function refKeyOptions(clientRefs: (Record<string, string> | undefined)[]): { value: string; label: string }[] {
  const keys: string[] = [...commonRefKeys]
  for (const refs of clientRefs) for (const k of Object.keys(refs ?? {})) if (!keys.includes(k)) keys.push(k)
  return keys.map((k) => ({ value: k, label: `${refKeyLabel(k)} (${k})` }))
}

/**
 * URL search value: a list of `key:value` strings. A single plain value
 * (`?ref=spaceman.run:run_42`, as written by hand) is accepted too; invalid
 * entries are dropped.
 */
export const refsSearch = z
  .preprocess((v) => (typeof v === 'string' ? [v] : v), z.array(z.string()))
  .transform((list) => dedupeByKey(list.filter((raw) => parseRef(raw) !== null)))
  .optional()
  .catch(undefined)

/** One value per key (the last one wins), at most MAX_REFS; undefined when empty. */
function dedupeByKey(list: string[]): string[] | undefined {
  const byKey = new Map<string, string>()
  for (const raw of list) byKey.set(raw.slice(0, raw.indexOf(':')), raw)
  const out = [...byKey.values()].slice(0, MAX_REFS)
  return out.length ? out : undefined
}
