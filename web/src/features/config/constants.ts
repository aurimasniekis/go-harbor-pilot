import type { ProviderSpec } from '@/lib/api/types'

export type ProviderType = ProviderSpec['type']

export const providerTypes: { value: ProviderType; label: string; bridge?: boolean; requiresBaseURL?: boolean; defaultBaseURL: string }[] = [
  { value: 'anthropic', label: 'Anthropic API', defaultBaseURL: 'https://api.anthropic.com' },
  { value: 'openai', label: 'OpenAI API', defaultBaseURL: 'https://api.openai.com/v1' },
  { value: 'gemini', label: 'Gemini API', defaultBaseURL: 'https://generativelanguage.googleapis.com' },
  { value: 'openai_compatible', label: 'OpenAI-compatible API', requiresBaseURL: true, defaultBaseURL: '' },
  { value: 'ollama', label: 'Ollama (local)', defaultBaseURL: 'http://127.0.0.1:11434' },
  { value: 'claude_code', label: 'Claude Code CLI bridge', bridge: true, defaultBaseURL: '' },
  { value: 'codex', label: 'Codex CLI bridge', bridge: true, defaultBaseURL: '' },
]

export const capabilities = ['streaming', 'tools', 'parallel_tools', 'tool_streaming', 'vision', 'reasoning', 'structured_output', 'prompt_cache', 'pdf'] as const

export const billingClasses = [
  { value: 'metered_api', label: 'Metered API (paid per use)' },
  { value: 'subscription', label: 'Subscription' },
  { value: 'free', label: 'Free' },
  { value: 'local', label: 'Local' },
] as const

export const classifications = ['official', 'partial', 'unofficial', 'local'] as const

export const harnesses = [
  { value: '', label: 'Any / not specified' },
  { value: 'claude_code', label: 'Claude Code' },
  { value: 'codex', label: 'Codex' },
  { value: 'opencode', label: 'OpenCode' },
  { value: 'pi', label: 'Pi' },
  { value: 'generic', label: 'Generic API client' },
] as const

export const fallbackSteps = [
  { value: 'retry', label: 'Retry the same candidate' },
  { value: 'alternate_account', label: 'Alternate account, same model' },
  { value: 'alternate_model', label: 'Alternate model in the pool' },
  { value: 'alternate_provider', label: 'Alternate provider' },
  { value: 'wait', label: 'Wait for capacity' },
  { value: 'stop', label: 'Stop' },
] as const

export const credentialScopes = [
  { value: 'gateway:invoke', label: 'Call model endpoints' },
  { value: 'gateway:models', label: 'List models' },
  { value: 'usage:read', label: 'Read own usage' },
  { value: 'tokens:mint', label: 'Mint short-lived child credentials' },
] as const

/** Integer input parsing that never produces NaN. */
export function toInt(v: string): number {
  const n = Number.parseInt(v, 10)
  return Number.isFinite(n) && n >= 0 ? n : 0
}

export function toggle(list: string[], value: string, on: boolean): string[] {
  return on ? (list.includes(value) ? list : [...list, value]) : list.filter((x) => x !== value)
}
