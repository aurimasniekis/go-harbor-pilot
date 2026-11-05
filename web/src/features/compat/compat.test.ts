import { describe, expect, it } from 'vitest'
import { countdown, effectiveState, isDefault, missingForPassthrough, splitPath, withDefaults } from './compat'

describe('compatibility policy helpers', () => {
  it('applies server defaults', () => {
    expect(withDefaults(undefined)).toEqual({ on_breaker_open: 'fail', allowed_losses: [], unknown_usage: 'record', unenforceable_budget: 'block' })
    expect(isDefault(undefined)).toBe(true)
    expect(isDefault({ on_breaker_open: 'fail', allowed_losses: ['traces'] })).toBe(false)
  })

  it('lists losses passthrough still needs', () => {
    expect(missingForPassthrough({ on_breaker_open: 'fail' })).toEqual([])
    expect(missingForPassthrough({ on_breaker_open: 'passthrough' })).toEqual(['detailed_usage', 'tool_statistics'])
    expect(missingForPassthrough({ on_breaker_open: 'passthrough', allowed_losses: ['detailed_usage'] })).toEqual(['tool_statistics'])
    expect(missingForPassthrough({ on_breaker_open: 'passthrough', allowed_losses: ['tool_statistics', 'detailed_usage'] })).toEqual([])
  })
})

describe('breaker helpers', () => {
  it('applies overrides to the state', () => {
    expect(effectiveState('open', 'auto')).toBe('open')
    expect(effectiveState('open', 'force_closed')).toBe('closed')
    expect(effectiveState('closed', 'force_open')).toBe('open')
  })

  it('formats countdowns', () => {
    const now = Date.parse('2026-11-05T12:00:00Z')
    expect(countdown(null, now)).toBe('—')
    expect(countdown('2026-11-05T11:59:00Z', now)).toBe('due now')
    expect(countdown('2026-11-05T12:00:42Z', now)).toBe('in 42s')
    expect(countdown('2026-11-05T12:01:20Z', now)).toBe('in 1m 20s')
    expect(countdown('2026-11-05T14:05:00Z', now)).toBe('in 2h 5m')
  })

  it('splits paths', () => {
    expect(splitPath('anthropic_messages>openai_chat')).toEqual({ inbound: 'anthropic_messages', upstream: 'openai_chat' })
  })
})
