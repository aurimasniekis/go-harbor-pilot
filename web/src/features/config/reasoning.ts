import type { ModelReasoningSpec, ModelSpec } from '@/lib/api/types'

/**
 * A model's reasoning option (D-047): how the client's reasoning settings
 * are written for the model's upstream. The server validates it
 * (internal/controlplane/reasoning_spec.go); these helpers keep the editor
 * from offering what the server would refuse.
 */

export type ReasoningMode = 'auto' | 'client' | 'fixed' | 'off'

export const modeOptions: { value: ReasoningMode; label: string }[] = [
  { value: 'auto', label: 'Built in (default)' },
  { value: 'client', label: 'Follow the client' },
  { value: 'fixed', label: 'Always the same level' },
  { value: 'off', label: 'Turn reasoning off' },
]

/** Plain names for the styles, which are the upstream's parameter shapes. */
export const styleLabels: Record<string, string> = {
  anthropic_thinking: 'Anthropic thinking',
  openai_reasoning_effort: 'reasoning_effort',
  openai_responses_reasoning: 'Responses reasoning',
  openrouter_reasoning: 'Router reasoning object',
  deepseek_thinking: 'DeepSeek thinking toggle',
  gemini_thinking_config: 'Gemini thinking budget',
  none: 'Send nothing',
}

/** The styles each provider type accepts (mirrors the server). */
const stylesByType: Record<string, string[]> = {
  anthropic: ['anthropic_thinking', 'none'],
  openai: ['openai_reasoning_effort', 'openai_responses_reasoning', 'none'],
  gemini: ['gemini_thinking_config', 'none'],
  openai_compatible: ['openai_reasoning_effort', 'openrouter_reasoning', 'deepseek_thinking', 'none'],
  ollama: ['openai_reasoning_effort', 'none'],
}

export function stylesFor(type: string): string[] {
  return stylesByType[type] ?? []
}

/** Effort levels, lowest first (`none` is mode off). */
export const ladder = ['minimal', 'low', 'medium', 'high', 'xhigh', 'max'] as const

/** Anthropic and Gemini turn a level into a thinking budget, which `max` has none of. */
export function levelsFor(type: string): string[] {
  return type === 'anthropic' || type === 'gemini' ? ladder.filter((l) => l !== 'max') : [...ladder]
}

/** Earlier reasoning can be sent back only to Chat Completions upstreams. */
export function historyAllowed(type: string): boolean {
  return type === 'openai_compatible' || type === 'ollama'
}

/** The history field a style uses when none is named. */
export function defaultHistoryField(style: string): string {
  return { deepseek_thinking: 'reasoning_content', openrouter_reasoning: 'reasoning' }[style] ?? ''
}

/** A blank option for the editor to start from. */
export function emptyReasoning(): ModelReasoningSpec {
  return { mode: 'auto' }
}

/**
 * Prepares the option for saving: keeps only what the mode uses, and
 * returns undefined for an option that changes nothing (the server stores
 * that as no option, so the model's spec stays unchanged).
 */
export function cleanReasoning(r: ModelReasoningSpec | undefined, type: string): ModelReasoningSpec | undefined {
  if (!r) return undefined
  const mode = (r.mode || 'auto') as ReasoningMode
  const out: ModelReasoningSpec = { mode }
  if (mode !== 'auto' && r.style) out.style = r.style
  if (mode === 'client' || mode === 'fixed') {
    if (r.effort) out.effort = r.effort
    else if (r.budget_tokens) out.budget_tokens = r.budget_tokens
  }
  if (mode === 'client') {
    if (r.efforts?.length) out.efforts = ladder.filter((l) => r.efforts!.includes(l))
    if (r.max_budget_tokens) out.max_budget_tokens = r.max_budget_tokens
  }
  if (historyAllowed(type) && r.history === 'field') {
    out.history = 'field'
    if (r.history_field?.trim()) out.history_field = r.history_field.trim()
  }
  if (mode === 'auto' && !out.history) return undefined
  return out
}

/**
 * Drops what a provider type cannot carry after the type changes: a style
 * it does not accept, levels without a budget, a Chat history field.
 */
export function reasoningForType(r: ModelReasoningSpec | undefined, type: string): ModelReasoningSpec | undefined {
  if (!r) return undefined
  const next = { ...r }
  if (next.style && !stylesFor(type).includes(next.style)) next.style = ''
  const levels = levelsFor(type)
  if (next.effort && !levels.includes(next.effort)) next.effort = ''
  if (next.efforts) next.efforts = next.efforts.filter((e) => levels.includes(e))
  if (!historyAllowed(type)) {
    next.history = ''
    next.history_field = ''
  }
  return next
}

/** A short label for the model list, or undefined when the option is unset. */
export function reasoningBadge(m: Pick<ModelSpec, 'reasoning'>): string | undefined {
  const r = m.reasoning
  if (!r) return undefined
  const mode = r.mode || 'auto'
  if (mode === 'auto') return r.history === 'field' ? 'Reasoning history' : undefined
  return { client: 'Reasoning: client', fixed: `Reasoning: ${r.effort || `${r.budget_tokens} tokens`}`, off: 'Reasoning off' }[mode]
}
