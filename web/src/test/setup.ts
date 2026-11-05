import '@testing-library/jest-dom/vitest'

// Mantine reads media queries and observes element sizes; jsdom has neither.
if (!window.matchMedia) {
  window.matchMedia = (query: string) =>
    ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }) as MediaQueryList
}
if (!('ResizeObserver' in window)) {
  ;(window as unknown as { ResizeObserver: unknown }).ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
}

// Autosizing text areas listen for font loading; jsdom has no FontFaceSet.
if (!('fonts' in document)) {
  ;(document as unknown as { fonts: unknown }).fonts = { addEventListener() {}, removeEventListener() {}, ready: Promise.resolve() }
}
