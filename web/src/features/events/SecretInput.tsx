import { Stack } from '@mantine/core'
import { Checkbox, TextAreaField, TextField } from '@/components/form'
import type { components } from '@/lib/api/schema.gen'

type SecretInfo = components['schemas']['SecretInfo']

/** Write-only secret input: leaving it empty keeps a stored value; a checkbox clears it. */
export function SecretInput({ label, hint, info, value, onChange, clear, onClear, multiline, required, error, placeholder }: {
  label: string
  hint?: string
  info?: SecretInfo
  value: string
  onChange: (v: string) => void
  clear: boolean
  onClear: (v: boolean) => void
  multiline?: boolean
  required?: boolean
  error?: string
  placeholder?: string
}) {
  const stored = info?.source === 'stored'
  const help = stored ? `Stored (fingerprint ${info?.fingerprint ?? '—'}). Leave empty to keep it.` : hint
  return (
    <Stack gap={6}>
      {multiline ? (
        <TextAreaField label={label} hint={help} value={value} mono minRows={4} spellCheck={false} autoComplete="off" required={required} error={error}
          placeholder={placeholder} onChange={(v) => { onChange(v); onClear(false) }} />
      ) : (
        <TextField label={label} type="password" autoComplete="off" value={value} hint={help} required={required} error={error} placeholder={placeholder}
          onChange={(v) => { onChange(v); onClear(false) }} />
      )}
      {stored && <Checkbox label={`Remove the stored ${label.toLowerCase()}`} checked={clear} onChange={(v) => { onClear(v); if (v) onChange('') }} />}
    </Stack>
  )
}

/** Builds the secrets map for a write: new values, explicit clears. */
export function secretChanges(entries: { name: string; value: string; clear: boolean }[]): Record<string, string> | undefined {
  const out: Record<string, string> = {}
  for (const e of entries) {
    if (e.value) out[e.name] = e.value
    else if (e.clear) out[e.name] = ''
  }
  return Object.keys(out).length ? out : undefined
}
