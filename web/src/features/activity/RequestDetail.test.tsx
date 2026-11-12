import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import type { Attempt } from '@/lib/api/types'
import { Attempts, bridgeSession } from './RequestDetail'

function attempt(n: number, account: string, extra: Partial<Attempt>): Attempt {
  return {
    id: `att_${n}`,
    n,
    account_id: account,
    provider_id: 'prv_sub',
    model: 'claude-test-model',
    upstream_protocol: 'claude_code_bridge',
    mode: 'translated',
    status: 'error',
    error_kind: 'upstream',
    error_message: '',
    executed: false,
    failover_reason: '',
    started_at: '2026-11-09T12:00:00Z',
    duration_ms: null,
    ttft_ms: null,
    tokens: {},
    unknown_events: [],
    ...extra,
  } as Attempt
}

const names = { account: (id?: string) => id ?? '', provider: (id?: string) => id ?? '' }

describe('Attempts', () => {
  it('lists every attempt of a failed request with its account and error', () => {
    render(
      <MantineProvider>
        <Attempts
          names={names}
          attempts={[
            attempt(1, 'seat-a', { error_message: 'API Error: 400 first' }),
            attempt(2, 'seat-b', { error_message: 'API Error: 400 second', failover_reason: 'alternate_account' }),
          ]}
        />
      </MantineProvider>,
    )
    expect(screen.getByText(/#1 seat-a/)).toBeInTheDocument()
    expect(screen.getByText(/#2 seat-b/)).toBeInTheDocument()
    expect(screen.getByText('API Error: 400 first')).toBeInTheDocument()
    expect(screen.getByText('API Error: 400 second')).toBeInTheDocument()
  })
})

describe('bridgeSession', () => {
  it('names the CLI session that served a bridge request', () => {
    expect(bridgeSession([{ provenance: { complete: true, bridge_session: 'shelved_session' } }])?.label).toBe('CLI session resumed')
    expect(bridgeSession([{ provenance: { bridge_session: 'transcript' } }])?.label).toBe('CLI session rebuilt')
    expect(bridgeSession([{ provenance: { complete: true, mode: 'native' } }])).toBeUndefined()
    expect(bridgeSession([{ provenance: null }])).toBeUndefined()
  })
})
