import { describe, expect, it } from 'vitest'
import {
  cleanReasoning,
  defaultHistoryField,
  fixedLevels,
  levelOnly,
  levelsFor,
  reasoningBadge,
  reasoningForType,
  reasoningProblem,
  sendableLevels,
  stylesFor,
} from './reasoning'
import { modelsForType } from './upstreamFields'
import type { ModelSpec } from '@/lib/api/types'

// The rule tables come from the server (reasoningRules.gen.ts, checked by
// internal/controlplane TestUIReasoningRules); these tests pin how the
// editor reads them.

describe('cleanReasoning', () => {
  it('drops an option that changes nothing, so the spec stays unchanged', () => {
    expect(cleanReasoning(undefined, 'openai_compatible')).toBeUndefined()
    expect(cleanReasoning({ mode: 'auto' }, 'openai_compatible')).toBeUndefined()
    expect(cleanReasoning({ mode: '', style: 'openrouter_reasoning', effort: 'low' }, 'openai_compatible')).toBeUndefined()
  })

  it('keeps every field the server accepts for the mode (M-C)', () => {
    expect(cleanReasoning({ mode: 'off', style: 'deepseek_thinking', effort: 'high', efforts: ['low'] }, 'openai_compatible'))
      .toEqual({ mode: 'off', style: 'deepseek_thinking' })
    // A fixed level outside the style's usual levels, accepted by efforts,
    // and a budget cap: both survive an open-and-save.
    expect(cleanReasoning({ mode: 'fixed', effort: 'xhigh', efforts: ['xhigh', 'high'], max_budget_tokens: 9000 }, 'openai_compatible'))
      .toEqual({ mode: 'fixed', effort: 'xhigh', efforts: ['high', 'xhigh'], max_budget_tokens: 9000 })
    expect(cleanReasoning({ mode: 'fixed', style: 'openrouter_reasoning', budget_tokens: 4000, max_budget_tokens: 8000 }, 'openai_compatible'))
      .toEqual({ mode: 'fixed', style: 'openrouter_reasoning', budget_tokens: 4000, max_budget_tokens: 8000 })
    expect(cleanReasoning({ mode: 'client', efforts: ['high', 'low'], max_budget_tokens: 8000 }, 'openai_compatible'))
      .toEqual({ mode: 'client', efforts: ['low', 'high'], max_budget_tokens: 8000 })
  })

  it('drops a fixed budget for styles that send only a level, and style none outside off', () => {
    expect(cleanReasoning({ mode: 'fixed', style: 'deepseek_thinking', budget_tokens: 4000 }, 'openai_compatible'))
      .toEqual({ mode: 'fixed', style: 'deepseek_thinking' })
    expect(cleanReasoning({ mode: 'client', style: 'none' }, 'openai_compatible')).toEqual({ mode: 'client' })
    expect(levelOnly('', 'ollama')).toBe(true)
    expect(levelOnly('openrouter_reasoning', 'openai_compatible')).toBe(false)
    expect(levelOnly('', 'anthropic')).toBe(false)
  })

  it('keeps a history field only for Chat upstreams, also in auto mode', () => {
    expect(cleanReasoning({ mode: 'auto', history: 'field', history_field: ' thoughts ' }, 'ollama'))
      .toEqual({ mode: 'auto', history: 'field', history_field: 'thoughts' })
    expect(cleanReasoning({ mode: 'auto', history: 'field' }, 'openai')).toBeUndefined()
  })

  it('stores nothing for a CLI bridge', () => {
    expect(cleanReasoning({ mode: 'off' }, 'claude_code')).toBeUndefined()
  })
})

