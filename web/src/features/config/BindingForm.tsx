import { type ReactNode, useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Checkbox as MCheckbox, Collapse, Divider, Group, Radio, SimpleGrid, Stack, Switch, Text, Title } from '@mantine/core'
import { Bot, Code2, Laptop, TerminalSquare, Wand2 } from 'lucide-react'
import { Button } from '@/components/ui'
import { Dialog, MutationError, NameField, NumberField, SelectField, TextField, nameError, slugify, useFieldErrors } from '@/components/form'
import { KeyValueEditor, LinesEditor } from '@/components/KeyValueEditor'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { ApiError } from '@/lib/api/client'
import { principalsQuery, resourceListQuery, usersQuery } from '@/lib/api/queries'
import { useSaveResource } from '@/lib/api/resources'
import type { AliasRes, BindingRes, BindingSpec, PlanRes, ProjectRes, ProviderRes } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { tools, type ToolValue } from '@/features/harness/tools'
import c from './config.module.css'

export const emptyBinding = (): BindingSpec => ({
  display_name: '',
  owner_kind: 'none',
  owner_id: '',
  plan_id: '',
  project_id: '',
  allowed_project_ids: [],
  expected_harness: '',
  allowed_models: [],
  limits: { requests_per_minute: 0, max_concurrency: 0, max_request_bytes: 0 },
  trust: { session_hints: true, project_hints: false, intent_hints: false },
  external_refs: {},
  enabled: true,
})

const toolIcons: Record<ToolValue, ReactNode> = {
  claude_code: <TerminalSquare size={18} />,
  codex: <Code2 size={18} />,
  opencode: <Laptop size={18} />,
  pi: <Wand2 size={18} />,
  generic: <Bot size={18} />,
}

/** Tool picker: a real radio group of cards (arrow keys move the choice). */
function ToolCards({ value, onChange }: { value: string; onChange: (v: ToolValue) => void }) {
  return (
    <Radio.Group label="Tool" value={value} onChange={(v) => onChange(v as ToolValue)}>
      <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="xs" mt={6}>
        {tools.map((t) => (
          <Radio.Card key={t.value} value={t.value} className={c.toolCard} data-selected={value === t.value || undefined}>
            <Group gap="sm" wrap="nowrap" align="flex-start">
              <Radio.Indicator mt={2} />
              <span className={c.toolIcon}>{toolIcons[t.value]}</span>
              <Stack gap={0} miw={0}>
                <Text size="sm" fw={600}>
                  {t.label}
                </Text>
                <Text size="xs" c="dimmed">
                  {t.hint}
                </Text>
              </Stack>
            </Group>
          </Radio.Card>
        ))}
      </SimpleGrid>
    </Radio.Group>
  )
}

function planOptions(plans: PlanRes[]) {
  return plans.map((p) => ({ value: p.id, label: p.spec.display_name ? `${p.spec.display_name} (${p.name})` : p.name }))
}

/** True when the error says the name is already taken. */
function nameTaken(err: unknown): boolean {
  return err instanceof ApiError && err.status === 409
}

