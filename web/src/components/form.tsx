import { createContext, type ReactNode, useContext, useEffect, useId, useRef, useState } from 'react'
import {
  Accordion,
  Alert,
  Anchor,
  Checkbox as MCheckbox,
  Code,
  CopyButton,
  Drawer,
  Group,
  Input,
  Modal,
  NumberInput,
  PasswordInput,
  Select,
  Stack,
  TagsInput,
  Text,
  Textarea,
  TextInput,
  ActionIcon,
  Tooltip,
} from '@mantine/core'
import { DateTimePicker } from '@mantine/dates'
import { Check, Copy, KeyRound } from 'lucide-react'
import f from './form.module.css'
import { Button } from './ui'
import { useOverlayEscape } from './overlayStack'
import { fieldErrorMap, summarizeError, type FieldProblem } from '@/lib/api/errors'
import { useCompact } from '@/ui/kit'

export const formStyles = f

const ORDER: ('label' | 'input' | 'description' | 'error')[] = ['label', 'input', 'description', 'error']

/**
 * Required marker drawn by CSS (::after on the label), so the label text and
 * the field's accessible name stay exactly the label.
 */
function req(required: boolean | undefined) {
  return { withAsterisk: false, labelProps: { 'data-required-mark': required ? true : undefined } }
}

/** Labelled form control with optional hint and error, for custom inputs. */
export function Field({ label, hint, error, children, required }: {
  label: ReactNode
  hint?: ReactNode
  error?: string
  required?: boolean
  children: (props: { id: string; 'aria-describedby'?: string; 'aria-invalid'?: boolean }) => ReactNode
}) {
  const id = useId()
  const hintId = `${id}-description`
  const errId = `${id}-error`
  const describedBy = [hint ? hintId : '', error ? errId : ''].filter(Boolean).join(' ') || undefined
  return (
    <Input.Wrapper
      id={id}
      label={label}
      description={hint}
      error={error}
      {...req(required)}
      inputWrapperOrder={ORDER}
      descriptionProps={{ id: hintId }}
      errorProps={{ id: errId, role: 'alert' }}
      className={f.field}
    >
      {children({ id, 'aria-describedby': describedBy, 'aria-invalid': error ? true : undefined })}
    </Input.Wrapper>
  )
}

export function TextField({ label, hint, error, value, onChange, type = 'text', suffix, required, ...rest }: {
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
  /** Unit shown inside the field (e.g. "min", "%"). */
  suffix?: string
  /** Monospace text (ids, URLs, references). */
  mono?: boolean
}) {
  const { mono, autoFocus, ...other } = rest
  const common = {
    label,
    description: hint,
    error,
    value,
    required,
    ...req(required),
    onChange: (e: React.ChangeEvent<HTMLInputElement>) => onChange(e.currentTarget.value),
    inputWrapperOrder: ORDER,
    'data-autofocus': autoFocus || undefined,
    // Monospace applies to the typed text only, not the label.
    styles: mono ? { input: { fontFamily: 'var(--font-mono)' } } : undefined,
    ...other,
  }
  if (type === 'password') return <PasswordInput {...common} />
  return (
    <TextInput
      {...common}
      type={type}
      rightSection={suffix ? <Text size="xs" c="dimmed">{suffix}</Text> : undefined}
      rightSectionWidth={suffix ? Math.max(32, suffix.length * 8 + 16) : undefined}
    />
  )
}

/** Multi-line text (templates, notes, JSON). Grows with its content. */
export function TextAreaField({ label, hint, error, value, onChange, minRows = 3, maxRows = 14, mono, required, ...rest }: {
  label: ReactNode
  hint?: ReactNode
  error?: string
  value: string
  onChange: (v: string) => void
  minRows?: number
  maxRows?: number
  mono?: boolean
  required?: boolean
  placeholder?: string
  disabled?: boolean
  maxLength?: number
  spellCheck?: boolean
  autoComplete?: string
}) {
  return (
    <Textarea
      label={label}
      description={hint}
      error={error}
      value={value}
      required={required}
      {...req(required)}
      onChange={(e) => onChange(e.currentTarget.value)}
      autosize
      minRows={minRows}
      maxRows={maxRows}
      inputWrapperOrder={ORDER}
      styles={mono ? { input: { fontFamily: 'var(--font-mono)' } } : undefined}
      spellCheck={rest.spellCheck ?? !mono}
      {...rest}
    />
  )
}

