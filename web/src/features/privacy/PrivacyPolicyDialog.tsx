import { Anchor, Group, MultiSelect, Radio, Select, Stack, Table, Text, Title } from '@mantine/core'
import { useMemo, useState } from 'react'
import { AlertTriangle, Plus, Trash2 } from 'lucide-react'
import { Banner, Button } from '@/components/ui'
import {
  Checkbox, Dialog, DialogActions, MutationError, NameField, NumberField, SelectField, TextAreaField, TextField, formStyles as f, nameError, useFieldErrors,
} from '@/components/form'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { useSaveResource } from '@/lib/api/resources'
import type { PrivacyAllowRule, PrivacyPolicyRes, PrivacyPolicySpec, PrivacyRule } from '@/lib/api/types'
import { toggle } from '@/features/config/constants'
import {
  allDetectors, credentialDetectors, defaultPrivacySpec, type DetectorInfo, type Enforcement, enforcementOf, enforcementOptions, enforcementSpec,
  heuristicDetectors, MIN_SECRET_VALUE, personalDetectors, secretLineCount, shortSecretLines, splitList,
} from './privacy'
import { TryPolicy } from './Sandbox'
import p from './privacy.module.css'

type SecretMode = 'keep' | 'stored' | 'reference' | 'none'

const actionOptions = [
  { value: 'mask', label: 'Mask' },
  { value: 'block', label: 'Block' },
  { value: 'warn', label: 'Warn' },
]

/** One table per detector group, so the action column lines up. */
function DetectorTable({ title, items, selected, onSelected, actions, onActions, defaultAction, bulk }: {
  title: string
  items: DetectorInfo[]
  selected: string[]
  onSelected: (v: string[]) => void
  actions: Record<string, string>
  onActions: (v: Record<string, string>) => void
  defaultAction: 'mask' | 'warn'
  bulk?: boolean
}) {
  const ids = items.map((d) => d.id)
  return (
    <Stack gap={6}>
      <Group justify="space-between" gap="xs">
        <Title order={4}>{title}</Title>
        {bulk && (
          <Group gap="xs">
            <Anchor component="button" type="button" size="sm" onClick={() => onSelected([...new Set([...selected, ...ids])])} disabled={ids.every((id) => selected.includes(id))}>
              Select all
            </Anchor>
            <Anchor component="button" type="button" size="sm" onClick={() => onSelected(selected.filter((x) => !ids.includes(x)))} disabled={!ids.some((id) => selected.includes(id))}>
              Select none
            </Anchor>
          </Group>
        )}
      </Group>
      <Table className={p.detectorTable} verticalSpacing={4} aria-label={title}>
        <Table.Tbody>
          {items.map((d) => {
            const on = selected.includes(d.id)
            return (
              <Table.Tr key={d.id}>
                <Table.Td w="40%">
                  <Checkbox label={d.label} checked={on} onChange={(v) => onSelected(toggle(selected, d.id, v))} />
                </Table.Td>
                <Table.Td visibleFrom="sm">
                  <span className={p.detectorHint}>{d.hint}</span>
                </Table.Td>
                <Table.Td w={120}>
                  <Select
                    size="xs"
                    w={110}
                    aria-label={`Action for ${d.label}`}
                    value={actions[d.id] ?? defaultAction}
                    allowDeselect={false}
                    disabled={!on}
                    style={{ visibility: on ? undefined : 'hidden' }}
                    onChange={(v) => {
                      if (!v) return
                      const next = { ...actions }
                      if (v === defaultAction) delete next[d.id]
                      else next[d.id] = v
                      onActions(next)
                    }}
                    data={actionOptions}
                  />
                </Table.Td>
              </Table.Tr>
            )
          })}
        </Table.Tbody>
      </Table>
    </Stack>
  )
}

