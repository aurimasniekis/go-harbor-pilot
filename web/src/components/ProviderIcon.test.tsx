import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { ProviderIcon } from './ProviderIcon'

describe('ProviderIcon', () => {
  it('renders the brand mark in its brand color for Claude, including demo providers', () => {
    const { container } = render(<ProviderIcon id="demo_claude" family="claude" name="Claude (demo)" />)
    const svg = container.querySelector('svg')
    expect(svg).toHaveAttribute('aria-hidden', 'true')
    expect(svg?.querySelector('path')).toHaveAttribute('fill', '#D97757')
  })

  it('uses the Anthropic mark for the admin API adapter', () => {
    const { container } = render(<ProviderIcon id="anthropic_admin" family="claude" name="Anthropic API" />)
    expect(container.querySelector('path')?.getAttribute('d')).not.toBe(
      render(<ProviderIcon id="claude_subscription" family="claude" name="Claude" />).container.querySelector('path')?.getAttribute('d'),
    )
  })

  it('lets black brand marks follow the text color so they stay visible in dark mode', () => {
    const { container } = render(<ProviderIcon id="cursor" family="cursor" name="Cursor" />)
    expect(container.querySelector('path')).toHaveAttribute('fill', 'currentColor')
  })

  it('falls back to a monogram for providers without a mark', () => {
    expect(render(<ProviderIcon id="codex_subscription" family="openai" name="Codex" />).container.textContent).toBe('Cx')
    expect(render(<ProviderIcon id="openai_admin" family="openai" name="OpenAI API" />).container.textContent).toBe('AI')
    expect(render(<ProviderIcon id="nexos" family="nexos" name="nexos.ai" />).container.textContent).toBe('n')
    const unknown = render(<ProviderIcon id="acme" family="acme" name="Acme (demo)" />).container.firstElementChild
    expect(unknown).toHaveTextContent('A')
    expect(unknown).toHaveAttribute('aria-hidden', 'true')
  })
})
