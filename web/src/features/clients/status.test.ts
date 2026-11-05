import { describe, expect, it } from 'vitest'
import type { Credential } from '@/lib/api/types'
import { expirySeconds } from '@/features/config/IssueCredentialDialog'
import { clientStatus, sortTokens } from './status'

const on = { spec: { enabled: true } }

describe('clientStatus', () => {
  it.each([
    ['off', { spec: { enabled: false } }, 3, 10, 0],
    ['unknown', on, undefined, 0, 0],
    ['not_set_up', on, 0, 0, 0],
    ['failing', on, 1, 10, 6],
    ['working', on, 1, 10, 5],
    ['idle', on, 2, 0, 0],
  ] as const)('%s', (want, b, tokens, requests, failed) => {
    expect(clientStatus(b, tokens, requests, failed)).toBe(want)
  })
})

describe('sortTokens', () => {
  it('lists active tokens first, newest first', () => {
    const t = (id: string, created: string, revoked?: string) => ({ id, created_at: created, revoked_at: revoked }) as Credential
    const out = sortTokens([t('old', '2026-01-01'), t('revoked', '2026-03-01', '2026-03-02'), t('new', '2026-02-01')])
    expect(out.map((c) => c.id)).toEqual(['new', 'old', 'revoked'])
  })
})

describe('expirySeconds', () => {
  it.each([
    ['90', null, 90 * 86_400],
    ['never', null, 0],
    ['custom', 14, 14 * 86_400],
    ['custom', null, null],
    ['custom', 0, null],
  ] as const)('%s / %s', (choice, days, want) => {
    expect(expirySeconds(choice, days)).toBe(want)
  })
})
