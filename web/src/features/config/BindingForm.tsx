import { type ReactNode, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Divider, Group, SimpleGrid, Stack, Switch, Text, Title, UnstyledButton } from '@mantine/core'
import { Bot, Code2, Laptop, TerminalSquare, Wand2 } from 'lucide-react'
import { Button } from '@/components/ui'
import { Advanced, Checkbox, Dialog, MutationError, SelectField, TextField, formStyles as f } from '@/components/form'
import { KeyValueEditor, LinesEditor } from '@/components/KeyValueEditor'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { principalsQuery, resourceListQuery, usersQuery } from '@/lib/api/queries'
import { useSaveResource } from '@/lib/api/resources'
import type { BindingRes, BindingSpec, PlanRes, ProjectRes } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { slugify } from '@/features/clients/clients'
import { toggle } from './constants'
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

const tools: { value: BindingSpec['expected_harness']; label: string; hint: string; icon: ReactNode }[] = [
  { value: 'claude_code', label: 'Claude Code', hint: 'Anthropic’s coding agent', icon: <TerminalSquare size={18} /> },
  { value: 'codex', label: 'Codex', hint: 'OpenAI’s coding agent', icon: <Code2 size={18} /> },
  { value: 'opencode', label: 'OpenCode', hint: 'Open-source coding agent', icon: <Laptop size={18} /> },
  { value: 'pi', label: 'Pi', hint: 'Pi coding agent', icon: <Wand2 size={18} /> },
  { value: 'generic', label: 'Other API client', hint: 'Any OpenAI, Anthropic or Gemini SDK', icon: <Bot size={18} /> },
]

/** Tool picker as selectable cards. */
function ToolCards({ value, onChange }: { value: string; onChange: (v: BindingSpec['expected_harness']) => void }) {
  return (
    <div role="radiogroup" aria-label="Tool">
      <Text size="sm" fw={500} mb={6}>
        Tool
      </Text>
      <SimpleGrid cols={{ base: 1, xs: 2, sm: 3 }} spacing="xs">
        {tools.map((t) => (
          <UnstyledButton
            key={t.value}
            role="radio"
            aria-checked={value === t.value}
            className={c.toolCard}
            data-selected={value === t.value || undefined}
            onClick={() => onChange(t.value)}
          >
            <Group gap="sm" wrap="nowrap" align="flex-start">
              <span className={c.toolIcon}>{t.icon}</span>
              <Stack gap={0} miw={0}>
                <Text size="sm" fw={600}>
                  {t.label}
                </Text>
                <Text size="xs" c="dimmed">
                  {t.hint}
                </Text>
              </Stack>
            </Group>
          </UnstyledButton>
        ))}
      </SimpleGrid>
    </div>
  )
}

function planOptions(plans: PlanRes[]) {
  return plans.map((p) => ({ value: p.id, label: p.spec.display_name ? `${p.spec.display_name} (${p.name})` : p.name }))
}

/** "Add client": name, tool and routing plan; everything else is optional. */
export function AddClientDialog({ onClose, onCreated }: { onClose: () => void; onCreated: (b: BindingRes) => void }) {
  const plans = useQuery(resourceListQuery<PlanRes>('routing-plans'))
  const planItems = plans.data?.items ?? []
  const [display, setDisplay] = useState('')
  const [slug, setSlug] = useState('')
  const [slugEdited, setSlugEdited] = useState(false)
  const [tool, setTool] = useState<BindingSpec['expected_harness']>('claude_code')
  const [planID, setPlanID] = useState('')
  const plan = planID || planItems[0]?.id || ''
  const save = useSaveResource<BindingSpec, BindingRes>('client-bindings')
  const name = slugEdited ? slug : slugify(display)
  return (
    <Dialog open title="Add client" onClose={onClose}>
      <TextField label="Name" value={display} onChange={setDisplay} placeholder="e.g. Laptop — Claude Code" required autoFocus />
      <TextField
        label="Short name"
        value={name}
        onChange={(v) => {
          setSlugEdited(true)
          setSlug(v)
        }}
        hint="Used in URLs and the API. Lowercase; cannot be changed later."
      />
      <ToolCards value={tool} onChange={setTool} />
      <SelectField label="Routing plan" value={plan} onChange={setPlanID} options={planOptions(planItems)} placeholder={plans.isLoading ? 'Loading…' : 'Choose a plan'}
        hint="Decides which accounts and models serve this client." />
      <MutationError error={save.error} />
      <div className={f.actions}>
        <Button onClick={onClose}>Cancel</Button>
        <Button
          variant="primary"
          busy={save.isPending}
          disabled={!name || !plan}
          onClick={() =>
            save.mutate({ body: { name, spec: { ...emptyBinding(), display_name: display.trim(), expected_harness: tool, plan_id: plan } } }, { onSuccess: onCreated })
          }
        >
          Create client and token
        </Button>
      </div>
    </Dialog>
  )
}

