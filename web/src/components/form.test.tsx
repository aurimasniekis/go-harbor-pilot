import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { render as rtlRender, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider, TextInput } from '@mantine/core'
import { theme } from '@/theme/theme'
import { ApiError } from '@/lib/api/apiError'
import { checkSecretRef, Dialog, FormDrawer, MutationError, nameError, parseNumberInput, RevealDialog, SecretOnce, slugify, useFieldErrors } from './form'
import { KeyValueEditor } from './KeyValueEditor'

const render = (ui: React.ReactElement) => rtlRender(<MantineProvider theme={theme}>{ui}</MantineProvider>)

describe('Dialog close guard', () => {
  it('closes at once when nothing was changed', async () => {
    const onClose = vi.fn()
    render(<Dialog open title="Add rule" onClose={onClose}><TextInput label="Name" /></Dialog>)
    await userEvent.keyboard('{Escape}')
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('asks before discarding typed input, and can keep editing', async () => {
    const onClose = vi.fn()
    render(<Dialog open title="Add rule" onClose={onClose}><TextInput label="Name" /></Dialog>)
    await userEvent.type(screen.getByLabelText('Name'), 'quota')
    await userEvent.keyboard('{Escape}')
    expect(onClose).not.toHaveBeenCalled()
    expect(screen.getByText('Discard changes?')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Keep editing' }))
    expect(onClose).not.toHaveBeenCalled()
    expect(screen.getByLabelText('Name')).toHaveValue('quota')
    await userEvent.click(screen.getByRole('button', { name: 'Close dialog' }))
    await userEvent.click(screen.getByRole('button', { name: 'Discard' }))
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('does not ask after the dialog revealed a result of its own save', async () => {
    const onClose = vi.fn()
    function Issue() {
      const [token, setToken] = useState('')
      return (
        <Dialog open title="Tokens" onClose={onClose}>
          <TextInput label="Token name" />
          <button type="button" onClick={() => setToken('hpa_x')}>Issue</button>
          {token && <SecretOnce value={token} label="API token" />}
        </Dialog>
      )
    }
    render(<Issue />)
    await userEvent.type(screen.getByLabelText('Token name'), 'ci')
    await userEvent.click(screen.getByRole('button', { name: 'Issue' }))
    await userEvent.keyboard('{Escape}')
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('respects an explicit dirty flag', async () => {
    const onClose = vi.fn()
    render(<Dialog open title="Edit" onClose={onClose} dirty><p>body</p></Dialog>)
    await userEvent.keyboard('{Escape}')
    expect(screen.getByText('Discard changes?')).toBeInTheDocument()
  })
})

describe('nested overlays', () => {
  it('Esc in a dialog opened from a drawer closes only the dialog', async () => {
    const closeDrawer = vi.fn()
    function Page() {
      const [dialog, setDialog] = useState(true)
      return (
        <FormDrawer open title="Account" onClose={closeDrawer}>
          <p>details</p>
          <Dialog open={dialog} title="Edit account" onClose={() => setDialog(false)}>
            <p>form</p>
          </Dialog>
        </FormDrawer>
      )
    }
    render(<Page />)
    await userEvent.keyboard('{Escape}')
    expect(screen.queryByText('form')).toBeNull()
    expect(closeDrawer).not.toHaveBeenCalled()
    await userEvent.keyboard('{Escape}')
    expect(closeDrawer).toHaveBeenCalledOnce()
  })
})

describe('RevealDialog', () => {
  it('cannot be dismissed by Esc; only the explicit button closes it', async () => {
    const onDone = vi.fn()
    render(<RevealDialog open title="Token created" value="hpk_secret" label="access token" onDone={onDone} />)
    await userEvent.keyboard('{Escape}')
    expect(onDone).not.toHaveBeenCalled()
    expect(screen.queryByRole('button', { name: 'Close dialog' })).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: "I've copied it" }))
    expect(onDone).toHaveBeenCalledOnce()
  })
})

describe('MutationError', () => {
  const err = new ApiError(422, 'validation_failed', 'validation failed', 'req-7', {
    fields: [
      { location: 'body.name', message: 'expected string to match pattern ^[a-z0-9][a-z0-9._-]{0,62}$' },
      { location: 'body.spec.ttl_seconds', message: 'expected number >= 0' },
    ],
  })

  it('lists every field problem in plain words with a reference', () => {
    render(<MutationError error={err} />)
    expect(screen.getByText('Fix the 2 highlighted fields.')).toBeInTheDocument()
    expect(screen.getByText('Must be at least 0')).toBeInTheDocument()
    expect(screen.getByText('Reference req-7')).toBeInTheDocument()
    expect(screen.queryByText(/validation failed/)).toBeNull()
  })

  it('does not repeat problems the form shows on its fields', () => {
    render(<MutationError error={err} inlineFields={['name']} />)
    expect(screen.queryByText(/Use lowercase letters/)).toBeNull()
    expect(screen.getByText('Must be at least 0')).toBeInTheDocument()
  })

  it('inside a dialog, does not repeat errors the form shows on its fields', () => {
    function Form() {
      const errs = useFieldErrors(err)
      return (
        <Dialog open title="Add" onClose={() => {}}>
          <TextInput label="Name" error={errs['body.name']} />
          <MutationError error={err} />
        </Dialog>
      )
    }
    render(<Form />)
    expect(screen.getAllByText(/Use lowercase letters/)).toHaveLength(1)
    expect(screen.getByText('Must be at least 0')).toBeInTheDocument()
  })

  it('offers to sign in again when the session ended', () => {
    render(<MutationError error={new ApiError(401, 'unauthenticated', 'authentication required')} />)
    expect(screen.getByRole('link', { name: 'Sign in again' })).toHaveAttribute('href', expect.stringContaining('/login?next='))
  })
})

describe('field helpers', () => {
  it.each([
    ['', {}, null],
    ['12', { integer: true }, 12],
    [12.7, { integer: true }, 12],
    [-5, { min: 0 }, 0],
    [500, { max: 100 }, 100],
    ['0.5', { integer: false }, 0.5],
  ] as const)('parseNumberInput(%j, %j) = %j', (input, opts, want) => {
    expect(parseNumberInput(input, opts)).toBe(want)
  })

  it.each([
    ['', undefined],
    ['env:HP_SECRET_OPENAI_KEY', undefined],
    ['file:/etc/harbor-pilot/secrets/key', undefined],
    ['env:OPENAI_KEY', expect.stringContaining('must start with HP_SECRET_')],
    ['env:HP_SECRET_lower', expect.stringContaining('env:HP_SECRET_NAME')],
    ['file:relative', expect.stringContaining('file:/absolute/path')],
    ['sk-raw-key', expect.stringContaining('env:HP_SECRET_NAME')],
  ])('checkSecretRef(%j)', (ref, want) => {
    expect(checkSecretRef(ref)).toEqual(want)
  })

  it('validates and derives short names', () => {
    expect(nameError('team-slack')).toBeUndefined()
    expect(nameError('Bad Name')).toMatch(/lowercase/)
    expect(slugify('Claude Code on a laptop')).toBe('claude-code-on-a-laptop')
    expect(slugify('  Équipe — Dev!  ')).toBe('equipe-dev')
  })
})

describe('KeyValueEditor', () => {
  it('shows the outside value again after a discard', async () => {
    function Form() {
      const [saved] = useState<Record<string, string>>({ team: 'web' })
      const [draft, setDraft] = useState(saved)
      return (
        <>
          <KeyValueEditor label="References" value={draft} onChange={(v) => setDraft(v)} />
          <button type="button" onClick={() => setDraft({ ...saved })}>Discard</button>
        </>
      )
    }
    render(<Form />)
    const box = screen.getByLabelText('References')
    await userEvent.clear(box)
    await userEvent.type(box, 'typo=1')
    expect(box).toHaveValue('typo=1')
    await userEvent.click(screen.getByRole('button', { name: 'Discard' }))
    expect(box).toHaveValue('team=web')
  })
})

describe('new password', () => {
  it.each([
    ['', '', undefined, {}],
    ['short', '', undefined, { password: 'Use at least 12 characters.' }],
    ['correct horse battery', 'correct horse', undefined, { confirm: 'The passwords do not match.' }],
    ['same-as-before-123', 'same-as-before-123', 'same-as-before-123', { password: 'Choose a password different from the current one.' }],
    ['a fine new passphrase', 'a fine new passphrase', 'old password 1', {}],
  ])('%j / %j', async (pw, confirm, current, want) => {
    const { newPasswordProblems } = await import('./form')
    expect(newPasswordProblems(pw, confirm, current)).toEqual(want)
  })
})
