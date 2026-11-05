import { Checkbox, Field, TextField, formStyles as f } from '@/components/form'
import type { components } from '@/lib/api/schema.gen'

type SecretInfo = components['schemas']['SecretInfo']

/** Write-only secret input: leaving it empty keeps a stored value; a checkbox clears it. */
export function SecretInput({ label, hint, info, value, onChange, clear, onClear, multiline }: {
  label: string
  hint?: string
  info?: SecretInfo
  value: string
  onChange: (v: string) => void
  clear: boolean
  onClear: (v: boolean) => void
  multiline?: boolean
}) {
  const stored = info?.source === 'stored'
  const help = stored ? `A value is stored (fingerprint ${info?.fingerprint ?? '—'}). Leave empty to keep it.` : hint
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      {multiline ? (
        <Field label={`${label} (write-only)`} hint={help}>
          {(p) => (
            <textarea {...p} className={f.textarea} rows={4} autoComplete="off" spellCheck={false} value={value}
              onChange={(e) => { onChange(e.target.value); onClear(false) }} />
          )}
        </Field>
      ) : (
        <TextField label={`${label} (write-only)`} type="password" autoComplete="off" value={value} hint={help}
          onChange={(v) => { onChange(v); onClear(false) }} />
      )}
      {stored && <Checkbox label={`Remove the stored ${label.toLowerCase()}`} checked={clear} onChange={(v) => { onClear(v); if (v) onChange('') }} />}
    </div>
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
