import { Store } from '@tanstack/react-store'

/**
 * Client-only UI preferences. Server data lives exclusively in TanStack
 * Query; nothing here duplicates it. Preferences persist to localStorage
 * (they are not secrets).
 */
export interface UiPrefs {
  sidebarCollapsed: boolean
  maskIdentifiers: boolean
  theme: 'dark' | 'light'
  /** Accounts pinned for comparison in charts. */
  compare: string[]
}

const KEY = 'hp.ui.v1'

function load(): UiPrefs {
  const defaults: UiPrefs = { sidebarCollapsed: false, maskIdentifiers: true, theme: 'dark', compare: [] }
  try {
    const raw = localStorage.getItem(KEY)
    if (!raw) return defaults
    const parsed = JSON.parse(raw) as Partial<UiPrefs>
    return {
      sidebarCollapsed: typeof parsed.sidebarCollapsed === 'boolean' ? parsed.sidebarCollapsed : defaults.sidebarCollapsed,
      maskIdentifiers: typeof parsed.maskIdentifiers === 'boolean' ? parsed.maskIdentifiers : defaults.maskIdentifiers,
      theme: parsed.theme === 'light' ? 'light' : 'dark',
      compare: Array.isArray(parsed.compare) ? parsed.compare.filter((x): x is string => typeof x === 'string').slice(0, 8) : [],
    }
  } catch {
    return defaults
  }
}

export const uiStore = new Store<UiPrefs>(load())

uiStore.subscribe(() => {
  try {
    localStorage.setItem(KEY, JSON.stringify(uiStore.state))
  } catch {
    /* storage unavailable */
  }
  document.documentElement.dataset.theme = uiStore.state.theme
})

export function updatePrefs(patch: Partial<UiPrefs>): void {
  uiStore.setState((s) => ({ ...s, ...patch }))
}

export function toggleCompare(id: string): void {
  uiStore.setState((s) => ({
    ...s,
    compare: s.compare.includes(id) ? s.compare.filter((x) => x !== id) : [...s.compare, id].slice(-8),
  }))
}
