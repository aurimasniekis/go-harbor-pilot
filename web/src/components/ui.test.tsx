import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { render as rtlRender, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { theme } from '@/theme/theme'
import userEvent from '@testing-library/user-event'
import { ApiError } from '@/lib/api/client'
import { QuotaBar, Tabs } from './ui'
import { QueryError } from './states'

const render = (ui: React.ReactElement) => rtlRender(<MantineProvider theme={theme}>{ui}</MantineProvider>)

describe('QuotaBar', () => {
  it('shows Unknown without a fill for unknown values', () => {
    render(<QuotaBar label="5-hour" percent={null} />)
    // An unknown value is not a meter (a meter requires a value).
    expect(screen.queryByRole('meter')).toBeNull()
    const bar = screen.getByRole('img', { name: '5-hour: unknown' })
    expect(bar).not.toHaveAttribute('aria-valuenow')
    expect(bar).toHaveAttribute('data-unknown', 'true')
    expect(screen.getByText('Unknown')).toBeInTheDocument()
  })

  it('shows a real zero as used up', () => {
    render(<QuotaBar label="7-day" percent={0} />)
    const meter = screen.getByRole('meter', { name: '7-day' })
    expect(meter).toHaveAttribute('aria-valuenow', '0')
    expect(screen.getByText('Used up')).toBeInTheDocument()
  })

  it('marks stale values', () => {
    render(<QuotaBar label="5-hour" percent={42} stale />)
    expect(screen.getByRole('meter')).toHaveAttribute('aria-valuetext', '42 percent remaining, stale')
    expect(screen.getByText(/42% left · stale/)).toBeInTheDocument()
  })
})

describe('Tabs', () => {
  function Harness() {
    const [v, setV] = useState<'a' | 'b' | 'c'>('a')
    return (
      <Tabs
        label="Providers"
        value={v}
        onChange={setV}
        items={[
          { value: 'a', label: 'All', count: 3 },
          { value: 'b', label: 'Claude', disabled: true },
          { value: 'c', label: 'Codex' },
        ]}
      />
    )
  }

  it('moves with arrow keys and skips disabled tabs', async () => {
    const user = userEvent.setup()
    render(<Harness />)
    const all = screen.getByRole('tab', { name: /All/ })
    all.focus()
    await user.keyboard('{ArrowRight}')
    expect(screen.getByRole('tab', { name: 'Codex' })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('tab', { name: 'Codex' })).toHaveFocus()
    await user.keyboard('{ArrowRight}')
    expect(screen.getByRole('tab', { name: /All/ })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('tab', { name: 'Claude' })).toBeDisabled()
  })
})

describe('QueryError', () => {
  it('explains a server failure in plain words with a reference and a retry button', async () => {
    const retry = vi.fn()
    render(<QueryError error={new ApiError(500, 'internal', 'internal server error', 'req-123')} retry={retry} />)
    expect(screen.getByText('Could not load this')).toBeInTheDocument()
    expect(screen.getByText(/The server could not complete this/)).toBeInTheDocument()
    expect(screen.getByText('Reference req-123')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(retry).toHaveBeenCalledOnce()
  })

  it('treats a missing object as not found, not as a transient error', () => {
    render(<QueryError error={new ApiError(404, 'not_found', 'object not found', 'req-9')} retry={() => {}} />)
    expect(screen.getByText('Not found')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Try again' })).toBeNull()
  })
})

describe('remainingPercent', () => {
  it('uses one "remaining" convention and keeps unknown unknown', async () => {
    const { remainingPercent } = await import('./ui')
    expect(remainingPercent({ remaining_fraction: 0.25 })).toBe(25)
    expect(remainingPercent({ used: 30, limit: 40 })).toBe(25)
    expect(remainingPercent({ remaining: 10, limit: 40 })).toBe(25)
    expect(remainingPercent({ used: null, limit: null })).toBeNull()
    expect(remainingPercent({ remaining_fraction: 0 })).toBe(0)
  })
})
