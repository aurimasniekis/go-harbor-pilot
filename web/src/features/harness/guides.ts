/**
 * Harness setup guides. The snippets mirror what `harbor-pilot harness
 * setup` writes (internal/harness). Tokens are never embedded: harnesses
 * reference a 0600 token file or an environment variable.
 */

export type HarnessKind = 'claude' | 'codex' | 'opencode' | 'pi' | 'sdk'

/** Tools the harbor-pilot CLI can configure (every kind except an SDK). */
export type CliKind = Exclude<HarnessKind, 'sdk'>

export interface GuideInput {
  /** Harbor Pilot public URL. */
  url: string
  /** Model or alias to select (optional). */
  model: string
  /** Client binding id to issue a credential for (optional). */
  bindingId: string
}

export interface Guide {
  kind: HarnessKind
  label: string
  /** The file setup edits. */
  file: string
  lang: 'json' | 'toml' | 'shell'
  /** What setup adds to that file. */
  snippet: string
  /** How the harness gets the token. */
  token: string
  /** Shell steps after setup, if any. */
  after: string[]
  verified: boolean
}

export const harnessKinds: { value: HarnessKind; label: string }[] = [
  { value: 'claude', label: 'Claude Code' },
  { value: 'codex', label: 'Codex' },
  { value: 'opencode', label: 'OpenCode' },
  { value: 'pi', label: 'Pi' },
  { value: 'sdk', label: 'Other API client' },
]

export const TOKEN_ENV = 'HARBOR_PILOT_TOKEN'
export const tokenFile = (kind: HarnessKind) => `~/.config/harbor-pilot/tokens/${kind}.token`

/** Trims trailing slashes; falls back to a placeholder. */
export function baseURL(url: string): string {
  const u = url.trim().replace(/\/+$/, '')
  return u || 'https://harbor-pilot.example.com'
}

const json = (v: unknown) => JSON.stringify(v, null, 2)

export function guide(kind: HarnessKind, input: GuideInput): Guide {
  const url = baseURL(input.url)
  const model = input.model.trim()
  const m = model || 'coding/default'
  switch (kind) {
    case 'claude': {
      const env: Record<string, string> = { ANTHROPIC_BASE_URL: url }
      if (model) env.ANTHROPIC_MODEL = model
      return {
        kind, label: 'Claude Code', file: '$CLAUDE_CONFIG_DIR/settings.json (default ~/.claude/settings.json)', lang: 'json',
        snippet: json({ apiKeyHelper: `cat ${tokenFile(kind)}`, env }),
        token: 'apiKeyHelper prints the token file; the token never lives in settings.json.',
        after: [], verified: false,
      }
    }
    case 'codex': {
      const top = [`model_provider = "harbor-pilot"`, ...(model ? [`model = ${JSON.stringify(model)}`] : [])]
      return {
        kind, label: 'Codex', file: '$CODEX_HOME/config.toml (default ~/.codex/config.toml)', lang: 'toml',
        snippet: [
          ...top,
          '',
          '[model_providers.harbor-pilot]',
          'name = "Harbor Pilot"',
          `base_url = ${JSON.stringify(url + '/v1')}`,
          'wire_api = "responses"',
          `env_key = "${TOKEN_ENV}"`,
        ].join('\n'),
        token: `Codex reads the key from the ${TOKEN_ENV} environment variable.`,
        after: [`export ${TOKEN_ENV}="$(cat ${tokenFile(kind)})"`], verified: false,
      }
    }
    case 'opencode':
      return {
        kind, label: 'OpenCode', file: '~/.config/opencode/opencode.json', lang: 'json',
        snippet: json({
          provider: {
            'harbor-pilot': {
              npm: '@ai-sdk/openai-compatible', name: 'Harbor Pilot',
              options: { baseURL: url + '/v1', apiKey: `{file:${tokenFile(kind)}}` },
              models: { [m]: { name: m } },
            },
          },
        }),
        token: 'The provider references the token file with {file:…} substitution.',
        after: [`Select the model as harbor-pilot/${m}`], verified: false,
      }
    case 'pi':
      return {
        kind, label: 'Pi', file: '~/.pi/agent/models.json', lang: 'json',
        snippet: json({
          providers: {
            'harbor-pilot': { baseUrl: url + '/v1', api: 'openai-completions', apiKey: TOKEN_ENV, models: [{ id: m, name: `Harbor Pilot ${m}` }] },
          },
        }),
        token: `Pi reads the key from the ${TOKEN_ENV} environment variable.`,
        after: [`export ${TOKEN_ENV}="$(cat ${tokenFile(kind)})"`], verified: false,
      }
    case 'sdk':
      return {
        kind, label: 'Other API client', file: 'the environment of the app (shell profile, .env or service settings)', lang: 'shell',
        snippet: [
          '# OpenAI SDKs and OpenAI-compatible tools',
          `export OPENAI_BASE_URL=${url}/v1`,
          `export OPENAI_API_KEY="$(cat ${tokenFile(kind)})"`,
          '',
          '# Anthropic SDKs',
          `export ANTHROPIC_BASE_URL=${url}`,
          `export ANTHROPIC_AUTH_TOKEN="$(cat ${tokenFile(kind)})"`,
          '',
          `# Gemini clients: base URL ${url}, token in the x-goog-api-key header`,
        ].join('\n'),
        token: 'The SDK reads the access token from the environment; the token itself stays in the file.',
        after: [], verified: false,
      }
  }
}

/**
 * Saves a pasted access token to the tool's token file, readable only by
 * you. Works in any POSIX shell (macOS, Linux, WSL): it waits for you to
 * paste the token and press Enter, without echoing it.
 */
export function saveTokenCommand(kind: HarnessKind): string {
  return `mkdir -p ~/.config/harbor-pilot/tokens && umask 077 && printf 'Paste the access token, then press Enter: ' && read -rs HP_TOKEN && printf '%s' "$HP_TOKEN" > ${tokenFile(kind)} && unset HP_TOKEN && echo`
}

/** CLI commands for a harness: issue a token straight into setup, preview, status, undo. */
export function commands(kind: HarnessKind, input: GuideInput): { label: string; cmd: string }[] {
  if (kind === 'sdk') return []
  const url = baseURL(input.url)
  const binding = input.bindingId.trim() || '<binding-id>'
  const model = input.model.trim() ? ` --model ${shellArg(input.model.trim())}` : ''
  const setup = `harbor-pilot harness setup ${kind} --url ${shellArg(url)} --token-stdin${model} --yes`
  return [
    { label: 'Preview the change (writes nothing)', cmd: `harbor-pilot harness preview ${kind} --url ${shellArg(url)} --token-file ${tokenFile(kind)}${model}` },
    {
      label: 'Issue a credential and apply setup (the token goes through the pipe, never the screen)',
      cmd: `harbor-pilot token issue --server ${shellArg(url)} --api-token-file ~/.config/harbor-pilot/api.token --binding ${binding} --name ${kind} \\\n  | ${setup}`,
    },
    { label: 'Check every harness', cmd: 'harbor-pilot harness status' },
    { label: 'Undo (removes only the keys setup added)', cmd: `harbor-pilot harness uninstall ${kind} --yes` },
    { label: 'Undo by restoring the original file', cmd: `harbor-pilot harness uninstall ${kind} --restore-backup --yes` },
  ]
}

/** Quotes a shell argument when it has special characters. */
export function shellArg(s: string): string {
  return /^[\w@%+=:,./~-]+$/.test(s) ? s : `'${s.replace(/'/g, `'\\''`)}'`
}
