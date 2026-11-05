import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { PageHeader, Unknown } from '@/ui/kit'
import { requestCost } from './RequestsPage'

describe('requestCost', () => {
  it.each([
    ['0.0026', 'USD', '$0.0026'],
    ['0.00052', 'USD', '$0.0005'],
    ['0.0105', 'USD', '$0.0105'],
    ['1.5', 'USD', '$1.50'],
    ['0', 'EUR', '€0.00'],
    ['0.02', 'CHF', 'CHF 0.0200'],
  ])('%s %s', (amount, currency, want) => {
    expect(requestCost(amount, currency)).toBe(want)
  })
})

describe('Unknown', () => {
  it('names the reason without adding a tab stop', () => {
    const { container } = render(
      <MantineProvider>
        <Unknown reason="No price configured" />
      </MantineProvider>,
    )
    expect(screen.getByText('No price configured')).toBeInTheDocument()
    expect(container.querySelector('[tabindex]')).toBeNull()
  })
})

describe('PageHeader', () => {
  it('names the browser tab after the page and resets it on leave', () => {
    const { unmount } = render(
      <MantineProvider>
        <PageHeader title="Requests" />
      </MantineProvider>,
    )
    expect(document.title).toBe('Requests · Harbor Pilot')
    unmount()
    expect(document.title).toBe('Harbor Pilot')
  })
})
