import { Globe } from 'lucide-react'
import {
  siAnthropic,
  siClaude,
  siCursor,
  siDeepseek,
  siGithubcopilot,
  siGooglegemini,
  siKimi,
  siMinimax,
  siOpenrouter,
} from 'simple-icons'
import type { SimpleIcon } from 'simple-icons'
import s from './ProviderIcon.module.css'

// Brand marks come from simple-icons (CC0). Providers without a mark there
// (OpenAI/Codex, xAI, Together, Z.ai, nexos.ai) get a neutral monogram tile
// instead of a hand-drawn logo.
const byProvider: Record<string, SimpleIcon> = {
  anthropic_admin: siAnthropic,
  // Gateway provider types.
  anthropic: siAnthropic,
  gemini: siGooglegemini,
  claude_code: siClaude,
}

const byFamily: Record<string, SimpleIcon> = {
  claude: siClaude,
  google: siGooglegemini,
  copilot: siGithubcopilot,
  cursor: siCursor,
  deepseek: siDeepseek,
  moonshot: siKimi,
  minimax: siMinimax,
  openrouter: siOpenrouter,
}

const providerMonograms: Record<string, string> = {
  codex_subscription: 'Cx',
  codex: 'Cx', // demo_codex
}

const monograms: Record<string, string> = {
  openai: 'AI',
  xai: 'x',
  together: 'T',
  zai: 'Z',
  nexos: 'n',
}

function luminance(hex: string): number {
  const ch = [0, 2, 4].map((i) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  })
  return 0.2126 * (ch[0] ?? 0) + 0.7152 * (ch[1] ?? 0) + 0.0722 * (ch[2] ?? 0)
}

/** Very dark brand colors (black marks) follow the text color so they stay visible in dark mode. */
function brandColor(hex: string): string {
  return luminance(hex) < 0.08 ? 'currentColor' : `#${hex}`
}

function monogramFor(base: string, family: string, name: string): string {
  const m = providerMonograms[base] ?? monograms[family]
  if (m) return m
  const first = name.replace(/\(.*\)/, '').trim().charAt(0)
  return first ? first.toUpperCase() : '?'
}

/**
 * Decorative provider mark. Always rendered next to the provider's name, so
 * it is hidden from assistive technology.
 */
export function ProviderIcon({ id, family, name, size = 16 }: { id: string; family: string; name: string; size?: number }) {
  const base = id.replace(/^demo_/, '')
  const icon = byProvider[base] ?? byFamily[family]
  if (icon) {
    return (
      <svg className={s.icon} width={size} height={size} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
        <path d={icon.path} fill={brandColor(icon.hex)} />
      </svg>
    )
  }
  if (family === 'generic') return <Globe className={s.icon} size={size} aria-hidden="true" />
  const text = monogramFor(base, family, name)
  return (
    <span className={s.monogram} style={{ width: size, height: size, fontSize: Math.round(size * (text.length > 1 ? 0.5 : 0.62)) }} aria-hidden="true">
      {text}
    </span>
  )
}
