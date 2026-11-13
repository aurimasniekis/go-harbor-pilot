import { describe, expect, it } from 'vitest'
import { cleanReasoning, levelOnly, levelsFor, reasoningBadge, reasoningForType, stylesFor } from './reasoning'
import { modelsForType } from './upstreamFields'
import type { ModelSpec } from '@/lib/api/types'

describe('cleanReasoning', () => {
  it('drops an option that changes nothing, so the spec stays unchanged', () => {
    expect(cleanReasoning(undefined, 'openai_compatible')).toBeUndefined()
    expect(cleanReasoning({ mode: 'auto' }, 'openai_compatible')).toBeUndefined()
    expect(cleanReasoning({ mode: '', style: 'openrouter_reasoning', effort: 'low' }, 'openai_compatible')).toBeUndefined()
  })

  it('keeps only what the mode uses', () => {
    expect(cleanReasoning({ mode: 'off', style: 'deepseek_thinking', effort: 'high', efforts: ['low'] }, 'openai_compatible'))
      .toEqual({ mode: 'off', style: 'deepseek_thinking' })
    expect(cleanReasoning({ mode: 'fixed', effort: 'low', budget_tokens: 4000, efforts: ['low'], max_budget_tokens: 9 }, 'openai_compatible'))
      .toEqual({ mode: 'fixed', effort: 'low' })
    expect(cleanReasoning({ mode: 'client', efforts: ['high', 'low'], max_budget_tokens: 8000 }, 'openai_compatible'))
      .toEqual({ mode: 'client', efforts: ['low', 'high'], max_budget_tokens: 8000 })
  })

  it('drops a fixed budget for styles that send only a level', () => {
    expect(cleanReasoning({ mode: 'fixed', style: 'deepseek_thinking', budget_tokens: 4000 }, 'openai_compatible'))
      .toEqual({ mode: 'fixed', style: 'deepseek_thinking' })
    expect(cleanReasoning({ mode: 'fixed', style: 'openrouter_reasoning', budget_tokens: 4000 }, 'openai_compatible'))
      .toEqual({ mode: 'fixed', style: 'openrouter_reasoning', budget_tokens: 4000 })
    expect(levelOnly('', 'ollama')).toBe(true)
    expect(levelOnly('', 'anthropic')).toBe(false)
  })

  it('keeps a history field only for Chat upstreams, also in auto mode', () => {
    expect(cleanReasoning({ mode: 'auto', history: 'field', history_field: ' thoughts ' }, 'ollama'))
      .toEqual({ mode: 'auto', history: 'field', history_field: 'thoughts' })
    expect(cleanReasoning({ mode: 'auto', history: 'field' }, 'openai')).toBeUndefined()
  })
})

describe('provider types', () => {
  it('offers the styles and levels the server accepts', () => {
    expect(stylesFor('openai_compatible')).toContain('openrouter_reasoning')
    expect(stylesFor('anthropic')).not.toContain('openrouter_reasoning')
    expect(levelsFor('gemini')).not.toContain('max')
    expect(levelsFor('openai_compatible')).toContain('max')
  })

  it('drops what a new provider type cannot carry', () => {
    const r = reasoningForType({ mode: 'client', style: 'openrouter_reasoning', effort: 'max', efforts: ['high', 'max'], history: 'field', history_field: 'x' }, 'anthropic')
    expect(r).toEqual({ mode: 'client', style: '', effort: '', efforts: ['high'], history: '', history_field: '' })
    const models = [{ id: 'm', reasoning: { mode: 'off', style: 'deepseek_thinking' } } as ModelSpec]
    expect(modelsForType(models, 'gemini')[0]!.reasoning?.style).toBe('')
    expect(modelsForType(models, 'openai_compatible')[0]!.reasoning?.style).toBe('deepseek_thinking')
  })
})

describe('reasoningBadge', () => {
  it('names the mode', () => {
    expect(reasoningBadge({ reasoning: undefined })).toBeUndefined()
    expect(reasoningBadge({ reasoning: { mode: 'client' } })).toBe('Reasoning: client')
    expect(reasoningBadge({ reasoning: { mode: 'fixed', effort: 'low' } })).toBe('Reasoning: low')
    expect(reasoningBadge({ reasoning: { mode: 'fixed', budget_tokens: 4096 } })).toBe('Reasoning: 4096 tokens')
    expect(reasoningBadge({ reasoning: { mode: 'off' } })).toBe('Reasoning off')
    expect(reasoningBadge({ reasoning: { mode: 'auto', history: 'field' } })).toBe('Reasoning history')
  })
})
