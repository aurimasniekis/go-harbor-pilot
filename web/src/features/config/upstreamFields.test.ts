import { describe, expect, it } from 'vitest'
import type { ModelSpec } from '@/lib/api/types'
import { cleanModel, emptyProviderRouting, formatRequestFields, hasProviderField, hasUpstreamFields, modelsForType, parseRequestFields, providerRoutingSet } from './upstreamFields'

describe('parseRequestFields', () => {
  it('treats blank text as no fields', () => {
    expect(parseRequestFields('   \n ')).toEqual({ value: {} })
  })

  it('reads a JSON object', () => {
    expect(parseRequestFields('{"transforms":["middle-out"]}')).toEqual({ value: { transforms: ['middle-out'] } })
  })

  it('reports invalid JSON instead of dropping it silently', () => {
    const r = parseRequestFields('{"transforms":')
    expect(r.value).toEqual({})
    expect(r.error).toMatch(/not valid JSON/)
  })

  it('refuses a value that is not an object', () => {
    expect(parseRequestFields('[1,2]').error).toMatch(/JSON object/)
    expect(parseRequestFields('"text"').error).toMatch(/JSON object/)
    expect(parseRequestFields('null').error).toMatch(/JSON object/)
  })

  it('leaves the allowlist to the server, which knows the provider type', () => {
    expect(parseRequestFields('{"temperature":0.2}')).toEqual({ value: { temperature: 0.2 } })
    expect(parseRequestFields('{"max_tokens":10}').error).toBeUndefined()
  })
})

describe('formatRequestFields', () => {
  it('is empty for no fields and stable in key order', () => {
    expect(formatRequestFields({})).toBe('')
    expect(formatRequestFields(undefined)).toBe('')
    expect(formatRequestFields({ b: 1, a: 2 })).toBe('{\n  "a": 2,\n  "b": 1\n}')
  })
})

describe('hasProviderField', () => {
  it('matches the pin key regardless of case', () => {
    expect(hasProviderField({ provider: {} })).toBe(true)
    expect(hasProviderField({ Provider: {} })).toBe(true)
    expect(hasProviderField({ transforms: [] })).toBe(false)
  })
})

describe('providerRoutingSet', () => {
  it('is false for an absent or empty block', () => {
    expect(providerRoutingSet(undefined)).toBe(false)
    expect(providerRoutingSet({})).toBe(false)
    expect(providerRoutingSet(emptyProviderRouting())).toBe(false)
    expect(providerRoutingSet({ max_price: {} })).toBe(false)
    expect(providerRoutingSet({ sort: '' })).toBe(false)
  })

  it('is true as soon as any field is set, including a false switch', () => {
    expect(providerRoutingSet({ order: ['provider-a'] })).toBe(true)
    expect(providerRoutingSet({ sort: 'price' })).toBe(true)
    expect(providerRoutingSet({ allow_fallbacks: false })).toBe(true)
    expect(providerRoutingSet({ max_price: { prompt: 1.5 } })).toBe(true)
  })
})

const base = (p: Partial<ModelSpec>): ModelSpec => ({
  id: ' m1 ',
  display_name: '',
  context_window: 0,
  max_output_tokens: 0,
  capabilities: ['streaming'],
  request_fields: {},
  request_field_policy: 'configured',
  ...p,
})

describe('cleanModel', () => {
  it('trims the id and keeps a real pin', () => {
    const m = cleanModel(base({ provider_routing: { order: ['provider-a'] } }))
    expect(m.id).toBe('m1')
    expect(m.provider_routing?.order).toEqual(['provider-a'])
  })

  it('drops an empty pin and an empty field map stays an object', () => {
    const m = cleanModel(base({ provider_routing: emptyProviderRouting() }))
    expect(m.provider_routing).toBeUndefined()
    expect(m.request_fields).toEqual({})
    expect(m.request_field_policy).toBe('configured')
  })
})

describe('hasUpstreamFields', () => {
  it('is true only when something will be sent', () => {
    expect(hasUpstreamFields({ request_fields: {} })).toBe(false)
    expect(hasUpstreamFields({ request_fields: {}, provider_routing: { order: ['x'] } })).toBe(true)
    expect(hasUpstreamFields({ request_fields: { transforms: [] } })).toBe(true)
  })
})

describe('modelsForType', () => {
  it('keeps a pin on an openai-compatible provider and drops it elsewhere', () => {
    const models = [base({ provider_routing: { order: ['provider-a'] }, request_fields: { transforms: [] } })]
    expect(modelsForType(models, 'openai_compatible')[0]!.provider_routing?.order).toEqual(['provider-a'])
    const anthropic = modelsForType(models, 'anthropic')[0]!
    expect(anthropic.provider_routing).toBeUndefined()
    // The free-form fields are still valid on every HTTP provider.
    expect(anthropic.request_fields).toEqual({ transforms: [] })
  })
})
