import { describe, expect, it } from 'vitest'
import type { AuditEntry } from '@/lib/api/types'
import { detailPairs, sentence, shortId } from './audit'

const names: Record<string, string> = { usr_1: 'Demo Admin', cbn_1: 'claude-code-laptop', pln_1: 'team-default' }
const n = { name: (id: string | undefined | null) => (id ? names[id] : undefined) }

function entry(p: Partial<AuditEntry>): AuditEntry {
  return { id: 'a', at: '2026-11-05T12:00:00Z', action: '', actor_id: 'usr_1', actor_kind: 'user', outcome: 'success', details: {}, ...p } as AuditEntry
}

describe('sentence', () => {
  it.each([
    [entry({ action: 'auth.login', details: { method: 'password' } }), 'Demo Admin signed in with a password'],
    [entry({ action: 'gateway_credential.issue', actor_id: 'system', actor_kind: 'system', target_id: 'cbn_1' }), 'System issued an access token for claude-code-laptop'],
    [entry({ action: 'routing_plan.publish', target_id: 'pln_1', details: { version: 2 } }), 'Demo Admin published team-default v2'],
    [entry({ action: 'model_alias.create', target_id: 'als_9', details: { name: 'coding/default' } }), 'Demo Admin created model name coding/default'],
    [entry({ action: 'client_binding.delete', target_id: 'cbn_x', details: {} }), 'Demo Admin deleted client cbn_x'],
    [entry({ action: 'auth.logout', actor_id: 'usr_01m6ntxscx7d196q9rm4z735kn' }), 'usr_01m6…35kn signed out'],
  ])('%#', (e, want) => {
    expect(sentence(e, n)).toBe(want)
  })

  it('falls back to a readable phrase for unknown actions', () => {
    expect(sentence(entry({ action: 'widget.frobnicate' }), n)).toBe('Demo Admin: widget frobnicate')
  })
})

describe('detailPairs', () => {
  it('flattens nested objects and lists without JSON', () => {
    expect(detailPairs({ name: 'x', managed_by: 'ui', scopes: ['invoke', 'models'], nested: { config_revision: 3 }, empty: '' })).toEqual([
      ['Name', 'x'],
      ['Managed by', 'ui'],
      ['Scopes', 'invoke, models'],
      ['Nested › Config revision', '3'],
    ])
  })
})

describe('shortId', () => {
  it('keeps short ids and shortens long ones', () => {
    expect(shortId('system')).toBe('system')
    expect(shortId('usr_01m6ntxscx7d196q9rm4z735kn')).toBe('usr_01m6…35kn')
  })
})