/** Create or edit a masking policy. */
export function PrivacyPolicyDialog({ policy, onClose }: { policy: PrivacyPolicyRes | null; onClose: () => void }) {
  const init: PrivacyPolicySpec = policy?.spec ?? defaultPrivacySpec()
  const [name, setName] = useState(policy?.name ?? '')
  const [description, setDescription] = useState(init.description ?? '')
  const [enforcement, setEnforcement] = useState<Enforcement>(enforcementOf(init))
  const [detectors, setDetectors] = useState<string[]>(init.detectors ?? [])
  const [personal, setPersonal] = useState<string[]>(init.personal_data ?? [])
  const [supplementary, setSupplementary] = useState<string[]>(init.supplementary ?? [])
  const [actions, setActions] = useState<Record<string, string>>(init.detector_actions ?? {})
  const [rules, setRules] = useState<PrivacyRule[]>(init.rules ?? [])
  const [allow, setAllow] = useState<PrivacyAllowRule[]>(init.allow ?? [])
  const [restore, setRestore] = useState(init.restore_in_responses ?? false)
  const tar = init.tool_arg_restore ?? { enabled: false }
  const [tarOn, setTarOn] = useState(tar.enabled)
  const [tarTools, setTarTools] = useState((tar.tools ?? []).join(', '))
  const [tarFields, setTarFields] = useState((tar.fields ?? []).join(', '))
  const [tarHosts, setTarHosts] = useState((tar.allowed_hosts ?? []).join(', '))
  const stored = policy?.secrets?.secret_values?.source === 'stored'
  const [secretMode, setSecretMode] = useState<SecretMode>(init.secret_values_ref ? 'reference' : stored ? 'keep' : 'none')
  const [secretValues, setSecretValues] = useState('')
  const [secretRef, setSecretRef] = useState(init.secret_values_ref ?? '')
  const save = useSaveResource<PrivacyPolicySpec>('privacy-policies')
  const errs = useFieldErrors(save.error)
  const err = (loc: string) => errs[`body.spec.${loc}`]

  const buildSpec = (): PrivacyPolicySpec => ({
    description: description.trim(),
    ...enforcementSpec(enforcement),
    detectors,
    personal_data: personal,
    supplementary,
    detector_actions: Object.fromEntries(Object.entries(actions).filter(([k]) => detectors.includes(k) || personal.includes(k) || supplementary.includes(k))),
    rules: rules.map((r) => ({ ...r, name: r.name.trim(), group: r.group ?? 0 })),
    allow: allow.map((a) => ({ ...a, name: a.name.trim() })),
    restore_in_responses: restore,
    tool_arg_restore: { enabled: tarOn, tools: splitList(tarTools), fields: splitList(tarFields), allowed_hosts: splitList(tarHosts) },
    secret_values_ref: secretMode === 'reference' ? secretRef.trim() : '',
  })
  const initial = useMemo(() => JSON.stringify(policy?.spec ?? defaultPrivacySpec()), [policy])
  const dirty = name !== (policy?.name ?? '') || JSON.stringify(buildSpec()) !== initial || secretValues !== '' || (stored && secretMode !== 'keep')

  const short = secretMode === 'stored' ? shortSecretLines(secretValues) : []
  const valueCount = secretLineCount(secretValues)
  const secretError =
    errs['body.secrets.secret_values'] ??
    (short.length === 0
      ? undefined
      : `${short.length === 1 ? `Line ${short[0]} is` : `Lines ${short.join(', ')} are`} shorter than ${MIN_SECRET_VALUE} characters and would not be masked.`)
  const configuredOn = detectors.includes('configured_secrets')
  const nameProblem = policy ? undefined : name ? nameError(name) : undefined

  const submit = () => {
    let secrets: Record<string, string> | undefined
    if (secretMode === 'stored' && secretValues.trim()) secrets = { secret_values: secretValues }
    else if (secretMode !== 'keep' && secretMode !== 'stored' && stored) secrets = { secret_values: '' }
    save.mutate({ existing: policy ?? undefined, body: { name, spec: buildSpec(), secrets } }, { onSuccess: onClose })
  }

  const setRule = (i: number, patch: Partial<PrivacyRule>) => setRules(rules.map((r, j) => (j === i ? { ...r, ...patch } : r)))
  const setAllowRule = (i: number, patch: Partial<PrivacyAllowRule>) => setAllow(allow.map((r, j) => (j === i ? { ...r, ...patch } : r)))
  // Allow rules can be limited to built-in detectors or to this policy's own rules.
  const allowTargets = [
    ...allDetectors.map((d) => ({ value: d.id, label: d.label })),
    ...rules.filter((r) => r.name.trim()).map((r) => ({ value: r.name.trim(), label: `Rule ${r.name.trim()}` })),
  ]
  const blocked =
    !name ||
    !!nameProblem ||
    (tarOn && splitList(tarTools).length === 0) ||
    (secretMode === 'reference' && !secretRef.trim()) ||
    short.length > 0
  const choice = enforcementOptions.find((o) => o.value === enforcement)

  return (
    <Dialog
      open
      wide
      title={policy ? `Edit masking policy ${policy.name}` : 'Add masking policy'}
      onClose={onClose}
      dirty={dirty}
      footer={
        <DialogActions>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" busy={save.isPending} disabled={blocked} onClick={submit}>
            {policy ? 'Save policy' : 'Create policy'}
          </Button>
        </DialogActions>
      }
    >
      <Stack gap="lg">
        <TerraformEditWarning r={policy} />
        <div className={f.grid}>
          <NameField value={name} onChange={setName} locked={!!policy} autoFocus={!policy} error={errs['body.name'] ?? nameProblem} />
          <TextField label="Description" value={description} onChange={setDescription} maxLength={1000} placeholder="Optional" />
        </div>

        <Radio.Group label={<Title order={4}>When masking can’t run</Title>} description="For example on a byte-for-byte passthrough route, or when stored values can’t be read."
          value={enforcement} onChange={(v) => setEnforcement(v as Enforcement)}>
          <Stack gap="xs" mt="xs">
            {enforcementOptions.map((o) => (
              <Radio key={o.value} value={o.value} label={o.label} description={o.hint} />
            ))}
          </Stack>
        </Radio.Group>
        {enforcement !== 'block' && (
          <Banner tone="warning" icon={AlertTriangle}>
            With “{choice?.label}”, secrets can reach providers unmasked whenever masking can’t run.
          </Banner>
        )}

        <DetectorTable title="Secrets and credentials" items={credentialDetectors} selected={detectors} onSelected={setDetectors} actions={actions} onActions={setActions}
          defaultAction="mask" bulk />
        {detectors.length === 0 && <Banner tone="info">With no credential type selected, every one of them is used.</Banner>}
        {err('detectors') && <Banner tone="error">{err('detectors')}</Banner>}
        <DetectorTable title="Personal data (optional)" items={personalDetectors} selected={personal} onSelected={setPersonal} actions={actions} onActions={setActions}
          defaultAction="mask" bulk />
        <DetectorTable title="Heuristics" items={heuristicDetectors} selected={supplementary} onSelected={setSupplementary} actions={actions} onActions={setActions}
          defaultAction="warn" />

        <Stack gap="sm">
          <Title order={4}>Configured secret values</Title>
          <Text size="sm" c="dimmed">
            Exact values to mask, such as internal passwords no detector knows about. One per line, at least {MIN_SECRET_VALUE} characters.
            {!configuredOn && ' Turn on “Configured secret values” above to use them.'}
          </Text>
          <SelectField<SecretMode>
            label="Values"
            value={secretMode}
            onChange={setSecretMode}
            options={[
              ...(stored ? [{ value: 'keep' as SecretMode, label: `Keep the stored values (${policy?.secrets?.secret_values?.fingerprint ?? 'set'})` }] : []),
              { value: 'stored', label: stored ? 'Replace the stored values' : 'Store values (encrypted)' },
              { value: 'reference', label: 'Read from an environment variable or file' },
              { value: 'none', label: stored ? 'Remove the stored values' : 'None' },
            ]}
          />
          {secretMode === 'stored' && (
            <TextAreaField label="Values (one per line)" value={secretValues} onChange={setSecretValues} mono minRows={3} autoComplete="off" spellCheck={false}
              error={secretError} hint={valueCount > 0 ? `${valueCount} ${valueCount === 1 ? 'value' : 'values'} will be stored. They are never shown again.` : 'Never shown again after saving.'} />
          )}
          {secretMode === 'reference' && (
            <TextField label="Reference" value={secretRef} onChange={setSecretRef} placeholder="e.g. file:/run/secrets/mask-values" error={err('secret_values_ref')} />
          )}
        </Stack>

        <Stack gap="sm">
          <Title order={4}>Custom rules</Title>
          <Text size="sm" c="dimmed">
            Your own patterns (RE2 regular expressions), for example internal host names.
          </Text>
          {rules.map((r, i) => (
            <div key={i} className={p.ruleCard}>
              <Group justify="space-between" gap="xs">
                <strong className="mono">{r.name || `Rule ${i + 1}`}</strong>
                <Button size="small" variant="ghost" icon={Trash2} aria-label={`Remove rule ${r.name || i + 1}`} onClick={() => setRules(rules.filter((_, j) => j !== i))} />
              </Group>
              <div className={f.grid}>
                <TextField label="Name" value={r.name} onChange={(v) => setRule(i, { name: v })} placeholder="e.g. internal_host" error={err(`rules[${i}].name`)}
                  hint="Lowercase; shown in summaries." />
                <TextField label="Pattern" value={r.pattern} onChange={(v) => setRule(i, { pattern: v })} placeholder="e.g. prod-db-[0-9]+" error={err(`rules[${i}].pattern`)} />
                <NumberField label="Part to mask" value={r.group ?? 0} min={0} max={9} onChange={(v) => setRule(i, { group: v ?? 0 })} error={err(`rules[${i}].group`)}
                  hint="0 masks the whole match; 1–9 a capture group." />
                <SelectField<PrivacyRule['action']> label="Action" value={r.action} onChange={(v) => setRule(i, { action: v })}
                  options={[{ value: 'mask', label: 'Mask' }, { value: 'block', label: 'Block the request' }, { value: 'warn', label: 'Warn only' }]} />
              </div>
            </div>
          ))}
          <div>
            <Button size="small" icon={Plus} onClick={() => setRules([...rules, { name: '', pattern: '', group: 0, action: 'mask' }])}>
              Add rule
            </Button>
          </div>
          {err('rules') && <Banner tone="error">{err('rules')}</Banner>}
        </Stack>

        <Stack gap="sm">
          <Title order={4}>Exceptions</Title>
          <Text size="sm" c="dimmed">
            A detection whose whole text matches an exception is sent as it is. Exceptions never override a block.
          </Text>
          {allow.map((a, i) => (
            <div key={i} className={p.ruleCard}>
              <Group justify="space-between" gap="xs">
                <strong className="mono">{a.name || `Exception ${i + 1}`}</strong>
                <Button size="small" variant="ghost" icon={Trash2} aria-label={`Remove exception ${a.name || i + 1}`} onClick={() => setAllow(allow.filter((_, j) => j !== i))} />
              </Group>
              <div className={f.grid}>
                <TextField label="Name" value={a.name} onChange={(v) => setAllowRule(i, { name: v })} placeholder="e.g. test-keys" error={err(`allow[${i}].name`)} />
                <TextField label="Pattern (the whole detection must match)" value={a.pattern} onChange={(v) => setAllowRule(i, { pattern: v })}
                  placeholder="e.g. sk-ant-api03-TEST.*" error={err(`allow[${i}].pattern`)} />
                <MultiSelect label="Only for" placeholder={(a.detectors ?? []).length ? undefined : 'All detectors'} data={allowTargets} value={a.detectors ?? []}
                  onChange={(v) => setAllowRule(i, { detectors: v })} searchable clearable error={err(`allow[${i}].detectors`)} comboboxProps={{ withinPortal: true }} />
              </div>
            </div>
          ))}
          <div>
            <Button size="small" icon={Plus} onClick={() => setAllow([...allow, { name: '', pattern: '', detectors: [] }])}>
              Add exception
            </Button>
          </div>
        </Stack>

        <Stack gap="sm">
          <Title order={4}>Unmasking replies</Title>
          <Checkbox label="Put the original values back in text returned to the same client" checked={restore} onChange={setRestore}
            description="The client sees its own values when the model repeats a placeholder. Placeholders from other sessions are never restored." />
          <Checkbox label="Also put them back in tool-call arguments" checked={tarOn} onChange={setTarOn} description="Off by default. Limit it to specific tools." />
          {tarOn && (
            <>
              <Banner tone="warning" icon={AlertTriangle}>
                Restored tool arguments let the model pass your secrets to tools on the client — commands, file writes or HTTP calls. Values are never
                restored inside URLs to hosts you have not listed.
              </Banner>
              <div className={f.grid}>
                <TextField label="Tools allowed to receive originals" value={tarTools} onChange={setTarTools} placeholder="e.g. set_env, write_secret"
                  error={err('tool_arg_restore.tools') ?? (splitList(tarTools).length === 0 ? 'Name at least one tool.' : undefined)} hint="Comma separated." />
                <TextField label="Argument fields" value={tarFields} onChange={setTarFields} placeholder="Any top-level field" />
                <TextField label="Hosts allowed inside URLs" value={tarHosts} onChange={setTarHosts} placeholder="e.g. api.internal.example" />
              </div>
            </>
          )}
        </Stack>

        <TryPolicy spec={buildSpec} hasStoredValues={stored || secretMode === 'stored'} />
        <MutationError error={save.error} />
      </Stack>
    </Dialog>
  )
}
