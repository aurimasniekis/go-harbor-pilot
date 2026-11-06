import { Store, useStore } from '@tanstack/react-store'
import { type ThemePref, uiStore } from './store'

export type Scheme = 'dark' | 'light'

/** The colour scheme a preference resolves to. */
export function resolveTheme(pref: ThemePref, osPrefersLight: boolean): Scheme {
  if (pref === 'auto') return osPrefersLight ? 'light' : 'dark'
  return pref
}

const query = typeof window !== 'undefined' && window.matchMedia ? window.matchMedia('(prefers-color-scheme: light)') : undefined

function current(): Scheme {
  return resolveTheme(uiStore.state.theme, query?.matches ?? false)
}

/** The resolved scheme, following the preference and (for auto) the OS. */
export const schemeStore = new Store<Scheme>(current())

function apply() {
  const next = current()
  if (schemeStore.state !== next) schemeStore.setState(() => next)
  if (typeof document !== 'undefined') document.documentElement.dataset.theme = next
}

apply()
uiStore.subscribe(apply)
query?.addEventListener?.('change', apply)

export function useScheme(): Scheme {
  return useStore(schemeStore, (s) => s)
}

/** Menu and palette labels for the preferences, in display order. */
export const themeChoices: { value: ThemePref; label: string }[] = [
  { value: 'auto', label: 'Match system' },
  { value: 'light', label: 'Light' },
  { value: 'dark', label: 'Dark' },
]