/**
 * A list of short values (model names, key=value subjects, mappings) as
 * removable chips. Typing a comma or Enter adds one; pasting a list splits it.
 */
export function ListField({ label, hint, error, value, onChange, placeholder, suggestions, validate, splitChars = [',', '\n'], disabled }: {
  label: ReactNode
  hint?: ReactNode
  error?: string
  value: string[]
  onChange: (v: string[]) => void
  placeholder?: string
  /** Known values offered while typing. */
  suggestions?: string[]
  /** Returns a message for an invalid item; invalid items are not added. */
  validate?: (item: string) => string | undefined
  splitChars?: string[]
  disabled?: boolean
}) {
  const [itemError, setItemError] = useState<string | undefined>()
  return (
    <TagsInput
      label={label}
      description={hint}
      error={error ?? itemError}
      value={value}
      data={suggestions}
      splitChars={splitChars}
      placeholder={value.length ? undefined : placeholder}
      clearable
      acceptValueOnBlur
      disabled={disabled}
      inputWrapperOrder={ORDER}
      comboboxProps={{ withinPortal: true }}
      onChange={(next) => {
        const items = next.map((v) => v.trim()).filter(Boolean)
        const bad = validate ? items.map((v) => validate(v)).find(Boolean) : undefined
        setItemError(bad)
        onChange(validate ? items.filter((v) => !validate(v)) : items)
      }}
    />
  )
}

/**
 * Whole or decimal numbers with an optional unit. `null` means empty (for
 * example "no limit"); negative and fractional input is refused unless
 * allowed, instead of silently meaning something else.
 */
export function NumberField({ label, hint, error, value, onChange, unit, min = 0, max, integer = true, placeholder, required, disabled, w }: {
  label: ReactNode
  hint?: ReactNode
  error?: string
  value: number | null
  onChange: (v: number | null) => void
  unit?: string
  min?: number
  max?: number
  integer?: boolean
  placeholder?: string
  required?: boolean
  disabled?: boolean
  /** Field width; numbers rarely need the full row. */
  w?: number | string
}) {
  return (
    <NumberInput
      label={label}
      description={hint}
      error={error}
      value={value ?? ''}
      onChange={(v) => onChange(parseNumberInput(v, { min, max, integer }))}
      min={min}
      max={max}
      allowNegative={min < 0}
      allowDecimal={!integer}
      clampBehavior="strict"
      placeholder={placeholder}
      required={required}
      {...req(required)}
      disabled={disabled}
      hideControls
      maw={w ?? 260}
      rightSection={unit ? <Text size="xs" c="dimmed" pr={8}>{unit}</Text> : undefined}
      rightSectionWidth={unit ? Math.max(32, unit.length * 8 + 16) : undefined}
      inputWrapperOrder={ORDER}
    />
  )
}

/** Normalizes NumberInput output: '' → null, clamps, drops fractions for integers. */
export function parseNumberInput(v: string | number, o: { min?: number; max?: number; integer?: boolean } = {}): number | null {
  if (v === '' || v === null || v === undefined) return null
  let n = typeof v === 'number' ? v : Number(v)
  if (!Number.isFinite(n)) return null
  if (o.integer) n = Math.trunc(n)
  if (o.min !== undefined && n < o.min) n = o.min
  if (o.max !== undefined && n > o.max) n = o.max
  return n
}

/**
 * Date and time picker in the browser's local time; the value is an RFC 3339
 * UTC string ('' = empty), so it round-trips exactly.
 */
export function DateTimeField({ label, hint, error, value, onChange, placeholder = 'Pick a date and time', clearable = true, required }: {
  label: ReactNode
  hint?: ReactNode
  error?: string
  value: string
  onChange: (v: string) => void
  placeholder?: string
  clearable?: boolean
  required?: boolean
}) {
  const date = value ? new Date(value) : null
  return (
    <DateTimePicker
      label={label}
      description={hint ?? `In your local time (${localTimeZone()}).`}
      error={error}
      value={date && !Number.isNaN(date.getTime()) ? date : null}
      onChange={(v) => onChange(v ? new Date(v).toISOString() : '')}
      placeholder={placeholder}
      clearable={clearable}
      required={required}
      {...req(required)}
      valueFormat="D MMM YYYY, HH:mm"
      inputWrapperOrder={ORDER}
      popoverProps={{ withinPortal: true }}
    />
  )
}

