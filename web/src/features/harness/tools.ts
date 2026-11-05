import type { HarnessKind } from './guides'

/** The tool a client is set up for (the binding's expected harness). */
export type ToolValue = 'claude_code' | 'codex' | 'opencode' | 'pi' | 'generic'

/** Every client tool, with one label used on every screen. */
export const tools: { value: ToolValue; label: string; hint: string }[] = [
  { value: 'claude_code', label: 'Claude Code', hint: 'Anthropic’s coding agent' },
  { value: 'codex', label: 'Codex', hint: 'OpenAI’s coding agent' },
  { value: 'opencode', label: 'OpenCode', hint: 'Open-source coding agent' },
  { value: 'pi', label: 'Pi', hint: 'Pi coding agent' },
  { value: 'generic', label: 'Other API client', hint: 'Any OpenAI, Anthropic or Gemini SDK' },
]

export function toolLabel(v: string | undefined): string {
  return tools.find((t) => t.value === v)?.label ?? 'Any tool'
}

/** The setup guide for a client's tool. */
export function kindForTool(v: string | undefined): HarnessKind {
  switch (v) {
    case 'codex':
      return 'codex'
    case 'opencode':
      return 'opencode'
    case 'pi':
      return 'pi'
    case 'generic':
      return 'sdk'
    default:
      return 'claude'
  }
}

/** What an access token may do, in plain words. */
export const tokenScopes: { value: string; label: string; hint: string }[] = [
  { value: 'gateway:invoke', label: 'Send requests', hint: 'Call the model endpoints' },
  { value: 'gateway:models', label: 'List models', hint: 'See which model names it may ask for' },
  { value: 'usage:read', label: 'Read its own usage', hint: 'Requests and costs of this client only' },
  { value: 'tokens:mint', label: 'Create run tokens', hint: 'Short-lived tokens for orchestrated runs' },
]

export const DEFAULT_SCOPES = ['gateway:invoke', 'gateway:models']

export function scopeLabel(v: string): string {
  return tokenScopes.find((s) => s.value === v)?.label ?? v
}
