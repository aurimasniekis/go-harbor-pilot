import type { ModelReasoningSpec, ModelSpec } from '@/lib/api/types'
import { reasoningRules as rules } from './reasoningRules.gen'

/**
 * A model's reasoning option (D-047): how the client's reasoning settings
 * are written for the model's upstream. The server validates it
 * (internal/controlplane/reasoning_spec.go); the rule tables these helpers
 * read are generated from the server's (reasoningRules.gen.ts), so the
 * editor offers only what the server accepts.
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

/** Effort levels, lowest first (`none` is mode off). */
export const ladder: readonly string[] = rules.ladder

const table = <T>(t: object) => t as Record<string, T>
const stylesByType = table<readonly string[]>(rules.stylesByType)
const styleProtocol = table<string>(rules.styleProtocol)
const styleLevels = table<readonly string[]>(rules.styleLevels)
const typeProtocols = table<readonly string[]>(rules.typeProtocols)
const protocolDefaultStyle = table<string>(rules.protocolDefaultStyle)
const defaultHistory = table<string>(rules.defaultHistoryField)
const budgetTypes: readonly string[] = rules.budgetTypes
const levelOnlyStyles: readonly string[] = rules.levelOnlyStyles
const reservedHistoryFields: readonly string[] = rules.reservedHistoryFields
const historyFieldRE = new RegExp(rules.historyFieldPattern)

/** True for the CLI bridges, which cannot carry the option. */
function bridgeType(type: string): boolean {
  return !(type in stylesByType)
}

/** The styles a provider type accepts in a mode (`none` only turns reasoning off). */
export function stylesFor(type: string, mode: string = 'off'): string[] {
  return (stylesByType[type] ?? []).filter((s) => s !== 'none' || mode === 'off')
}

/**
 * The methods a style ends up as on a provider type, one per protocol the
 * type may speak: the style where it writes that protocol, else the
 * protocol's default. `none` writes nothing.
 */
function methodsFor(style: string, type: string): string[] {
  return (typeProtocols[type] ?? [])
    .map((p) => (style && (styleProtocol[style] === '' || styleProtocol[style] === p) ? style : protocolDefaultStyle[p] ?? ''))
    .filter((m) => m && m !== 'none')
}

/** Levels the server accepts at all for a type: budget types only those their style maps to a budget. */
export function levelsFor(type: string, style = ''): string[] {
  if (!budgetTypes.includes(type) || style === 'none') return [...ladder]
  return sendableLevels(style, type)
}

/** Levels every method of the style sends as they are. */
export function sendableLevels(style: string, type: string): string[] {
  const ms = methodsFor(style, type)
  return ladder.filter((l) => ms.every((m) => (styleLevels[m] ?? []).includes(l)))
}

/** The levels a fixed option may name: the model's accepted levels when listed, else what the style sends. */
export function fixedLevels(r: ModelReasoningSpec, type: string): string[] {
  const allowed = levelsFor(type, r.style ?? '')
  if (r.efforts?.length) return ladder.filter((l) => r.efforts!.includes(l) && allowed.includes(l))
  return sendableLevels(r.style ?? '', type)
}

/** True when the style sends only a level, never a token budget (a fixed budget is refused). */
export function levelOnly(style: string, type: string): boolean {
  return methodsFor(style, type).some((m) => levelOnlyStyles.includes(m))
}

/** Earlier reasoning can be sent back only to Chat Completions upstreams. */
export function historyAllowed(type: string): boolean {
  return (rules.historyTypes as readonly string[]).includes(type)
}

/** The history field the option uses when none is named ('' when it must be named). */
export function defaultHistoryField(style: string): string {
  const m = style && (styleProtocol[style] === '' || styleProtocol[style] === rules.chatProtocol) ? style : protocolDefaultStyle[rules.chatProtocol] ?? ''
  return defaultHistory[m] ?? ''
}

/** A blank option for the editor to start from. */
export function emptyReasoning(): ModelReasoningSpec {
  return { mode: 'auto' }
}

/**
 * Prepares the option for saving: keeps every field the server accepts for
 * the mode and drops the rest, and returns undefined for an option that
 * changes nothing (the server stores that as no option).
 */
export function cleanReasoning(r: ModelReasoningSpec | undefined, type: string): ModelReasoningSpec | undefined {
  if (!r || bridgeType(type)) return undefined
  const mode = (r.mode || 'auto') as ReasoningMode
  const out: ModelReasoningSpec = { mode }
  if (mode !== 'auto' && r.style && (r.style !== 'none' || mode === 'off')) out.style = r.style
  if (mode === 'client' || mode === 'fixed') {
    if (r.effort) out.effort = r.effort
    else if (r.budget_tokens && !(mode === 'fixed' && levelOnly(out.style ?? '', type))) out.budget_tokens = r.budget_tokens
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

/** What the server would refuse in an option, or undefined when it is fine. */
export function reasoningProblem(r: ModelReasoningSpec | undefined, type: string): string | undefined {
  const c = cleanReasoning(r, type)
  if (!c) return undefined
  if (c.mode === 'fixed') {
    if (!c.effort && !c.budget_tokens) return 'Choose the level to send every time.'
    if (c.effort && !fixedLevels(c, type).includes(c.effort)) return `This parameter can't send ${c.effort}; choose another level or accept it below.`
  }
  if (c.effort && c.efforts?.length && !c.efforts.includes(c.effort)) return 'The level must be one of the accepted levels.'
  if (c.budget_tokens && c.max_budget_tokens && c.budget_tokens > c.max_budget_tokens) return 'The budget is larger than the largest thinking budget.'
  if (c.history === 'field') {
    const name = c.history_field ?? ''
    if (!name && !defaultHistoryField(c.style ?? '')) return 'Name the field earlier reasoning goes in.'
    if (name && !historyFieldRE.test(name)) return 'The field name must be a lowercase identifier, such as reasoning_content.'
    if (reservedHistoryFields.includes(name)) return `${name} is a standard message field; use another name.`
  }
  return undefined
}

/**
 * Keeps only what a provider type can carry after the type changes: no
 * option at all for a CLI bridge; otherwise styles, levels, budgets and
 * the history field the new type or style cannot take are reset.
 */
export function reasoningForType(r: ModelReasoningSpec | undefined, type: string): ModelReasoningSpec | undefined {
  if (!r || bridgeType(type)) return undefined
  const next = { ...r }
  if (next.style && !stylesFor(type, next.mode || 'auto').includes(next.style)) next.style = ''
  const levels = levelsFor(type, next.style ?? '')
  if (next.efforts) next.efforts = next.efforts.filter((e) => levels.includes(e))
  if (next.effort && !(next.mode === 'fixed' ? fixedLevels(next, type) : levels).includes(next.effort)) next.effort = ''
  if (next.mode === 'fixed' && next.budget_tokens && levelOnly(next.style ?? '', type)) next.budget_tokens = 0
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
