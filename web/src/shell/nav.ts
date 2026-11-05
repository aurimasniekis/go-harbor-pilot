import { Bell, Coins, Gauge, House, Laptop, List, type LucideIcon, Route as RouteIcon, Server, Settings, ShieldCheck } from 'lucide-react'

export interface NavItem {
  to: string
  label: string
  icon: LucideIcon
  /** Path prefixes that belong to this item (defaults to `to`). */
  match?: string[]
  /** Permission required to show the item (any signed-in user when absent). */
  perm?: string
  /** Extra words the command palette matches. */
  keywords?: string[]
}

export interface NavSection {
  label?: string
  items: NavItem[]
}

/**
 * Primary navigation: what the operator watches (Monitor) and what they set
 * up (Configure). Admin and diagnostics live under Settings in the footer.
 */
export const navSections: NavSection[] = [
  { items: [{ to: '/', label: 'Home', icon: House, keywords: ['overview', 'dashboard', 'health'] }] },
  {
    label: 'Monitor',
    items: [
      { to: '/activity', label: 'Requests', icon: List, perm: 'activity.read', keywords: ['activity', 'sessions', 'statistics', 'why this route'] },
      { to: '/quotas', label: 'Limits', icon: Gauge, match: ['/quotas', '/usage/budgets'], perm: 'usage.read', keywords: ['quotas', 'budgets', 'rate limits'] },
      { to: '/usage', label: 'Spend', icon: Coins, perm: 'usage.read', keywords: ['usage', 'cost', 'tokens'] },
    ],
  },
  {
    label: 'Configure',
    items: [
      { to: '/clients', label: 'Clients', icon: Laptop, perm: 'config.read', keywords: ['tools', 'tokens', 'setup', 'claude code', 'codex'] },
      { to: '/providers', label: 'Providers', icon: Server, perm: 'config.read', keywords: ['accounts', 'api keys', 'subscriptions'] },
      {
        to: '/routing/plans',
        label: 'Routing',
        icon: RouteIcon,
        match: ['/routing', '/providers/pools'],
        perm: 'config.read',
        keywords: ['plans', 'model names', 'fallback groups', 'pools'],
      },
      { to: '/privacy', label: 'Privacy', icon: ShieldCheck, match: ['/privacy', '/traces'], perm: 'config.read', keywords: ['masking', 'captured content', 'traces'] },
      {
        to: '/notifications',
        label: 'Alerts',
        icon: Bell,
        match: ['/notifications', '/events/destinations'],
        perm: 'config.read',
        keywords: ['notifications', 'telegram', 'slack', 'discord', 'webhooks', 'pulse'],
      },
    ],
  },
]

/** Settings sits in the sidebar footer; it also owns the diagnostics pages. */
export const settingsItem: NavItem = {
  to: '/settings',
  label: 'Settings',
  icon: Settings,
  match: ['/settings', '/workspaces', '/compatibility', '/events'],
  perm: 'config.read',
  keywords: ['people', 'users', 'sso', 'audit', 'backups', 'api tokens', 'workspaces', 'diagnostics', 'compatibility', 'event stream'],
}

/** Tabs of the phone bottom bar. */
export const bottomTabs = ['/', '/activity', '/quotas', '/usage']

export const navItems: NavItem[] = [...navSections.flatMap((s) => s.items), settingsItem]

/** The nav item for a path: the longest matching prefix wins. */
export function activeNavItem(path: string): NavItem | undefined {
  const p = path.replace(/\/$/, '') || '/'
  let best: NavItem | undefined
  let bestLen = -1
  for (const it of navItems) {
    for (const m of it.match ?? [it.to]) {
      const hit = m === '/' ? p === '/' : p === m || p.startsWith(`${m}/`)
      if (hit && m.length > bestLen) {
        best = it
        bestLen = m.length
      }
    }
  }
  return best
}