/** A settings section: title and one line of help on the left, fields on the right. */
function SettingsSection({ title, help, children }: { title: string; help?: string; children: ReactNode }) {
  return (
    <div className={c.settingsSection}>
      <div>
        <Title order={3}>{title}</Title>
        {help && (
          <Text size="xs" c="dimmed" mt={4}>
            {help}
          </Text>
        )}
      </div>
      <Stack gap="md">{children}</Stack>
    </div>
  )
}

/** Numbers where empty means "no limit" (sent as 0). */
function LimitField({ label, value, onChange, suffix }: { label: string; value: number; onChange: (n: number) => void; suffix: string }) {
  return (
    <TextField label={label} type="number" suffix={suffix} placeholder="Unlimited" value={value ? String(value) : ''}
      onChange={(v) => onChange(Math.max(0, Math.round(Number(v) || 0)))} />
  )
}

/** Client settings, saved together. */
export function BindingForm({ binding, onSaved }: { binding: BindingRes; onSaved: (b: BindingRes) => void }) {
  const plans = useQuery(resourceListQuery<PlanRes>('routing-plans'))
  const projects = useQuery(resourceListQuery<ProjectRes>('projects'))
  const canUsers = useCan('users.manage')
  const users = useQuery({ ...usersQuery, enabled: canUsers })
  const principals = useQuery({ ...principalsQuery, enabled: canUsers })
  const canWrite = useCan('config.write')
  const [s, setS] = useState<BindingSpec>(() => structuredClone(binding.spec))
  const [refsValid, setRefsValid] = useState(true)
  const save = useSaveResource<BindingSpec, BindingRes>('client-bindings')
  const set = (p: Partial<BindingSpec>) => setS({ ...s, ...p })
  const projectItems = projects.data?.items ?? []
  const dirty = JSON.stringify(s) !== JSON.stringify(binding.spec)

  const ownerOptions =
    s.owner_kind === 'user'
      ? (users.data?.items ?? []).map((u) => ({ value: u.id, label: u.email }))
      : (principals.data?.items ?? []).map((p) => ({ value: p.id, label: p.name }))

  return (
    <Stack gap="lg">
      <TerraformEditWarning r={binding} />
      <SettingsSection title="Basics">
        <TextField label="Name" value={s.display_name} onChange={(v) => set({ display_name: v })} maxLength={120} placeholder={binding.name} />
        <SelectField label="Routing plan" value={s.plan_id} onChange={(v) => set({ plan_id: v })} options={planOptions(plans.data?.items ?? [])} />
        <SelectField label="Tool" value={s.expected_harness} onChange={(v) => set({ expected_harness: v as BindingSpec['expected_harness'] })}
          options={[{ value: '', label: 'Any tool' }, ...tools.map((t) => ({ value: t.value, label: t.label }))]}
          hint="Informational: a different tool is recorded, never blocked." />
        <SelectField label="Default project" value={s.project_id} onChange={(v) => set({ project_id: v })}
          options={[{ value: '', label: 'None' }, ...projectItems.map((p) => ({ value: p.id, label: p.name }))]} />
      </SettingsSection>
      <Divider />
      <SettingsSection title="Limits" help="Leave empty for no limit.">
        <div className={f.grid}>
          <LimitField label="Requests per minute" suffix="/min" value={s.limits.requests_per_minute} onChange={(n) => set({ limits: { ...s.limits, requests_per_minute: n } })} />
          <LimitField label="Requests at once" suffix="max" value={s.limits.max_concurrency} onChange={(n) => set({ limits: { ...s.limits, max_concurrency: n } })} />
        </div>
        <LinesEditor label="Allowed models" value={s.allowed_models} onChange={(v) => set({ allowed_models: v })}
          hint="Model names this client may ask for, one per line. Empty means anything its plan routes." />
      </SettingsSection>
      <Divider />
      <SettingsSection title="Advanced" help="Ownership, request size, project and session hints, and references.">
        <Advanced label="Show advanced settings">
          <div className={f.grid}>
            <SelectField label="Owner" value={s.owner_kind || 'none'} onChange={(v) => set({ owner_kind: v as BindingSpec['owner_kind'], owner_id: '' })}
              options={[{ value: 'none', label: 'No owner' }, { value: 'user', label: 'A person' }, { value: 'service', label: 'An automation account' }]} />
            {(s.owner_kind === 'user' || s.owner_kind === 'service') &&
              (canUsers ? (
                <SelectField label={s.owner_kind === 'user' ? 'Person' : 'Automation account'} value={s.owner_id} onChange={(v) => set({ owner_id: v })}
                  options={[{ value: '', label: 'Select…' }, ...ownerOptions]} />
              ) : (
                <TextField label="Owner id" value={s.owner_id} onChange={(v) => set({ owner_id: v })} />
              ))}
            <TextField label="Largest request" type="number" suffix="bytes" placeholder="Server default" value={s.limits.max_request_bytes ? String(s.limits.max_request_bytes) : ''}
              onChange={(v) => set({ limits: { ...s.limits, max_request_bytes: Math.max(0, Math.round(Number(v) || 0)) } })} />
          </div>
          <Switch label="Group requests into sessions" description="Uses the tool’s session headers. Only affects grouping, never access."
            checked={s.trust.session_hints} onChange={(e) => set({ trust: { ...s.trust, session_hints: e.currentTarget.checked } })} />
          <Switch label="Let the tool choose the project" description="From the allowed projects below (X-HP-Project header)."
            checked={s.trust.project_hints} onChange={(e) => set({ trust: { ...s.trust, project_hints: e.currentTarget.checked } })} />
          <Switch label="Trust the purpose the tool declares" description="X-HP-Intent header; otherwise it is recorded as reported."
            checked={s.trust.intent_hints} onChange={(e) => set({ trust: { ...s.trust, intent_hints: e.currentTarget.checked } })} />
          {projectItems.length > 0 && (
            <fieldset className={c.checks}>
              <legend className={c.legend}>Projects the tool may choose</legend>
              {projectItems.map((p) => (
                <Checkbox key={p.id} label={p.name} checked={s.allowed_project_ids.includes(p.id)}
                  onChange={(on) => set({ allowed_project_ids: toggle(s.allowed_project_ids, p.id, on) })} />
              ))}
            </fieldset>
          )}
          <KeyValueEditor label="References" value={s.external_refs} onChange={(v, ok) => { set({ external_refs: v }); setRefsValid(ok) }}
            hint="Trusted references for reporting, one key=value per line, e.g. git.repo=example.test/acme/web." />
        </Advanced>
      </SettingsSection>
      <MutationError error={save.error} />
      {canWrite && (
        <div className={c.saveBar} data-dirty={dirty || undefined}>
          <Text size="sm" c="dimmed">
            {dirty ? 'Unsaved changes' : 'All changes saved'}
          </Text>
          <Group gap="xs">
            {dirty && <Button onClick={() => setS(structuredClone(binding.spec))}>Discard</Button>}
            <Button variant="primary" busy={save.isPending} disabled={!dirty || !s.plan_id || !refsValid}
              onClick={() => save.mutate({ existing: binding, body: { name: binding.name, spec: s } }, { onSuccess: onSaved })}>
              Save client
            </Button>
          </Group>
        </div>
      )}
    </Stack>
  )
}
