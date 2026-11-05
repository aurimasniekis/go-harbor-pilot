import { type ReactNode, useEffect, useState } from 'react'
import { ListField, TextAreaField } from './form'

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

/**
 * Local text for an editor bound to an outside value. The draft follows the
 * value whenever the value changes from outside (Discard, a reload, another
 * save), so the screen never shows text that is not what will be saved.
 */
export function useBoundDraft<V>(value: V, format: (v: V) => string, parse: (t: string) => V): [string, (t: string) => void] {
  const formatted = format(value)
  const [text, setText] = useState(formatted)
  useEffect(() => {
    // Keep what the user typed when it means the same value (spacing,
    // order); otherwise show the outside value.
    setText((t) => (format(parse(t)) === formatted ? t : formatted))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [formatted])
  return [text, setText]
}

/** Text area of key=value lines bound to a string map. */
export function KeyValueEditor({ label, hint, value, onChange, placeholder, error }: {
  label: ReactNode
  hint?: ReactNode
  value: Record<string, string>
  onChange: (v: Record<string, string>, valid: boolean) => void
  placeholder?: string
  error?: string
}) {
  const [text, setText] = useBoundDraft(value, formatKeyValues, (t) => parseKeyValues(t).value)
  const parsed = parseKeyValues(text)
  return (
    <TextAreaField
      label={label}
      hint={hint ?? 'One key=value per line.'}
      error={error ?? parsed.error}
      value={text}
      placeholder={placeholder}
      mono
      minRows={2}
      onChange={(t) => {
        setText(t)
        const r = parseKeyValues(t)
        onChange(r.value, !r.error)
      }}
    />
  )
}

/** A list of short values (one item each) as chips. */
export function LinesEditor({ label, hint, value, onChange, placeholder, suggestions, validate, error }: {
  label: ReactNode
  hint?: ReactNode
  value: string[]
  onChange: (v: string[]) => void
  placeholder?: string
  suggestions?: string[]
  validate?: (item: string) => string | undefined
  error?: string
}) {
  return (
    <ListField
      label={label}
      hint={hint ?? 'Press Enter or type a comma after each item.'}
      value={value}
      onChange={onChange}
      placeholder={placeholder}
      suggestions={suggestions}
      validate={validate}
      error={error}
    />
  )
}