/** The browser's IANA time zone, e.g. Europe/Vilnius. */
export function localTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

/** Server rule for secret references: env:HP_SECRET_* or file:/absolute/path. */
export const SECRET_REF_RE = /^(env:HP_SECRET_[A-Z0-9_]+|file:\/\S+)$/

/** Plain-language problem with a secret reference, or undefined when valid or empty. */
export function checkSecretRef(ref: string): string | undefined {
  const v = ref.trim()
  if (!v) return undefined
  if (v.startsWith('env:') && !v.startsWith('env:HP_SECRET_')) return 'Environment variables used as secrets must start with HP_SECRET_ (for example env:HP_SECRET_OPENAI_KEY).'
  if (!SECRET_REF_RE.test(v)) return 'Use env:HP_SECRET_NAME or file:/absolute/path.'
  return undefined
}

/**
 * A reference to a secret kept outside Harbor Pilot. `example` names the
 * variable in the placeholder (OPENAI_KEY → env:HP_SECRET_OPENAI_KEY).
 */
export function SecretRefField({ label = 'Secret reference', hint, error, value, onChange, example = 'API_KEY', disabled }: {
  label?: ReactNode
  hint?: ReactNode
  error?: string
  value: string
  onChange: (v: string) => void
  example?: string
  disabled?: boolean
}) {
  const local = checkSecretRef(value)
  return (
    <TextInput
      label={label}
      description={hint ?? <>An environment variable starting with <Code>HP_SECRET_</Code>, or a file under the server's secret roots.</>}
      error={error ?? local}
      value={value}
      onChange={(e) => onChange(e.currentTarget.value)}
      placeholder={`env:HP_SECRET_${example}`}
      styles={{ input: { fontFamily: 'var(--font-mono)' } }}
      disabled={disabled}
      autoComplete="off"
      spellCheck={false}
      inputWrapperOrder={ORDER}
    />
  )
}

/** Names must be lowercase slugs: letters, digits, '.', '_' and '-'. */
export const NAME_RE = /^[a-z0-9][a-z0-9._-]{0,62}$/

/** Plain-language problem with a resource name, or undefined when valid. */
export function nameError(name: string): string | undefined {
  if (!name) return undefined
  if (!NAME_RE.test(name)) return 'Use lowercase letters, digits, dots, dashes or underscores, starting with a letter or digit (at most 63).'
  return undefined
}

/** "Claude Code on a laptop" → "claude-code-on-a-laptop". */
export function slugify(text: string): string {
  return text
    .toLowerCase()
    .normalize('NFKD')
    .replace(/[̀-ͯ]/g, '')
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/^[^a-z0-9]+/, '')
    .replace(/-+$/, '')
    .slice(0, 63)
}

/**
 * The short name of a resource (used in URLs and the API). Shows the rule up
 * front, checks it live, and explains why it is locked after creation.
 */
export function NameField({ label = 'Short name', value, onChange, error, locked, autoFocus }: {
  label?: ReactNode
  value: string
  onChange: (v: string) => void
  error?: string
  /** Existing resources cannot be renamed. */
  locked?: boolean
  autoFocus?: boolean
}) {
  return (
    <TextInput
      label={label}
      description={locked ? 'Names cannot be changed after creation.' : 'Lowercase letters, digits, dots, dashes or underscores. Used in URLs and the API.'}
      error={error ?? nameError(value)}
      value={value}
      onChange={(e) => onChange(e.currentTarget.value)}
      disabled={locked}
      required={!locked}
      {...req(!locked)}
      styles={{ input: { fontFamily: 'var(--font-mono)' } }}
      autoComplete="off"
      spellCheck={false}
      data-autofocus={autoFocus || undefined}
      inputWrapperOrder={ORDER}
    />
  )
}

/** Server rule: passwords are 12 to 1024 bytes. */
export const PASSWORD_MIN = 12

