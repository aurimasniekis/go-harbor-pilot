import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { theme } from '@/theme/theme'
import type { ModelSpec } from '@/lib/api/types'
import { ModelDialog } from './ProviderDialog'

const model = (p: Partial<ModelSpec> = {}): ModelSpec => ({
  id: 'vendor/model-a',
  display_name: '',
  context_window: 0,
  max_output_tokens: 0,
  capabilities: ['streaming'],
  request_fields: {},
  request_field_policy: 'configured',
  ...p,
})

function open(m: ModelSpec, type: 'openai_compatible' | 'anthropic' = 'openai_compatible') {
  const onSave = vi.fn()
  render(
    <MantineProvider theme={theme}>
      <ModelDialog model={m} providerType={type} onSave={onSave} onClose={() => {}} />
    </MantineProvider>,
  )
  return onSave
}

/** Expands the section, which starts collapsed unless the model already sends fields. */
async function showFields() {
  const toggle = screen.getByRole('button', { name: /Upstream request fields/ })
  if (toggle.getAttribute('aria-expanded') !== 'true') await userEvent.click(toggle)
  return toggle
}

/** Expands the section and waits for the panel to render its content. */
async function showPin() {
  await showFields()
  return screen.findByLabelText(/Pin the upstream provider/)
}

const json = () => screen.getByLabelText('Other fields (JSON object)')
const policy = () => screen.getByLabelText('When the client already sends one of these', { selector: 'input' })
const done = () => screen.getByRole('button', { name: 'Done' })

describe('ModelDialog upstream request fields', () => {
  it('offers the provider pin only for an openai-compatible provider', async () => {
    open(model(), 'anthropic')
    await showFields()
    // The pin is vendor-specific; the free-form escape hatch and the
    // conflict rule apply everywhere.
    expect(screen.queryByLabelText(/Pin the upstream provider/)).toBeNull()
    expect(json()).toBeInTheDocument()
    expect(policy()).toBeInTheDocument()
  })

  it('pins a provider and saves it', async () => {
    const onSave = open(model())
    await userEvent.click(await showPin())
    await userEvent.click(screen.getByLabelText('Prefer in order', { selector: 'input' }))
    await userEvent.paste('provider-a')
    await userEvent.keyboard('{Enter}')
    await userEvent.click(done())

    const saved = onSave.mock.calls[0]![0] as ModelSpec
    expect(saved.provider_routing?.order).toEqual(['provider-a'])
    expect(saved.request_fields).toEqual({})
  })

  it('keeps a false switch, which the server would otherwise omit', async () => {
    const onSave = open(model({ provider_routing: { order: ['provider-a'], allow_fallbacks: false } }))
    await showPin()
    await userEvent.click(done())
    expect((onSave.mock.calls[0]![0] as ModelSpec).provider_routing?.allow_fallbacks).toBe(false)
  })

  it('reads the free-form JSON and explains a bad value instead of saving it', async () => {
    const onSave = open(model())
    await showFields()
    await userEvent.click(json())
    await userEvent.paste('{"transforms":["middle-out"]}')
    await userEvent.click(done())
    expect((onSave.mock.calls[0]![0] as ModelSpec).request_fields).toEqual({ transforms: ['middle-out'] })

    onSave.mockClear()
    await userEvent.clear(json())
    await userEvent.click(json())
    await userEvent.paste('{"transforms":')
    expect(screen.getByText(/not valid JSON/)).toBeInTheDocument()
    // A half-typed object blocks saving instead of storing a broken spec.
    expect(done()).toBeDisabled()
    expect(onSave).not.toHaveBeenCalled()
  })

  it('leaves the allowlist to the server and says so', async () => {
    open(model())
    await showFields()
    await userEvent.click(json())
    await userEvent.paste('{"temperature":0.2}')
    expect(screen.getByText(/Only fields this provider type allows/)).toBeInTheDocument()
    expect(done()).toBeEnabled()
  })

  it('refuses to save both a pin and a provider field', async () => {
    open(model({ request_fields: { provider: { order: ['x'] } } }))
    await userEvent.click(await showPin())
    expect(screen.getByText(/Remove this provider entry or the pin above/)).toBeInTheDocument()
    expect(done()).toBeDisabled()
  })

  it('opens with a saved pin, counts it, and keeps the policy', async () => {
    const onSave = open(model({ provider_routing: { order: ['provider-a'] }, request_field_policy: 'client' }))
    expect(await showFields()).toHaveTextContent('(1)')
    expect(policy()).toHaveValue('Keep theirs, only fill missing ones')
    await userEvent.click(done())
    const saved = onSave.mock.calls[0]![0] as ModelSpec
    expect(saved.provider_routing?.order).toEqual(['provider-a'])
    expect(saved.request_field_policy).toBe('client')
  })
})
