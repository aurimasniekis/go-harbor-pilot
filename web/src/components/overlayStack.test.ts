import { describe, expect, it, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { overlayDepth, useOverlayEscape } from './overlayStack'

const esc = (target: EventTarget = window) => target.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))

describe('useOverlayEscape', () => {
  it('closes only the most recent overlay, then the one below it', () => {
    const outer = vi.fn()
    const inner = vi.fn()
    const a = renderHook(({ open }) => useOverlayEscape(open, outer), { initialProps: { open: true } })
    const b = renderHook(({ open }) => useOverlayEscape(open, inner), { initialProps: { open: true } })
    expect(overlayDepth()).toBe(2)
    esc()
    expect(inner).toHaveBeenCalledOnce()
    expect(outer).not.toHaveBeenCalled()
    b.rerender({ open: false })
    esc()
    expect(outer).toHaveBeenCalledOnce()
    a.unmount()
    b.unmount()
    expect(overlayDepth()).toBe(0)
  })

  it('leaves Esc to an open dropdown inside the overlay', () => {
    const close = vi.fn()
    const h = renderHook(() => useOverlayEscape(true, close))
    const input = document.createElement('input')
    input.setAttribute('aria-haspopup', 'listbox')
    input.setAttribute('aria-expanded', 'true')
    document.body.appendChild(input)
    esc(input)
    expect(close).not.toHaveBeenCalled()
    input.remove()
    h.unmount()
  })
})