/** Problems with a new password and its confirmation (undefined when fine or not typed yet). */
export function newPasswordProblems(password: string, confirm: string, current?: string): { password?: string; confirm?: string } {
  const out: { password?: string; confirm?: string } = {}
  const bytes = new TextEncoder().encode(password).length
  if (password && bytes < PASSWORD_MIN) out.password = `Use at least ${PASSWORD_MIN} characters.`
  else if (bytes > 1024) out.password = 'Use at most 1024 bytes.'
  else if (password && current !== undefined && password === current) out.password = 'Choose a password different from the current one.'
  if (confirm && confirm !== password) out.confirm = 'The passwords do not match.'
  return out
}

/** True when a new password and its confirmation can be submitted. */
export function newPasswordReady(password: string, confirm: string, current?: string): boolean {
  const p = newPasswordProblems(password, confirm, current)
  return !!password && password === confirm && !p.password && !p.confirm
}

/**
 * New password with confirmation and the length rule shown up front. Pass
 * `current` to refuse reusing it. Use newPasswordReady() to enable Save.
 */
export function NewPasswordFields({ password, confirm, onPassword, onConfirm, current, label = 'New password' }: {
  password: string
  confirm: string
  onPassword: (v: string) => void
  onConfirm: (v: string) => void
  current?: string
  label?: string
}) {
  const p = newPasswordProblems(password, confirm, current)
  return (
    <>
      <PasswordInput
        label={label}
        description={`At least ${PASSWORD_MIN} characters. A passphrase of several words is easiest to remember.`}
        value={password}
        onChange={(e) => onPassword(e.currentTarget.value)}
        error={p.password}
        autoComplete="new-password"
        required
        {...req(true)}
        inputWrapperOrder={ORDER}
      />
      <PasswordInput
        label="Repeat the new password"
        value={confirm}
        onChange={(e) => onConfirm(e.currentTarget.value)}
        error={p.confirm}
        autoComplete="new-password"
        required
        {...req(true)}
        inputWrapperOrder={ORDER}
      />
    </>
  )
}

// Mantine Select treats "" as "nothing selected"; options may use "" for
// "none", so it travels as a sentinel.
const NONE = '\u0000none'

export function SelectField<T extends string>({ label, hint, value, onChange, options, disabled, error, placeholder, required }: {
  label: ReactNode
  hint?: ReactNode
  value: T
  onChange: (v: T) => void
  options: { value: T; label: string }[]
  disabled?: boolean
  error?: string
  placeholder?: string
  required?: boolean
}) {
  const data = options.map((o) => ({ value: o.value === '' ? NONE : o.value, label: o.label }))
  return (
    <Select
      label={label}
      description={hint}
      error={error}
      data={data}
      value={value === '' ? NONE : value}
      onChange={(v) => v !== null && onChange((v === NONE ? '' : v) as T)}
      disabled={disabled}
      placeholder={placeholder}
      required={required}
      {...req(required)}
      allowDeselect={false}
      searchable={options.length > 8}
      comboboxProps={{ withinPortal: true }}
      inputWrapperOrder={ORDER}
      checkIconPosition="right"
    />
  )
}

export function Checkbox({ label, checked, onChange, disabled, description }: {
  label: ReactNode
  checked: boolean
  onChange: (v: boolean) => void
  disabled?: boolean
  description?: ReactNode
}) {
  return <MCheckbox label={label} description={description} checked={checked} disabled={disabled} onChange={(e) => onChange(e.currentTarget.checked)} />
}

/* ---------- overlays ---------- */

const OverlayContext = createContext<{ markSaved: () => void } | null>(null)

/**
 * Error keys each form reads through useFieldErrors, per error object: the
 * form shows those on its fields, so MutationError lists only the rest.
 */
const shownOnFields = new WeakMap<object, Set<string>>()

/**
 * For overlays that stay open after a successful save (a token shown once,
 * a "saved" message): call the returned function after saving so closing
 * does not ask to discard what was just saved.
 */
export function useMarkSaved(): () => void {
  const ctx = useContext(OverlayContext)
  return ctx?.markSaved ?? noop
}

function noop() {}

/**
 * Tracks whether the user changed anything inside an overlay since it
 * opened: typing, toggling, or picking an option in a dropdown.
 */