describe('levels and styles per type (M-B)', () => {
  it('offers style none only to turn reasoning off', () => {
    expect(stylesFor('openai_compatible', 'client')).toEqual(['openai_reasoning_effort', 'openrouter_reasoning', 'deepseek_thinking'])
    expect(stylesFor('openai_compatible', 'off')).toContain('none')
    expect(stylesFor('anthropic', 'fixed')).toEqual(['anthropic_thinking'])
  })

  it('offers the levels a fixed style can send', () => {
    expect(sendableLevels('deepseek_thinking', 'openai_compatible')).toEqual(['low', 'high', 'max'])
    expect(sendableLevels('', 'ollama')).toEqual(['low', 'medium', 'high'])
    expect(sendableLevels('openrouter_reasoning', 'openai_compatible')).toEqual(['minimal', 'low', 'medium', 'high', 'xhigh', 'max'])
    // An openai provider answers with Chat or Responses: only levels both send.
    expect(sendableLevels('', 'openai')).toEqual(['low', 'medium', 'high'])
    expect(fixedLevels({ mode: 'fixed', style: 'deepseek_thinking' }, 'openai_compatible')).toEqual(['low', 'high', 'max'])
    expect(fixedLevels({ mode: 'fixed', efforts: ['high', 'xhigh'] }, 'ollama')).toEqual(['high', 'xhigh'])
  })

  it('keeps budget types to levels with a budget', () => {
    expect(levelsFor('gemini')).not.toContain('max')
    expect(levelsFor('anthropic')).toEqual(['minimal', 'low', 'medium', 'high', 'xhigh'])
    expect(levelsFor('openai_compatible')).toContain('max')
  })

  it('names the history field a style falls back to', () => {
    expect(defaultHistoryField('deepseek_thinking')).toBe('reasoning_content')
    expect(defaultHistoryField('openrouter_reasoning')).toBe('reasoning')
    expect(defaultHistoryField('')).toBe('')
  })
})

describe('reasoningProblem', () => {
  it('reports what the server would refuse', () => {
    expect(reasoningProblem({ mode: 'fixed' }, 'openai_compatible')).toMatch(/Choose the level/)
    expect(reasoningProblem({ mode: 'fixed', style: 'deepseek_thinking', effort: 'medium' }, 'openai_compatible')).toMatch(/can't send medium/)
    expect(reasoningProblem({ mode: 'fixed', style: 'deepseek_thinking', effort: 'high' }, 'openai_compatible')).toBeUndefined()
    // L-1: a history field without a name and no style default (also after
    // switching back to Built in).
    expect(reasoningProblem({ mode: 'auto', style: 'deepseek_thinking', history: 'field' }, 'openai_compatible')).toMatch(/Name the field/)
    expect(reasoningProblem({ mode: 'client', style: 'deepseek_thinking', history: 'field' }, 'openai_compatible')).toBeUndefined()
    expect(reasoningProblem({ mode: 'auto', history: 'field', history_field: 'Bad-Name' }, 'ollama')).toMatch(/lowercase identifier/)
    expect(reasoningProblem({ mode: 'auto', history: 'field', history_field: 'reasoning_details' }, 'ollama')).toMatch(/standard message field/)
    expect(reasoningProblem({ mode: 'client', budget_tokens: 9000, max_budget_tokens: 8000 }, 'openai_compatible')).toMatch(/larger than/)
  })
})

describe('provider type changes (M-D)', () => {
  it('drops the option for a CLI bridge', () => {
    expect(reasoningForType({ mode: 'client', style: '' }, 'claude_code')).toBeUndefined()
    const models = [{ id: 'm', reasoning: { mode: 'client' } } as ModelSpec]
    expect(modelsForType(models, 'codex')[0]!.reasoning).toBeUndefined()
  })

  it('resets what the new type or style cannot take', () => {
    expect(reasoningForType({ mode: 'client', style: 'openrouter_reasoning', effort: 'max', efforts: ['high', 'max'], history: 'field', history_field: 'x' }, 'anthropic'))
      .toEqual({ mode: 'client', style: '', effort: '', efforts: ['high'], history: '', history_field: '' })
    // A fixed budget moved to a type whose default style sends only a level.
    expect(reasoningForType({ mode: 'fixed', budget_tokens: 4000 }, 'openai')).toMatchObject({ budget_tokens: 0 })
    // A fixed level the new type's style cannot send.
    expect(reasoningForType({ mode: 'fixed', effort: 'minimal' }, 'openai_compatible')).toMatchObject({ effort: '' })
    expect(reasoningForType({ mode: 'fixed', effort: 'high' }, 'openai_compatible')).toMatchObject({ effort: 'high' })
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
