import {
  type CSSVariablesResolver,
  type MantineColorsTuple,
  type VariantColorsResolver,
  createTheme,
  defaultVariantColorsResolver,
} from '@mantine/core'

/*
 * Mantine theme over the dark and light palettes in styles/tokens.css. The
 * token CSS variables stay the single source of truth: Mantine's palette
 * variables are pointed at them per color scheme, so components and the
 * remaining CSS-module screens share exactly the same colors.
 */

function hex(c: string): [number, number, number] {
  const v = c.replace('#', '')
  return [parseInt(v.slice(0, 2), 16), parseInt(v.slice(2, 4), 16), parseInt(v.slice(4, 6), 16)]
}

function mix(a: string, b: string, t: number): string {
  const [r1, g1, b1] = hex(a)
  const [r2, g2, b2] = hex(b)
  const m = (x: number, y: number) => Math.round(x + (y - x) * t).toString(16).padStart(2, '0')
  return `#${m(r1, r2)}${m(g1, g2)}${m(b1, b2)}`
}

/** A 10-shade tuple around a base color (index 5 is the base). */
function tuple(base: string): MantineColorsTuple {
  return [
    mix(base, '#ffffff', 0.85),
    mix(base, '#ffffff', 0.7),
    mix(base, '#ffffff', 0.5),
    mix(base, '#ffffff', 0.3),
    mix(base, '#ffffff', 0.15),
    base,
    mix(base, '#000000', 0.12),
    mix(base, '#000000', 0.25),
    mix(base, '#000000', 0.38),
    mix(base, '#000000', 0.5),
  ]
}

/** Mantine palette name → token name (see tokens.css). */
const tones = {
  blue: 'accent',
  green: 'success',
  teal: 'info',
  cyan: 'info',
  yellow: 'warning',
  red: 'error',
  violet: 'purple',
  grape: 'purple',
  orange: 'orange',
} as const

export type Tone = keyof typeof tones | 'gray'

// Night Owl surfaces, lightest text first (Mantine's dark scheme reads
// dark-0 as text, dark-2 dimmed, dark-4 borders, dark-6 inputs, dark-7 body).
const dark: MantineColorsTuple = ['#d6deeb', '#b2bccc', '#8b9fae', '#5f7e97', '#1d3b53', '#12304a', '#0b2942', '#011627', '#010f1c', '#000b14']

/** Light, filled and outline variants of the token tones use the token variables. */
export const variantColorResolver: VariantColorsResolver = (input) => {
  const name = typeof input.color === 'string' ? input.color.split('.')[0] : ''
  const token = (tones as Record<string, string>)[name ?? '']
  if (token) {
    const text = token === 'error' ? 'var(--error-text)' : token === 'accent' ? 'var(--accent-text)' : `var(--${token})`
    switch (input.variant) {
      case 'light':
        return { background: `var(--${token}-soft, color-mix(in srgb, var(--${token}) 14%, transparent))`, hover: `color-mix(in srgb, var(--${token}) 22%, transparent)`, color: text, border: 'transparent' }
      case 'filled':
        return { background: `var(--${token})`, hover: `color-mix(in srgb, var(--${token}) 85%, var(--text))`, color: 'var(--text-inverse)', border: 'transparent' }
      case 'outline':
        return { background: 'transparent', hover: `color-mix(in srgb, var(--${token}) 10%, transparent)`, color: text, border: `1px solid var(--${token})` }
      case 'subtle':
        return { background: 'transparent', hover: `color-mix(in srgb, var(--${token}) 12%, transparent)`, color: text, border: 'transparent' }
      case 'transparent':
        return { background: 'transparent', hover: 'transparent', color: text, border: 'transparent' }
    }
  }
  if (name === 'gray' && input.variant === 'light') {
    return { background: 'var(--surface-raised)', hover: 'var(--border)', color: 'var(--text-secondary)', border: 'transparent' }
  }
  return defaultVariantColorsResolver(input)
}