function useTouched(open: boolean) {
  const [touched, setTouched] = useState(false)
  useEffect(() => {
    if (open) setTouched(false)
  }, [open])
  // React events bubble through portals, so input in a dialog opened from
  // this overlay reaches these handlers too. The innermost overlay claims
  // the event and the ones around it ignore it.
  const claim = (e: React.SyntheticEvent): boolean => {
    const n = e.nativeEvent as Event & { hpOverlayTouched?: boolean }
    if (n.hpOverlayTouched) return false
    n.hpOverlayTouched = true
    return true
  }
  const mark = (e: React.SyntheticEvent) => {
    if (claim(e)) setTouched(true)
  }
  const handlers = {
    onInput: mark,
    onChange: mark,
    onClick: (e: React.MouseEvent) => {
      if ((e.target as HTMLElement).closest?.('[role="option"]')) mark(e)
    },
  }
  return { touched, handlers, reset: () => setTouched(false) }
}

/**
 * Close behaviour shared by dialogs and drawers: Esc only closes the top
 * overlay, and closing with unsaved changes asks first.
 */
function useGuardedClose({ open, onClose, dirty, lockClose }: { open: boolean; onClose: () => void; dirty?: boolean; lockClose?: boolean }) {
  const { touched, handlers, reset } = useTouched(open)
  const [confirming, setConfirming] = useState(false)
  const isDirty = dirty ?? touched
  const request = () => {
    if (lockClose) return
    if (isDirty) setConfirming(true)
    else onClose()
  }
  useOverlayEscape(open, request)
  const confirm = (
    <ConfirmDialog
      open={confirming}
      title="Discard changes?"
      message="You have unsaved changes. Closing now loses them."
      confirmLabel="Discard"
      cancelLabel="Keep editing"
      onCancel={() => setConfirming(false)}
      onConfirm={() => {
        setConfirming(false)
        onClose()
      }}
    />
  )
  return { request, handlers, confirm, saved: { markSaved: reset } }
}

interface OverlayProps {
  open: boolean
  title: ReactNode
  onClose: () => void
  children: ReactNode
  /**
   * Unsaved changes: closing asks to discard first. When omitted, any
   * input inside the overlay since it opened counts as unsaved.
   */
  dirty?: boolean
  /**
   * One-time reveal (tokens, links): no close button, Esc or outside click.
   * The content must offer an explicit button that calls onClose.
   */
  lockClose?: boolean
  /** Actions pinned to the bottom. A trailing <DialogActions> is pinned as well. */
  footer?: ReactNode
}

/**
 * Modal dialog: the title stays at the top, actions stay at the bottom and
 * the body scrolls between them; full screen on phones.
 */
export function Dialog({ open, title, onClose, children, wide, dirty, lockClose, footer }: OverlayProps & {
  /** Wider dialog for structured editors. */
  wide?: boolean
}) {
  const compact = useCompact()
  const g = useGuardedClose({ open, onClose, dirty, lockClose })
  return (
    <>
      <Modal
        opened={open}
        onClose={g.request}
        title={title}
        size={wide ? 'xl' : 'lg'}
        fullScreen={compact}
        centered={!compact}
        radius="lg"
        closeOnEscape={false}
        closeOnClickOutside={!lockClose}
        withCloseButton={!lockClose}
        closeButtonProps={{ 'aria-label': 'Close dialog' }}
        classNames={{ title: f.dialogTitle, body: f.dialogBody, content: f.overlayContent }}
        transitionProps={{ duration: 0 }}
      >
        <OverlayContext.Provider value={g.saved}>
          <Stack gap="md" {...g.handlers}>
            {children}
            {footer && <DialogActions>{footer}</DialogActions>}
          </Stack>
        </OverlayContext.Provider>
      </Modal>
      {g.confirm}
    </>
  )
}

/**
 * Side panel for viewing and editing one object. Same close behaviour as
 * Dialog; full screen on phones. Dialogs opened from it close on their own.
 */
export function FormDrawer({ open, title, onClose, children, dirty, lockClose, footer, size = 'lg' }: OverlayProps & {
  size?: 'md' | 'lg' | 'xl'
}) {
  const compact = useCompact()
  const g = useGuardedClose({ open, onClose, dirty, lockClose })
  return (
    <>
      <Drawer
        opened={open}
        onClose={g.request}
        title={title}
        position="right"
        size={compact ? '100%' : size}
        closeOnEscape={false}
        closeOnClickOutside={!lockClose}
        withCloseButton={!lockClose}
        closeButtonProps={{ 'aria-label': 'Close panel' }}
        classNames={{ title: f.dialogTitle, body: f.dialogBody, content: f.overlayContent }}
        transitionProps={{ duration: 0 }}
      >
        <OverlayContext.Provider value={g.saved}>
          <Stack gap="md" {...g.handlers}>
            {children}
            {footer && <DialogActions>{footer}</DialogActions>}
          </Stack>
        </OverlayContext.Provider>
      </Drawer>
      {g.confirm}
    </>
  )
}