/** "Add client": name, tool and routing plan; then its first access token. */
export function AddClientDialog({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: (b: BindingRes) => void }) {
  const plans = useQuery(resourceListQuery<PlanRes>('routing-plans'))
  const planItems = plans.data?.items ?? []
  const [display, setDisplay] = useState('')
  const [slug, setSlug] = useState('')
  const [slugEdited, setSlugEdited] = useState(false)
  const [tool, setTool] = useState<ToolValue>('claude_code')
  const [planID, setPlanID] = useState('')
  const plan = planID || planItems[0]?.id || ''
  const save = useSaveResource<BindingSpec, BindingRes>('client-bindings')
  const fe = useFieldErrors(save.error)
  const name = slugEdited ? slug : slugify(display)
  const nameErr = fe.name ?? (nameTaken(save.error) ? `A client called ${name} already exists. Choose another short name.` : undefined)
  const reason = !display.trim() && !name ? 'Give the client a name.' : !name ? 'Enter a short name.' : nameError(name) ? 'Fix the short name.' : !plan ? 'Choose a routing plan.' : ''
  return (
    <Dialog
      open={open}
      title="Add client"
      onClose={onClose}
      dirty={!!display || slugEdited}
      footer={
        <>
          {reason && (
            <Text size="xs" c="dimmed" mr="auto">
              {reason}
            </Text>
          )}
          <Button onClick={onClose}>Cancel</Button>
          <Button
            variant="primary"
            busy={save.isPending}
            disabled={!!reason}
            onClick={() =>
              save.mutate({ body: { name, spec: { ...emptyBinding(), display_name: display.trim(), expected_harness: tool, plan_id: plan } } }, { onSuccess: onCreated })
            }
          >
            Create client and token
          </Button>
        </>
      }
    >
      <TextField label="Name" value={display} onChange={setDisplay} placeholder="e.g. Laptop — Claude Code" autoFocus maxLength={120} error={fe.display_name} />
      <NameField
        value={name}
        onChange={(v) => {
          // An emptied short name follows the Name again.
          setSlugEdited(v !== '')
          setSlug(v)
        }}
        error={nameErr}
      />
      <ToolCards value={tool} onChange={setTool} />
      <SelectField label="Routing plan" value={plan} onChange={setPlanID} options={planOptions(planItems)} placeholder={plans.isLoading ? 'Loading…' : 'Choose a plan'}
        hint="Decides which accounts and models serve this client." error={fe.plan_id} />
      <MutationError error={nameTaken(save.error) ? null : save.error} inlineFields={['name', 'display_name', 'plan_id']} />
    </Dialog>
  )
}

/** A settings section: title and one line of help on the left, fields on the right. */
function SettingsSection({ title, help, children, action }: { title: string; help?: ReactNode; children: ReactNode; action?: ReactNode }) {
  return (
    <div className={c.settingsSection}>
      <div>
        <Title order={3}>{title}</Title>
        {help && (
          <Text size="xs" c="dimmed" mt={4}>
            {help}
          </Text>
        )}
        {action && <div style={{ marginTop: 8 }}>{action}</div>}
      </div>
      <Stack gap="md">{children}</Stack>
    </div>
  )
}

/** Model names a client could ask for: aliases and provider model ids. */
function useModelSuggestions(): string[] {
  const aliases = useQuery(resourceListQuery<AliasRes>('model-aliases'))
  const providers = useQuery(resourceListQuery<ProviderRes>('providers'))
  return useMemo(() => {
    const out = new Set<string>()
    for (const a of aliases.data?.items ?? []) out.add(a.name)
    for (const p of providers.data?.items ?? []) for (const m of p.spec.models ?? []) out.add(m.id)
    return [...out].sort()
  }, [aliases.data, providers.data])
}

