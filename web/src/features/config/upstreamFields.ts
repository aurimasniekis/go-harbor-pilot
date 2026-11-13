import type { ModelSpec, UpstreamProviderSpec } from '@/lib/api/types'
import { reasoningForType } from './reasoning'

/** A fresh, empty pin: every field unset, so nothing is sent until filled in. */
export function emptyProviderRouting(): UpstreamProviderSpec {
  return { order: [], only: [], ignore: [], quantizations: [] }
}

/** Pretty JSON for the free-form fields; keys are sorted so the text is stable. */
export function formatRequestFields(fields: Record<string, unknown> | undefined | null): string {
  const entries = Object.entries(fields ?? {}).sort(([a], [b]) => a.localeCompare(b))
  if (entries.length === 0) return ''
  return JSON.stringify(Object.fromEntries(entries), null, 2)
}

/**
 * Parses the free-form fields. Anything that is not a JSON object counts as
 * empty, so a half-typed editor never becomes part of the saved spec.
 */
export function parseRequestFields(text: string): { value: Record<string, unknown>; error?: string } {
  if (!text.trim()) return { value: {} }
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    return { value: {}, error: 'This is not valid JSON yet.' }
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    return { value: {}, error: 'Use a JSON object, for example {"transforms": ["middle-out"]}.' }
  }
  // Which names are allowed depends on the provider type. The server keeps
  // that allowlist (internal/controlplane/request_fields.go) and its
  // validation error lists the allowed names, so it is not repeated here.
  return { value: parsed as Record<string, unknown> }
}

/** True when at least one pin field is set, so an empty block is not saved. */
export function providerRoutingSet(pin: UpstreamProviderSpec | null | undefined): boolean {
  if (!pin) return false
  return Object.entries(pin).some(([k, v]) => {
    if (k === 'max_price') return !!v && Object.keys(v).length > 0
    if (Array.isArray(v)) return v.length > 0
    if (typeof v === 'boolean') return true
    return v !== undefined && v !== ''
  })
}

/**
 * Prepares a model for saving: trimmed id, a non-null field map, an explicit
 * conflict policy, and no empty pin block (which the server would drop).
 */
export function cleanModel(m: ModelSpec): ModelSpec {
  return {
    ...m,
    id: m.id.trim(),
    request_fields: m.request_fields ?? {},
    request_field_policy: m.request_field_policy || 'configured',
    provider_routing: providerRoutingSet(m.provider_routing) ? m.provider_routing : undefined,
  }
}

/** True when the model sends anything upstream beyond the request itself. */
export function hasUpstreamFields(m: Pick<ModelSpec, 'provider_routing' | 'request_fields'>): boolean {
  return !!m.provider_routing || Object.keys(m.request_fields ?? {}).length > 0
}

/**
 * Drops a provider pin from models of a provider type that cannot carry one.
 * Switching the type while creating a provider would otherwise leave a pin
 * the server refuses.
 */
export function modelsForType(models: ModelSpec[], type: string): ModelSpec[] {
  return models.map((m) => {
    let next = m
    if (m.provider_routing && type !== 'openai_compatible') next = { ...next, provider_routing: undefined }
    // A reasoning option keeps only what the new type accepts.
    if (m.reasoning) next = { ...next, reasoning: reasoningForType(m.reasoning, type) }
    return next
  })
}

/**
 * Normalises a field name the way the server does: ASCII letters lowered,
 * `_` and `-` removed, so snake_case and camelCase spellings compare equal.
 */
export function normalizeFieldName(name: string): string {
  return name.replace(/[_-]/g, '').replace(/[A-Z]/g, (c) => c.toLowerCase())
}

/** True when a free-form key collides with the typed pin (any spelling). */
export function hasProviderField(fields: Record<string, unknown>): boolean {
  return Object.keys(fields).some((k) => normalizeFieldName(k) === 'provider')
}
