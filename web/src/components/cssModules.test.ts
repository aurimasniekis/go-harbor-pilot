/// <reference types="node" />
import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

// Every `alias.className` used with a CSS module must exist in that module.
// An unknown class silently renders as no class at all (unstyled controls,
// collapsed grids), so this guards removals and typos.

const sources = import.meta.glob(['/src/**/*.{ts,tsx}', '!/src/**/*.test.{ts,tsx}'], {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>
const classes = new Map<string, Set<string>>()
function classesOf(path: string): Set<string> | undefined {
  const file = join(process.cwd(), path)
  if (!existsSync(file)) return undefined
  let s = classes.get(path)
  if (!s) {
    const css = readFileSync(file, 'utf8').replace(/\/\*[\s\S]*?\*\//g, '')
    s = new Set([...css.matchAll(/\.([A-Za-z_][\w-]*)/g)].map((m) => m[1] ?? ''))
    classes.set(path, s)
  }
  return s
}

function resolve(from: string, spec: string): string {
  if (spec.startsWith('@/')) return '/src/' + spec.slice(2)
  const parts = from.split('/').slice(0, -1)
  for (const seg of spec.split('/')) {
    if (seg === '..') parts.pop()
    else if (seg !== '.') parts.push(seg)
  }
  return parts.join('/')
}

// Modules re-exported under another name.
const reexports: Record<string, string> = {
  formStyles: '/src/components/form.module.css',
  uiStyles: '/src/components/ui.module.css',
}

function missingRefs(): string[] {
  const out: string[] = []
  for (const [file, text] of Object.entries(sources)) {
    const aliases = new Map<string, string>()
    for (const m of text.matchAll(/import\s+(\w+)\s+from\s+'([^']+\.module\.css)'/g)) aliases.set(m[1] ?? '', resolve(file, m[2] ?? ''))
    for (const m of text.matchAll(/import\s*\{([^}]*)\}\s*from\s*'[^']+'/g)) {
      for (const part of (m[1] ?? '').split(',')) {
        const [name = '', as] = part.trim().split(/\s+as\s+/)
        const target = reexports[name]
        if (target) aliases.set(as ?? name, target)
      }
    }
    for (const [alias, cssPath] of aliases) {
      const known = classesOf(cssPath)
      if (!known) {
        out.push(`${file}: ${alias} imports missing module ${cssPath}`)
        continue
      }
      for (const m of text.matchAll(new RegExp(`\\b${alias}\\.([A-Za-z_]\\w*)\\b(?![.(]|\\s*\\()`, 'g'))) {
        if (!known.has(m[1] ?? '')) {
          const line = text.slice(0, m.index).split('\n').length
          out.push(`${file}:${line} ${alias}.${m[1]} is not defined in ${cssPath}`)
        }
      }
    }
  }
  return out
}

describe('CSS module references', () => {
  it('finds the sources it checks', () => {
    expect(Object.keys(sources).length).toBeGreaterThan(50)
    expect(classesOf('/src/components/form.module.css')?.has('grid')).toBe(true)
  })
  it('only uses classes that exist', () => {
    expect(missingRefs()).toEqual([])
  })
})