/** Client settings, saved together. Reports unsaved changes to the page. */
export function BindingForm({ binding, onSaved, onDirtyChange }: { binding: BindingRes; onSaved: (b: BindingRes) => void; onDirtyChange?: (dirty: boolean) => void }) {
  const plans = useQuery(resourceListQuery<PlanRes>('routing-plans'))
  const projects = useQuery(resourceListQuery<ProjectRes>('projects'))
  const canUsers = useCan('users.manage')
  const users = useQuery({ ...usersQuery, enabled: canUsers })
  const principals = useQuery({ ...principalsQuery, enabled: canUsers })
  const canWrite = useCan('config.write')
  const models = useModelSuggestions()
  const [s, setS] = useState<BindingSpec>(() => structuredClone(binding.spec))
  const [refsValid, setRefsValid] = useState(true)
  const [resetKey, setResetKey] = useState(0)
  const [showAdvanced, setShowAdvanced] = useState(false)
  const save = useSaveResource<BindingSpec, BindingRes>('client-bindings')
  const fe = useFieldErrors(save.error)
  const set = (p: Partial<BindingSpec>) => setS({ ...s, ...p })
  const projectItems = projects.data?.items ?? []
  const dirty = JSON.stringify(s) !== JSON.stringify(binding.spec) || !refsValid
  useEffect(() => onDirtyChange?.(dirty), [dirty, onDirtyChange])

  const ownerOptions =
    s.owner_kind === 'user'
      ? (users.data?.items ?? []).map((u) => ({ value: u.id, label: u.display_name ? `${u.display_name} (${u.email})` : u.email }))
      : (principals.data?.items ?? []).map((p) => ({ value: p.id, label: p.name }))
  const ownerMissing = (s.owner_kind === 'user' || s.owner_kind === 'service') && !s.owner_id
  const unknownModels = models.length ? s.allowed_models.filter((m) => !models.includes(m)) : []
  const problem = !refsValid
    ? 'Fix the highlighted field.'
    : ownerMissing
      ? `Choose the ${s.owner_kind === 'user' ? 'person' : 'automation account'} who owns this client.`
      : !s.plan_id
        ? 'Choose a routing plan.'
        : ''
  const discard = () => {
    setS(structuredClone(binding.spec))
    setRefsValid(true)
    setResetKey((k) => k + 1)
  }

  return (
    <Stack gap="lg">
      <TerraformEditWarning r={binding} />
      <SettingsSection title="Basics">
        <TextField label="Name" value={s.display_name} onChange={(v) => set({ display_name: v })} maxLength={120} placeholder={binding.name}
          hint={`Leave empty to use the short name (${binding.name}).`} error={fe.display_name} />
        <SelectField label="Routing plan" value={s.plan_id} onChange={(v) => set({ plan_id: v })} options={planOptions(plans.data?.items ?? [])} error={fe.plan_id} />
        <SelectField label="Tool" value={s.expected_harness} onChange={(v) => set({ expected_harness: v as BindingSpec['expected_harness'] })}
          options={[{ value: '', label: 'Any tool' }, ...tools.map((t) => ({ value: t.value, label: t.label }))]}
          hint="Used for setup instructions and reports; a different tool still works." />
        <SelectField label="Default project" value={s.project_id} onChange={(v) => set({ project_id: v })}
          options={[{ value: '', label: 'None' }, ...projectItems.map((p) => ({ value: p.id, label: p.name }))]} hint="Usage is attributed to this project." />
      </SettingsSection>
      <Divider />
      <SettingsSection title="Limits" help="Empty means no limit.">
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md" verticalSpacing="md">
          <NumberField label="Requests per minute" unit="/min" placeholder="Unlimited" min={1} max={1_000_000} value={s.limits.requests_per_minute || null}
            onChange={(n) => set({ limits: { ...s.limits, requests_per_minute: n ?? 0 } })} error={fe.requests_per_minute} />
          <NumberField label="Requests at once" unit="at once" placeholder="Unlimited" min={1} max={10_000} value={s.limits.max_concurrency || null}
            onChange={(n) => set({ limits: { ...s.limits, max_concurrency: n ?? 0 } })} error={fe.max_concurrency} />
        </SimpleGrid>
        <LinesEditor key={`models-${resetKey}`} label="Allowed models" value={s.allowed_models} onChange={(v) => set({ allowed_models: v })} suggestions={models}
          placeholder="Any model its plan routes" error={fe.allowed_models}
          hint={
            unknownModels.length
              ? `Not a known model name: ${unknownModels.join(', ')}. Requests for it will be refused unless the plan maps it.`
              : 'Model names this client may ask for. Empty means anything its routing plan serves.'
          } />
      </SettingsSection>
      <Divider />
      <SettingsSection
        title="Advanced"
        help="Ownership, request size, project and session hints, and references."
        action={
          <Button size="small" variant="ghost" onClick={() => setShowAdvanced(!showAdvanced)} aria-expanded={showAdvanced}>
            {showAdvanced ? 'Hide' : 'Show'}
          </Button>
        }
      >
        {!showAdvanced ? (
          <Text size="sm" c="dimmed">
            {s.owner_kind === 'none' || !s.owner_kind ? 'No owner' : 'Has an owner'} · sessions {s.trust.session_hints ? 'grouped' : 'not grouped'} ·{' '}
            {Object.keys(s.external_refs ?? {}).length} references
          </Text>
        ) : (
          <>
            <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md" verticalSpacing="md">
              <SelectField label="Owner" value={s.owner_kind || 'none'} onChange={(v) => set({ owner_kind: v as BindingSpec['owner_kind'], owner_id: '' })}
                options={[{ value: 'none', label: 'No owner' }, { value: 'user', label: 'A person' }, { value: 'service', label: 'An automation account' }]} />
              {(s.owner_kind === 'user' || s.owner_kind === 'service') &&
                (canUsers ? (
                  <SelectField label={s.owner_kind === 'user' ? 'Person' : 'Automation account'} value={s.owner_id} onChange={(v) => set({ owner_id: v })}
                    options={ownerOptions} placeholder="Choose…" required error={fe.owner_id ?? (ownerMissing ? 'Required' : undefined)} />
                ) : (
                  <TextField label="Owner id" value={s.owner_id} onChange={(v) => set({ owner_id: v })} required error={fe.owner_id ?? (ownerMissing ? 'Required' : undefined)} />
                ))}
            </SimpleGrid>
            <NumberField label="Largest request" unit="bytes" placeholder="Server default" min={1024} max={1 << 30} value={s.limits.max_request_bytes || null}
              onChange={(n) => set({ limits: { ...s.limits, max_request_bytes: n ?? 0 } })} w={300} error={fe.max_request_bytes} />
            <Switch label="Group requests into sessions" description="Uses the tool’s session headers. Only affects grouping, never access."
              checked={s.trust.session_hints} onChange={(e) => set({ trust: { ...s.trust, session_hints: e.currentTarget.checked } })} />
            <Switch label="Let the tool choose the project" description="From the projects chosen below (X-HP-Project header)."
              checked={s.trust.project_hints} onChange={(e) => set({ trust: { ...s.trust, project_hints: e.currentTarget.checked } })} />
            <Collapse expanded={s.trust.project_hints}>
              {projectItems.length > 0 ? (
                <MCheckbox.Group label="Projects the tool may choose" value={s.allowed_project_ids} onChange={(v) => set({ allowed_project_ids: v })}>
                  <Group gap="md" mt={6}>
                    {projectItems.map((p) => (
                      <MCheckbox key={p.id} value={p.id} label={p.name} />
                    ))}
                  </Group>
                </MCheckbox.Group>
              ) : (
                <Text size="sm" c="dimmed">
                  There are no projects yet.
                </Text>
              )}
            </Collapse>
            <Switch label="Trust the purpose the tool declares" description="X-HP-Intent header; otherwise it is recorded as reported."
              checked={s.trust.intent_hints} onChange={(e) => set({ trust: { ...s.trust, intent_hints: e.currentTarget.checked } })} />
            <KeyValueEditor key={`refs-${resetKey}`} label="References" value={s.external_refs} onChange={(v, ok) => { set({ external_refs: v }); setRefsValid(ok) }}
              hint="Trusted references for reporting, one key=value per line, e.g. git.repo=example.test/acme/web." error={fe.external_refs} />
          </>
        )}
      </SettingsSection>
      <MutationError error={save.error} />
      {canWrite && (
        <div className={c.saveBar} data-dirty={dirty || undefined}>
          <Text size="sm" c={problem && dirty ? 'var(--warning)' : 'dimmed'}>
            {problem && dirty ? problem : dirty ? 'Unsaved changes' : 'All changes saved'}
          </Text>
          <Group gap="xs">
            {dirty && <Button onClick={discard}>Discard</Button>}
            <Button variant="primary" busy={save.isPending} disabled={!dirty || !!problem}
              onClick={() => save.mutate({ existing: binding, body: { name: binding.name, spec: s } }, { onSuccess: onSaved })}>
              Save client
            </Button>
          </Group>
        </div>
      )}
    </Stack>
  )
}
