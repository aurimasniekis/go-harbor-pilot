import { useEffect, useRef } from 'react'

/**
 * One Escape handler for every open overlay (dialog, drawer, confirm).
 *
 * Mantine closes each open modal on the same window keydown, outermost
 * first, so Esc in a dialog opened from a drawer closed both. Overlays here
 * disable Mantine's own Escape handling and register in this stack; Esc
 * closes only the most recently opened one.
 */

type Entry = { id: number; onEscape: () => void }

const stack: Entry[] = []
let nextId = 1
let listening = false

function onKeyDown(e: KeyboardEvent): void {
  if (e.key !== 'Escape' || e.isComposing || e.defaultPrevented) return
  // Comboboxes, menus and popovers inside an overlay handle their own Esc.
  const target = e.target as HTMLElement | null
  if (target?.getAttribute?.('data-mantine-stop-propagation') === 'true') return
  if (target?.getAttribute?.('aria-expanded') === 'true' && target.hasAttribute('aria-haspopup')) return
  if (target?.closest?.('[role="menu"], [role="listbox"]')) return
  const top = stack[stack.length - 1]
  if (!top) return
  e.preventDefault()
  e.stopPropagation()
  top.onEscape()
}

function ensureListener(): void {
  if (listening || typeof window === 'undefined') return
  window.addEventListener('keydown', onKeyDown, { capture: true })
  listening = true
}

/** Registers an overlay while `opened`; Esc calls `onEscape` when it is on top. */
export function useOverlayEscape(opened: boolean, onEscape: () => void): void {
  const handler = useRef(onEscape)
  useEffect(() => {
    handler.current = onEscape
  })
  useEffect(() => {
    if (!opened) return
    ensureListener()
    const entry: Entry = { id: nextId++, onEscape: () => handler.current() }
    stack.push(entry)
    return () => {
      const i = stack.findIndex((x) => x.id === entry.id)
      if (i >= 0) stack.splice(i, 1)
    }
  }, [opened])
}

/** Number of registered overlays (tests). */
export function overlayDepth(): number {
  return stack.length
}
