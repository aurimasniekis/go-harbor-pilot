import { type ReactNode, useId, useState } from 'react'
import {
  Accordion,
  Alert,
  Checkbox as MCheckbox,
  Code,
  CopyButton,
  Group,
  Input,
  Modal,
  PasswordInput,
  Select,
  Stack,
  Text,
  TextInput,
  ActionIcon,
  Tooltip,
} from '@mantine/core'
import { Check, Copy, KeyRound } from 'lucide-react'
import f from './form.module.css'
import { Button } from './ui'
import { ApiError, fieldErrors } from '@/lib/api/client'
import { useCompact } from '@/ui/kit'

export const formStyles = f

/** Labelled form control with optional hint and error, for custom inputs. */
export function Field({ label, hint, error, children }: {
  label: ReactNode
  hint?: ReactNode
  error?: string
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
      inputWrapperOrder={['label', 'input', 'description', 'error']}
      descriptionProps={{ id: hintId }}
      errorProps={{ id: errId, role: 'alert' }}
      className={f.field}
    >
      {children({ id, 'aria-describedby': describedBy, 'aria-invalid': error ? true : undefined })}
    </Input.Wrapper>
  )
}

export function TextField({ label, hint, error, value, onChange, type = 'text', suffix, ...rest }: {
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
}) {
  const common = {
    label,
    description: hint,
    error,
    value,
    onChange: (e: React.ChangeEvent<HTMLInputElement>) => onChange(e.currentTarget.value),
    inputWrapperOrder: ['label', 'input', 'description', 'error'] as ('label' | 'input' | 'description' | 'error')[],
    // Required stays a native attribute; the asterisk would join the label's
    // accessible name.
    withAsterisk: false,
    ...rest,
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

// Mantine Select treats "" as "nothing selected"; options may use "" for
// "none", so it travels as a sentinel.
const NONE = '\u0000none'

export function SelectField<T extends string>({ label, hint, value, onChange, options, disabled, error, placeholder }: {
  label: ReactNode
  hint?: ReactNode
  value: T
  onChange: (v: T) => void
  options: { value: T; label: string }[]
  disabled?: boolean
  error?: string
  placeholder?: string
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
      allowDeselect={false}
      searchable={options.length > 8}
      comboboxProps={{ withinPortal: true }}
      inputWrapperOrder={['label', 'input', 'description', 'error']}
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

/** Modal dialog; full screen on phones. */
export function Dialog({ open, title, onClose, children, wide }: {
  open: boolean
  title: ReactNode
  onClose: () => void
  children: ReactNode
  /** Wider dialog for structured editors. */
  wide?: boolean
}) {
  const compact = useCompact()
  return (
    <Modal
      opened={open}
      onClose={onClose}
      title={title}
      size={wide ? 'xl' : 'lg'}
      fullScreen={compact}
      centered={!compact}
      radius="lg"
      closeButtonProps={{ 'aria-label': 'Close dialog' }}
      classNames={{ title: f.dialogTitle, body: f.dialogBody }}
      transitionProps={{ duration: 0 }}
    >
      <Stack gap="md">{children}</Stack>
    </Modal>
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

/** Renders an error for a failed mutation (never shows request bodies). */
export function MutationError({ error }: { error: unknown }) {
  if (!error) return null
  const msg = error instanceof ApiError ? error.message : 'The request failed.'
  const rid = error instanceof ApiError ? error.requestId : undefined
  return (
    <Alert color="red" variant="light" role="alert">
      <Text size="sm">
        {msg}
        {rid ? ` (request ${rid})` : ''}
      </Text>
    </Alert>
  )
}

export function useFieldErrors(error: unknown): Record<string, string> {
  return fieldErrors(error)
}

/** Dialog footer: secondary actions left of the primary one. */
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
      <Button variant={variant} size={size} icon={icon} aria-label={label} disabled={disabled} busy={busy} onClick={() => setOpen(true)}>
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

export function ConfirmDialog({ open, title, message, confirmLabel, danger = true, onCancel, onConfirm }: {
  open: boolean
  title: ReactNode
  message: ReactNode
  confirmLabel: string
  danger?: boolean
  onCancel: () => void
  onConfirm: () => void
}) {
  return (
    <Modal opened={open} onClose={onCancel} title={title} size="sm" centered radius="lg" closeButtonProps={{ 'aria-label': 'Close dialog' }}
      classNames={{ title: f.dialogTitle }}>
      <Stack gap="md">
        <Text size="sm">{message}</Text>
        <DialogActions>
          <Button onClick={onCancel}>Cancel</Button>
          <Button variant={danger ? 'danger' : 'primary'} onClick={onConfirm}>
            {confirmLabel}
          </Button>
        </DialogActions>
      </Stack>
    </Modal>
  )
}
