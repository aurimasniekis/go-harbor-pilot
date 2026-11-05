import { describe, expect, it } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { loginErrorMessage } from './login'
import { idleText, orderSessions, uaSummary } from './account'
import type { WebSession } from '@/lib/api/types'

describe('loginErrorMessage', () => {
  it.each([
    ['nothing', null, null],
    ['wrong password', new ApiError(401, 'invalid_credentials', 'invalid email or password'), 'Email or password is incorrect.'],
    ['password sign-in off', new ApiError(401, 'invalid_credentials', 'password sign-in is disabled; use single sign-on'), 'Password sign-in is turned off. Use single sign-on.'],
    ['throttled', new ApiError(429, 'too_many_requests', 'slow down'), 'Too many attempts. Wait a minute and try again.'],
    ['other server text', new ApiError(500, 'internal', 'database is locked'), 'Database is locked.'],
    ['network', new TypeError('fetch failed'), 'Sign-in failed. Check your connection and try again.'],
  ])('%s', (_, err, want) => {
    expect(loginErrorMessage(err)).toBe(want)
  })
})

const s = (over: Partial<WebSession>): WebSession =>
  ({ id: 'x', auth_method: 'password', created_at: '2026-01-01T00:00:00Z', current: false, expires_at: '2026-01-01T01:00:00Z', last_seen_at: '2026-01-01T00:00:00Z', ...over }) as WebSession

describe('account helpers', () => {
  it('puts this device first, then the most recent', () => {
    const got = orderSessions([
      s({ id: 'old', last_seen_at: '2026-01-01T00:00:00Z' }),
      s({ id: 'new', last_seen_at: '2026-01-02T00:00:00Z' }),
      s({ id: 'me', current: true, last_seen_at: '2025-12-01T00:00:00Z' }),
    ])
    expect(got.map((x) => x.id)).toEqual(['me', 'new', 'old'])
  })
  it('describes idle expiry', () => {
    const now = Date.parse('2026-01-01T00:00:00Z')
    expect(idleText({ expires_at: '2026-01-01T00:30:00Z' }, now)).toBe('Ends after 30 min idle')
    expect(idleText({ expires_at: '2025-12-31T23:00:00Z' }, now)).toBe('Ending now')
  })
  it.each([
    ['Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/130.0 Safari/537.36', 'Chrome on macOS'],
    ['Mozilla/5.0 (Windows NT 10.0) Gecko/20100101 Firefox/131.0', 'Firefox on Windows'],
    [undefined, 'Unknown device'],
  ])('summarises %s', (ua, want) => {
    expect(uaSummary(ua)).toBe(want)
  })
})
