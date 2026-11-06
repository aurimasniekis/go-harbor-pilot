import {
  BarChart3,
  FlaskConical,
  Gauge,
  History,
  KeyRound,
  LogOut,
  type LucideIcon,
  Monitor,
  Moon,
  Plug,
  Plus,
  Server,
  Sun,
  UserRound,
  Users,
  Wallet,
  HardDrive,
} from 'lucide-react'
import type { ThemePref } from '@/lib/store'
import { navItems } from './nav'

/** What a palette entry does when chosen. */
export type PaletteRun = { kind: 'go'; to: string; hash?: string } | { kind: 'signout' } | { kind: 'theme'; value: ThemePref }

export interface PaletteEntry {
  id: string
  label: string
  description?: string
  keywords?: string[]
  icon: LucideIcon
  group: 'Pages' | 'Actions'
  run: PaletteRun
}

interface ActionDef extends Omit<PaletteEntry, 'group'> {
  perm?: string
}

const actions: ActionDef[] = [
  { id: 'connect', label: 'Connect a tool', description: 'Claude Code, Codex, OpenCode or Pi', icon: Plug, run: { kind: 'go', to: '/clients/setup' }, perm: 'config.read', keywords: ['setup', 'harness'] },
  { id: 'new-client', label: 'Add a client', description: 'A tool or service that sends requests', icon: Plus, run: { kind: 'go', to: '/clients' }, perm: 'config.write', keywords: ['token'] },
  { id: 'new-provider', label: 'Add a provider', description: 'API key, local model or CLI subscription', icon: Server, run: { kind: 'go', to: '/providers' }, perm: 'config.write', keywords: ['api key', 'openai', 'anthropic'] },
  { id: 'simulate', label: 'Test a request', description: 'See where a routing plan sends it', icon: FlaskConical, run: { kind: 'go', to: '/routing/plans' }, perm: 'config.read', keywords: ['simulate', 'simulator'] },
  { id: 'add-budget', label: 'Add a budget', description: 'Cap spend for a workspace, project or client', icon: Plus, run: { kind: 'go', to: '/quotas?add=budget' }, perm: 'config.write', keywords: ['budgets', 'spend limit'] },
  { id: 'budgets', label: 'Budgets', description: 'Spend limits and how close you are', icon: Wallet, run: { kind: 'go', to: '/quotas', hash: 'budgets' }, perm: 'usage.read' },
  { id: 'quota-tracking', label: 'Quota tracking', description: 'Trackers that read provider quotas', icon: Gauge, run: { kind: 'go', to: '/quotas/monitors' }, perm: 'usage.read', keywords: ['tracker', 'monitors', 'quota'] },
  { id: 'add-tracker', label: 'Add a quota tracker', description: 'Watch a provider account’s quota', icon: Plus, run: { kind: 'go', to: '/quotas/monitors' }, perm: 'config.write', keywords: ['tracker', 'monitor'] },
  { id: 'breakdown', label: 'Spend breakdown', description: 'Usage and cost by client, model or account', icon: BarChart3, run: { kind: 'go', to: '/usage/breakdown' }, perm: 'usage.read', keywords: ['statistics', 'stats'] },
  { id: 'people', label: 'People', description: 'Invite and manage users', icon: Users, run: { kind: 'go', to: '/settings/users' }, perm: 'users.manage', keywords: ['users', 'invite'] },
  { id: 'audit', label: 'Audit log', description: 'Who changed what', icon: History, run: { kind: 'go', to: '/settings/audit' }, perm: 'audit.read' },
  { id: 'backups', label: 'System & backups', description: 'Version, storage and backups', icon: HardDrive, run: { kind: 'go', to: '/settings/system' }, perm: 'system.manage', keywords: ['backup'] },
  { id: 'account', label: 'My account', description: 'Your name, sessions and password', icon: UserRound, run: { kind: 'go', to: '/account' }, keywords: ['profile', 'account', 'name', 'sessions'] },
  { id: 'password', label: 'Change password', icon: KeyRound, run: { kind: 'go', to: '/account' }, keywords: ['password'] },
]

const themeIcon: Record<ThemePref, LucideIcon> = { auto: Monitor, light: Sun, dark: Moon }
const themeLabel: Record<ThemePref, string> = { auto: 'Match system theme', light: 'Use light theme', dark: 'Use dark theme' }

/**
 * Every palette entry for a principal: pages first, then actions. Pages
 * carry no description (the label says it); actions say what they do.
 */
export function paletteEntries(can: (perm?: string) => boolean, theme: ThemePref, signedIn: boolean): PaletteEntry[] {
  const pages: PaletteEntry[] = navItems
    .filter((i) => can(i.perm))
    .map((i) => ({ id: `go-${i.to}`, label: i.label, keywords: i.keywords, icon: i.icon, group: 'Pages', run: { kind: 'go', to: i.to } }))
  const acts: PaletteEntry[] = actions.filter((a) => can(a.perm)).map(({ perm: _perm, ...a }) => ({ ...a, group: 'Actions' }))
  const themes: PaletteEntry[] = (['auto', 'light', 'dark'] as const)
    .filter((t) => t !== theme)
    .map((t) => ({ id: `theme-${t}`, label: themeLabel[t], icon: themeIcon[t], group: 'Actions', keywords: ['theme', 'appearance'], run: { kind: 'theme', value: t } }))
  const out = [...pages, ...acts, ...themes]
  if (signedIn) out.push({ id: 'signout', label: 'Sign out', icon: LogOut, group: 'Actions', keywords: ['log out', 'logout'], run: { kind: 'signout' } })
  return out
}

/** True when every word of the query appears in one of the texts. */
export function matchesQuery(texts: (string | undefined)[], query: string): boolean {
  const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean)
  if (words.length === 0) return true
  const hay = texts.filter(Boolean).join(' ').toLowerCase()
  return words.every((w) => hay.includes(w))
}

/** Entries matching a query the way the palette filters them. */
export function searchPalette(entries: PaletteEntry[], query: string): PaletteEntry[] {
  return entries.filter((e) => matchesQuery([e.label, e.description, ...(e.keywords ?? [])], query))
}

/** "⌘ K" on Apple platforms, "Ctrl K" elsewhere. */
export function shortcutLabel(os: string): string {
  return os === 'macos' || os === 'ios' ? '⌘ K' : 'Ctrl K'
}

/** A short build label: semantic versions as is, commit hashes shortened. */
export function friendlyVersion(v: string | undefined): string {
  if (!v) return ''
  const m = /^([0-9a-f]{7,40})(-dirty)?$/.exec(v)
  if (m) return `build ${m[1]!.slice(0, 7)}${m[2] ? ' (modified)' : ''}`
  return v.replace(/^v?/, 'v')
}