/** Collapsed "Advanced" section for rarely needed fields. */
export function Advanced({ children, label = 'Advanced', defaultOpen = false }: { children: ReactNode; label?: string; defaultOpen?: boolean }) {
  return (
    <Accordion variant="contained" radius="md" defaultValue={defaultOpen ? 'adv' : null} className={f.advanced}>
      <Accordion.Item value="adv">
        <Accordion.Control>{label}</Accordion.Control>
        <Accordion.Panel>
          <Stack gap="md">{children}</Stack>
        </Accordion.Panel>
      </Accordion.Item>
    </Accordion>
  )
}

/** Shows a secret exactly once with a copy button. */
export function SecretOnce({ value, label }: { value: string; label: string }) {
  // A revealed secret means the action already succeeded: closing the
  // overlay afterwards must not ask to discard anything.
  const markSaved = useMarkSaved()
  useEffect(() => markSaved(), [markSaved])
  return (
    <Stack gap="xs">
      <Alert color="yellow" variant="light" icon={<KeyRound size={16} aria-hidden="true" />}>
        <Text size="sm">
          Copy this {label} now. It is shown <strong>only once</strong>; Harbor Pilot keeps only a non-reversible verifier.
        </Text>
      </Alert>
      <Group gap="xs" wrap="nowrap" className={f.secret}>
        <Code data-testid="secret-value" className={f.secretValue}>
          {value}
        </Code>
        <CopyButton value={value} timeout={1500}>
          {({ copied, copy }) => (
            <Tooltip label={copied ? 'Copied' : 'Copy'}>
              <ActionIcon variant="default" size="lg" aria-label={`Copy ${label}`} onClick={copy}>
                {copied ? <Check size={16} /> : <Copy size={16} />}
              </ActionIcon>
            </Tooltip>
          )}
        </CopyButton>
      </Group>
    </Stack>
  )
}

/**
 * A dialog that shows a secret once. It cannot be dismissed by Esc or an
 * outside click; the user closes it with the explicit button after copying.
 */
export function RevealDialog({ open, title, value, label, onDone, children, doneLabel = "I've copied it" }: {
  open: boolean
  title: ReactNode
  value: string
  /** What the secret is, e.g. "access token". */
  label: string
  onDone: () => void
  /** Extra guidance (snippets, next steps). */
  children?: ReactNode
  doneLabel?: string
}) {
  return (
    <Dialog open={open} title={title} onClose={onDone} lockClose footer={<Button variant="primary" onClick={onDone}>{doneLabel}</Button>}>
      <SecretOnce value={value} label={label} />
      {children}
    </Dialog>
  )
}

function fieldShown(p: FieldProblem, inline: string[]): boolean {
  const last = p.field.split('.').pop() ?? p.field
  return inline.some((k) => k === p.location || k === p.field || k === last)
}

/**
 * Explains why a mutation failed, in plain words. Field problems shown on
 * their fields (listed in `inlineFields`) are not repeated. A request id is
 * kept as small print for support. An ended session offers to sign in.
 */
export function MutationError({ error, inlineFields }: {
  error: unknown
  /** Field keys whose errors the form shows on the fields themselves. */
  inlineFields?: string[]
}) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (error) ref.current?.scrollIntoView?.({ block: 'nearest' })
  }, [error])
  if (!error) return null
  const s = summarizeError(error)
  const shown = inlineFields ?? [...((typeof error === 'object' && shownOnFields.get(error)) || [])]
  const rest = s.fields.filter((p) => !fieldShown(p, shown))
  return (
    <div ref={ref}>
      <Alert color="red" variant="light" role="alert">
        <Stack gap={4}>
          <Text size="sm">{s.title}</Text>
          {rest.length > 0 && (
            <ul className={f.errorList}>
              {rest.map((p) => (
                <li key={p.location}>
                  <Text size="sm" span fw={600}>{fieldLabel(p.field)}:</Text> <Text size="sm" span>{p.message}</Text>
                </li>
              ))}
            </ul>
          )}
          {s.signIn && (
            <Anchor size="sm" href={`/login?next=${encodeURIComponent(window.location.pathname + window.location.search)}`}>
              Sign in again
            </Anchor>
          )}
          {s.requestId && (
            <Text size="xs" c="dimmed">
              Reference {s.requestId}
            </Text>
          )}
        </Stack>
      </Alert>
    </div>
  )
}

