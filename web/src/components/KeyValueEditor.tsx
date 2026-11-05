import { useState } from 'react'
import { Field, formStyles as f } from './form'

/** Parses "key=value" lines; blank lines and lines without '=' are reported. */
export function parseKeyValues(text: string): { value: Record<string, string>; error?: string } {
  const value: Record<string, string> = {}
  const lines = text.split('\n')
  for (let i = 0; i < lines.length; i++) {
    const line = (lines[i] ?? '').trim()
    if (!line) continue
    const eq = line.indexOf('=')
    if (eq <= 0) return { value, error: `Line ${i + 1}: expected key=value` }
    value[line.slice(0, eq).trim()] = line.slice(eq + 1).trim()
  }
  return { value }
}

export function formatKeyValues(m: Record<string, string> | undefined | null): string {
  return Object.entries(m ?? {})
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([k, v]) => `${k}=${v}`)
    .join('\n')
}

/** Textarea of key=value lines bound to a string map. */
export function KeyValueEditor({ label, hint, value, onChange, placeholder }: {
  label: string
  hint?: string
  value: Record<string, string>
  onChange: (v: Record<string, string>, valid: boolean) => void
  placeholder?: string
}) {
  const [text, setText] = useState(() => formatKeyValues(value))
  const parsed = parseKeyValues(text)
  return (
    <Field label={label} hint={hint ?? 'One key=value per line.'} error={parsed.error}>
      {(p) => (
        <textarea
          {...p}
          className={f.textarea}
          value={text}
          placeholder={placeholder}
          onChange={(e) => {
            setText(e.target.value)
            const r = parseKeyValues(e.target.value)
            onChange(r.value, !r.error)
          }}
        />
      )}
    </Field>
  )
}

/** Textarea of one item per line bound to a string list. */
export function LinesEditor({ label, hint, value, onChange, placeholder }: {
  label: string
  hint?: string
  value: string[]
  onChange: (v: string[]) => void
  placeholder?: string
}) {
  const [text, setText] = useState(() => value.join('\n'))
  return (
    <Field label={label} hint={hint ?? 'One per line.'}>
      {(p) => (
        <textarea
          {...p}
          className={f.textarea}
          value={text}
          placeholder={placeholder}
          onChange={(e) => {
            setText(e.target.value)
            onChange(e.target.value.split('\n').map((l) => l.trim()).filter(Boolean))
          }}
        />
      )}
    </Field>
  )
}
