import { describe, expect, it } from 'vitest'
import { baseURL, commands, guide, harnessKinds, shellArg } from './guides'

const input = { url: 'https://hp.example.com/', model: 'coding/default', bindingId: 'bnd_1' }

describe('guide', () => {
  it('never embeds a token', () => {
    for (const { value } of harnessKinds) {
      const g = guide(value, input)
      expect(g.snippet).not.toMatch(/hpk_/)
      for (const c of commands(value, input)) expect(c.cmd).not.toMatch(/hpk_/)
    }
  })

  it('points OpenAI-compatible harnesses at /v1 and Claude Code at the root', () => {
    expect(JSON.parse(guide('claude', input).snippet).env.ANTHROPIC_BASE_URL).toBe('https://hp.example.com')
    expect(guide('codex', input).snippet).toContain('base_url = "https://hp.example.com/v1"')
    expect(JSON.parse(guide('opencode', input).snippet).provider['harbor-pilot'].options.baseURL).toBe('https://hp.example.com/v1')
    expect(JSON.parse(guide('pi', input).snippet).providers['harbor-pilot'].baseUrl).toBe('https://hp.example.com/v1')
  })

  it('omits the model when none is chosen', () => {
    const g = guide('claude', { ...input, model: '' })
    expect(JSON.parse(g.snippet).env.ANTHROPIC_MODEL).toBeUndefined()
    expect(guide('codex', { ...input, model: '' }).snippet).not.toContain('model = ')
  })
})

describe('commands', () => {
  it('pipes the issued token into setup', () => {
    const c = commands('codex', input)[1]!.cmd
    expect(c).toContain('--binding bnd_1')
    expect(c).toContain('| harbor-pilot harness setup codex --url https://hp.example.com --token-stdin --model coding/default --yes')
  })

  it('uses a placeholder binding', () => {
    expect(commands('pi', { ...input, bindingId: '' })[1]!.cmd).toContain('--binding <binding-id>')
  })
})

describe('helpers', () => {
  it('normalizes the URL and quotes shell arguments', () => {
    expect(baseURL('')).toBe('https://harbor-pilot.example.com')
    expect(baseURL('http://x:8080///')).toBe('http://x:8080')
    expect(shellArg('a b')).toBe(`'a b'`)
    expect(shellArg("it's")).toBe(`'it'\\''s'`)
    expect(shellArg('coding/default')).toBe('coding/default')
  })
})
