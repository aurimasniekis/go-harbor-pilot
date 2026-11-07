import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RefForm } from './RefPicker'

function renderForm(existing: string[], onAdd = vi.fn()) {
  // No client list in this test: the query never resolves.
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, queryFn: () => new Promise(() => {}) } } })
  render(
    <QueryClientProvider client={client}>
      <MantineProvider>
        <RefForm existing={existing} onAdd={onAdd} />
      </MantineProvider>
    </QueryClientProvider>,
  )
  return onAdd
}

describe('RefForm', () => {
  it('adds a run reference', async () => {
    const onAdd = renderForm([])
    expect(screen.getByLabelText('Reference', { selector: 'input' })).toHaveValue('spaceman.run')
    await userEvent.type(screen.getByLabelText('Value'), 'run_a')
    await userEvent.click(screen.getByRole('button', { name: 'Add filter' }))
    expect(onAdd).toHaveBeenCalledWith({ key: 'spaceman.run', value: 'run_a' })
  })

  it('explains an empty value instead of adding it', async () => {
    const onAdd = renderForm([])
    await userEvent.click(screen.getByRole('button', { name: 'Add filter' }))
    expect(onAdd).not.toHaveBeenCalled()
    expect(screen.getByText(/Enter a value/)).toBeInTheDocument()
  })

  it('stops at four references', () => {
    renderForm(['a:1', 'b:1', 'c:1', 'd:1'])
    expect(screen.getByRole('button', { name: 'Add filter' })).toBeDisabled()
    expect(screen.getByText(/Up to 4 references/)).toBeInTheDocument()
  })
})
