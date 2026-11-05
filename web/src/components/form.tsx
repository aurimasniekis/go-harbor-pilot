import { type ReactNode, useEffect, useId, useRef, useState } from 'react'
import { Check, Copy, X } from 'lucide-react'
import f from './form.module.css'
import { Banner, Button, uiStyles } from './ui'
import { ApiError, fieldErrors } from '@/lib/api/client'

export const formStyles = f

/** Labelled form control with optional hint and error. */
export function Field({ label, hint, error, children }: {
  label: ReactNode
  hint?: ReactNode
  error?: string
  children: (props: { id: string; 'aria-describedby'?: string; 'aria-invalid'?: boolean }) => ReactNode
}) {
  const id = useId()
  const hintId = `${id}-hint`
  const errId = `${id}-err`
  const describedBy = [hint ? hintId : '', error ? errId : ''].filter(Boolean).join(' ') || undefined
  return (
    <div className={f.field}>
      <label htmlFor={id} className={f.label}>
        {label}
      </label>
      {children({ id, 'aria-describedby': describedBy, 'aria-invalid': error ? true : undefined })}
      {hint && (
        <span id={hintId} className={f.hint}>
          {hint}
        </span>
      )}
      {error && (
        <span id={errId} className={f.error} role="alert">
          {error}
        </span>
      )}
    </div>
  )
}

export function TextField({ label, hint, error, value, onChange, type = 'text', ...rest }: {
  label: ReactNode
  hint?: ReactNode
  error?: string
  value: string
  onChange: (v: string) => void
  type?: string
  placeholder?: string
  autoComplete?: string
  required?: boolean
  autoFocus?: boolean
  disabled?: boolean
  maxLength?: number
}) {
  return (
    <Field label={label} hint={hint} error={error}>
      {(p) => <input {...p} {...rest} type={type} className={uiStyles.input} value={value} onChange={(e) => onChange(e.target.value)} />}
    </Field>
  )
}

export function SelectField<T extends string>({ label, hint, value, onChange, options, disabled }: {
  label: ReactNode
  hint?: ReactNode
  value: T
  onChange: (v: T) => void
  options: { value: T; label: string }[]
  disabled?: boolean
}) {
  return (
    <Field label={label} hint={hint}>
      {(p) => (
        <select {...p} className={uiStyles.select} value={value} disabled={disabled} onChange={(e) => onChange(e.target.value as T)}>
          {options.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      )}
    </Field>
  )
}

export function Checkbox({ label, checked, onChange, disabled }: {
  label: ReactNode
  checked: boolean
  onChange: (v: boolean) => void
  disabled?: boolean
}) {
  return (
    <label className={f.check}>
      <input type="checkbox" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
      {label}
    </label>
  )
}

/** Accessible modal dialog built on the native <dialog> element. */
export function Dialog({ open, title, onClose, children }: {
  open: boolean
  title: ReactNode
  onClose: () => void
  children: ReactNode
}) {
  const ref = useRef<HTMLDialogElement>(null)
  const titleId = useId()
  useEffect(() => {
    const d = ref.current
    if (!d) return
    if (open && !d.open) d.showModal?.()
    if (!open && d.open) d.close()
  }, [open])
  return (
    <dialog ref={ref} className={f.dialog} aria-labelledby={titleId} onClose={onClose} onCancel={onClose}>
      {open && (
        <>
          <div className={f.dialogHeader}>
            <h2 id={titleId} style={{ fontSize: 'var(--text-md)' }}>
              {title}
            </h2>
            <Button variant="ghost" icon={X} aria-label="Close dialog" onClick={onClose} />
          </div>
          <div className={f.dialogBody}>{children}</div>
        </>
      )}
    </dialog>
  )
}

/** Shows a secret exactly once with a copy button. */
export function SecretOnce({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <div className={f.field}>
      <Banner tone="warning">
        This {label} is shown <strong>only once</strong>. Copy it now and store it securely; Harbor Pilot keeps only a
        non-reversible verifier.
      </Banner>
      <div className={f.secret}>
        <code data-testid="secret-value">{value}</code>
        <Button
          size="small"
          icon={copied ? Check : Copy}
          aria-label={`Copy ${label}`}
          onClick={() => {
            void navigator.clipboard?.writeText(value).then(() => setCopied(true))
          }}
        />
      </div>
    </div>
  )
}

/** Renders an error banner for a failed mutation (never shows request bodies). */
export function MutationError({ error }: { error: unknown }) {
  if (!error) return null
  const msg = error instanceof ApiError ? error.message : 'The request failed.'
  const rid = error instanceof ApiError ? error.requestId : undefined
  return (
    <Banner tone="error" role="alert">
      {msg}
      {rid ? ` (request ${rid})` : ''}
    </Banner>
  )
}

export function useFieldErrors(error: unknown): Record<string, string> {
  return fieldErrors(error)
}