function fieldLabel(field: string): string {
  const parts = field.split('.')
  const last = parts[parts.length - 1] ?? field
  const idx = parts.find((p) => /^\d+$/.test(p))
  const label = last.replace(/_ref$/, ' reference').replace(/[_-]+/g, ' ')
  const nice = label.charAt(0).toUpperCase() + label.slice(1)
  return idx !== undefined ? `${nice} (item ${Number(idx) + 1})` : nice
}

/**
 * Field errors from a failed save, keyed by full location (body.spec.x),
 * by path without body/spec (x, models.0.id) and by last segment.
 */
export function useFieldErrors(error: unknown): Record<string, string> {
  const map = fieldErrorMap(error)
  if (!error || typeof error !== 'object') return map
  let seen = shownOnFields.get(error)
  if (!seen) {
    seen = new Set()
    shownOnFields.set(error, seen)
  }
  const keys = seen
  // Record which errors the form reads (and so shows on its fields).
  return new Proxy(map, {
    get(target, key) {
      if (typeof key === 'string' && key in target) keys.add(key)
      return Reflect.get(target, key)
    },
  })
}

/** Dialog footer: secondary actions left of the primary one; pinned in overlays. */
export function DialogActions({ children }: { children: ReactNode }) {
  return <div className={f.actions}>{children}</div>
}

/**
 * A button that asks for confirmation in a dialog before running `onConfirm`
 * (replaces window.confirm).
 */
export function ConfirmButton({ title, message, confirmLabel, onConfirm, danger = true, busy, children, icon, label, size, variant = 'ghost', disabled }: {
  title: ReactNode
  message: ReactNode
  confirmLabel: string
  onConfirm: () => void
  danger?: boolean
  busy?: boolean
  children?: ReactNode
  icon?: React.ComponentProps<typeof Button>['icon']
  /** aria-label for an icon-only trigger. */
  label?: string
  size?: 'small'
  variant?: React.ComponentProps<typeof Button>['variant']
  disabled?: boolean
}) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button
        variant={variant}
        size={size}
        icon={icon}
        aria-label={label}
        disabled={disabled}
        busy={busy}
        onClick={(e) => {
          // Never trigger the row or card the button sits in.
          e.stopPropagation()
          setOpen(true)
        }}
      >
        {children}
      </Button>
      <ConfirmDialog
        open={open}
        title={title}
        message={message}
        confirmLabel={confirmLabel}
        danger={danger}
        onCancel={() => setOpen(false)}
        onConfirm={() => {
          setOpen(false)
          onConfirm()
        }}
      />
    </>
  )
}

export function ConfirmDialog({ open, title, message, confirmLabel, cancelLabel = 'Cancel', danger = true, onCancel, onConfirm }: {
  open: boolean
  title: ReactNode
  message: ReactNode
  confirmLabel: string
  cancelLabel?: string
  danger?: boolean
  onCancel: () => void
  onConfirm: () => void
}) {
  useOverlayEscape(open, onCancel)
  return (
    <Modal
      opened={open}
      onClose={onCancel}
      title={title}
      size="sm"
      centered
      radius="lg"
      closeOnEscape={false}
      closeButtonProps={{ 'aria-label': 'Close dialog' }}
      classNames={{ title: f.dialogTitle }}
      transitionProps={{ duration: 0 }}
      zIndex={400}
    >
      <Stack gap="md">
        <Text size="sm">{message}</Text>
        <Group justify="flex-end" gap="xs">
          <Button onClick={onCancel}>{cancelLabel}</Button>
          <Button variant={danger ? 'danger' : 'primary'} onClick={onConfirm} data-autofocus>
            {confirmLabel}
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