export const theme = createTheme({
  variantColorResolver,
  primaryColor: 'blue',
  primaryShade: { light: 6, dark: 5 },
  colors: {
    dark,
    blue: tuple('#82aaff'),
    green: tuple('#addb67'),
    teal: tuple('#7fdbca'),
    cyan: tuple('#7fdbca'),
    yellow: tuple('#ecc48d'),
    red: tuple('#f2726f'),
    violet: tuple('#c792ea'),
    grape: tuple('#c792ea'),
    orange: tuple('#f78c6c'),
  },
  fontFamily: 'var(--font-sans)',
  fontFamilyMonospace: 'var(--font-mono)',
  fontSizes: { xs: '0.75rem', sm: '0.8125rem', md: '0.875rem', lg: '1rem', xl: '1.125rem' },
  lineHeights: { xs: '1.4', sm: '1.45', md: '1.5', lg: '1.5', xl: '1.5' },
  headings: {
    fontWeight: '600',
    sizes: {
      h1: { fontSize: '1.5rem', lineHeight: '1.3' },
      h2: { fontSize: '1rem', lineHeight: '1.4' },
      h3: { fontSize: '0.9375rem', lineHeight: '1.4' },
      h4: { fontSize: '0.875rem', lineHeight: '1.4' },
    },
  },
  radius: { xs: '2px', sm: '4px', md: '6px', lg: '8px', xl: '12px' },
  defaultRadius: 'md',
  spacing: { xs: '6px', sm: '10px', md: '16px', lg: '20px', xl: '28px' },
  cursorType: 'pointer',
  focusRing: 'auto',
  components: {
    Paper: { defaultProps: { bg: 'var(--surface)' } },
    Card: { defaultProps: { bg: 'var(--surface)' } },
    Drawer: { styles: { content: { background: 'var(--surface)' }, header: { background: 'var(--surface)' } } },
    Modal: { styles: { content: { background: 'var(--surface)' }, header: { background: 'var(--surface)' } } },
    Tooltip: {
      defaultProps: { multiline: true, maw: 320, withArrow: true, openDelay: 150 },
      styles: { tooltip: { background: 'var(--surface-raised)', color: 'var(--text)', border: '1px solid var(--border-strong)', fontSize: 'var(--text-sm)' } },
    },
    Popover: { styles: { dropdown: { background: 'var(--surface-raised)', borderColor: 'var(--border-strong)' } } },
    Menu: { styles: { dropdown: { background: 'var(--surface-raised)', borderColor: 'var(--border-strong)' } } },
    Combobox: { styles: { dropdown: { background: 'var(--surface-raised)', borderColor: 'var(--border-strong)' } } },
    Select: { styles: { dropdown: { background: 'var(--surface-raised)', borderColor: 'var(--border-strong)' } } },
    Table: {
      defaultProps: { verticalSpacing: 7, horizontalSpacing: 'sm', highlightOnHover: true },
      styles: { th: { color: 'var(--text-muted)', fontWeight: 600, fontSize: 'var(--text-xs)', whiteSpace: 'nowrap' } },
    },
    Badge: { defaultProps: { variant: 'light', radius: 'sm', tt: 'none', fw: 600 } },
    // One label style for every field, Mantine or custom.
    InputWrapper: {
      styles: {
        label: { fontWeight: 600, fontSize: 'var(--mantine-font-size-sm)', color: 'var(--text)', marginBottom: 4 },
        description: { color: 'var(--text-muted)' },
      },
    },
    Fieldset: {
      styles: {
        root: { background: 'var(--surface)', borderColor: 'var(--border)' },
        legend: { fontWeight: 600, fontSize: 'var(--mantine-font-size-sm)', color: 'var(--text)' },
      },
    },
    Accordion: {
      styles: {
        item: { backgroundColor: 'var(--surface)', borderColor: 'var(--border)' },
        label: { fontWeight: 600 },
      },
    },
    Anchor: { defaultProps: { underline: 'hover' } },
  },
})

function toneVars(): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [name, token] of Object.entries(tones)) {
    const text = token === 'error' ? 'var(--error-text)' : token === 'accent' ? 'var(--accent-text)' : `var(--${token})`
    out[`--mantine-color-${name}-filled`] = `var(--${token})`
    out[`--mantine-color-${name}-filled-hover`] = `color-mix(in srgb, var(--${token}) 85%, var(--text))`
    out[`--mantine-color-${name}-light`] = `var(--${token}-soft, color-mix(in srgb, var(--${token}) 14%, transparent))`
    out[`--mantine-color-${name}-light-hover`] = `color-mix(in srgb, var(--${token}) 22%, transparent)`
    out[`--mantine-color-${name}-light-color`] = text
    out[`--mantine-color-${name}-outline`] = `var(--${token})`
    out[`--mantine-color-${name}-outline-hover`] = `color-mix(in srgb, var(--${token}) 10%, transparent)`
    out[`--mantine-color-${name}-text`] = text
  }
  return out
}

const shared = (): Record<string, string> => ({
  ...toneVars(),
  '--mantine-color-body': 'var(--bg)',
  '--mantine-color-text': 'var(--text)',
  '--mantine-color-bright': 'var(--text)',
  '--mantine-color-dimmed': 'var(--text-muted)',
  '--mantine-color-placeholder': 'var(--text-muted)',
  '--mantine-color-anchor': 'var(--link)',
  '--mantine-color-error': 'var(--error-text)',
  '--mantine-color-default': 'var(--surface-raised)',
  '--mantine-color-default-hover': 'color-mix(in srgb, var(--surface-raised) 70%, var(--border-strong))',
  '--mantine-color-default-color': 'var(--text)',
  '--mantine-color-default-border': 'var(--border)',
  '--mantine-primary-color-filled': 'var(--accent)',
  '--mantine-primary-color-filled-hover': 'color-mix(in srgb, var(--accent) 85%, var(--text))',
  '--mantine-primary-color-light': 'var(--accent-soft)',
  '--mantine-primary-color-light-hover': 'color-mix(in srgb, var(--accent) 22%, transparent)',
  '--mantine-primary-color-light-color': 'var(--accent-text)',
  '--mantine-primary-color-contrast': 'var(--text-inverse)',
  '--mantine-color-gray-light': 'var(--surface-raised)',
  '--mantine-color-gray-light-hover': 'var(--border)',
  '--mantine-color-gray-light-color': 'var(--text-secondary)',
  '--mantine-color-gray-text': 'var(--text-muted)',
  '--mantine-color-gray-filled': 'var(--border-strong)',
  '--mantine-color-gray-outline': 'var(--border-strong)',
})

export const cssVariablesResolver: CSSVariablesResolver = () => ({
  variables: {},
  light: shared(),
  dark: shared(),
})
