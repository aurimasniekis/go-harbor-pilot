import { Select } from '@mantine/core'
import { useState } from 'react'
import { AlertTriangle, Plus, Trash2 } from 'lucide-react'
import { Banner, Button } from '@/components/ui'
import { Checkbox, Dialog, Field, MutationError, SelectField, TextField, formStyles as f, useFieldErrors } from '@/components/form'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { useSaveResource } from '@/lib/api/resources'
import type { PrivacyAllowRule, PrivacyPolicyRes, PrivacyPolicySpec, PrivacyRule } from '@/lib/api/types'
import { toggle } from '@/features/config/constants'
import c from '@/features/config/config.module.css'
import { credentialDetectors, defaultPrivacySpec, type DetectorInfo, heuristicDetectors, personalDetectors, splitList } from './privacy'
import p from './privacy.module.css'

type SecretMode = 'keep' | 'stored' | 'reference' | 'none'

function DetectorGroup({ legend, items, selected, onSelected, actions, onActions, defaultAction }: {
  legend: string
  items: DetectorInfo[]
  selected: string[]
  onSelected: (v: string[]) => void
  actions: Record<string, string>
  onActions: (v: Record<string, string>) => void
  defaultAction: 'mask' | 'warn'
}) {
  return (
    <fieldset className={c.checks} style={{ flexDirection: 'column' }}>
      <legend className={c.legend}>{legend}</legend>
      <div className={p.group}>
        {items.map((d) => {
          const on = selected.includes(d.id)
          return (
            <div key={d.id} className={p.detector}>
              <div className={p.detectorRow}>
                <Checkbox label={d.label} checked={on} onChange={(v) => onSelected(toggle(selected, d.id, v))} />
                {on && (
                  <Select
                    size="xs"
                    w={110}
                    aria-label={`Action for ${d.label}`}
                    value={actions[d.id] ?? defaultAction}
                    allowDeselect={false}
                    onChange={(v) => {
                      if (!v) return
                      const next = { ...actions }
                      if (v === defaultAction) delete next[d.id]
                      else next[d.id] = v
                      onActions(next)
                    }}
                    data={[
                      { value: 'mask', label: 'Mask' },
                      { value: 'block', label: 'Block' },
                      { value: 'warn', label: 'Warn' },
                    ]}
                  />
                )}
              </div>
              {d.hint && <span className={p.hint}>{d.hint}</span>}
            </div>
          )
        })}
      </div>
    </fieldset>
  )
}

/** Create or edit a privacy (outbound masking) policy. */
export function PrivacyPolicyDialog({ policy, onClose }: { policy: PrivacyPolicyRes | null; onClose: () => void }) {
  const init: PrivacyPolicySpec = policy?.spec ?? defaultPrivacySpec()
  const [name, setName] = useState(policy?.name ?? '')
  const [description, setDescription] = useState(init.description ?? '')
  const [required, setRequired] = useState(init.required)
  const [fallback, setFallback] = useState(init.allow_unmasked_fallback ?? false)
  const [detectors, setDetectors] = useState<string[]>(init.detectors ?? [])
  const [personal, setPersonal] = useState<string[]>(init.personal_data ?? [])
  const [supplementary, setSupplementary] = useState<string[]>(init.supplementary ?? [])
  const [actions, setActions] = useState<Record<string, string>>(init.detector_actions ?? {})
  const [rules, setRules] = useState<PrivacyRule[]>(init.rules ?? [])
  const [allow, setAllow] = useState<(PrivacyAllowRule & { detectorsText: string })[]>(
    (init.allow ?? []).map((a) => ({ ...a, detectorsText: (a.detectors ?? []).join(', ') })),
  )
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

  const submit = () => {
    const spec: PrivacyPolicySpec = {
      description: description.trim(),
      required,
      allow_unmasked_fallback: required && fallback,
      detectors,
      personal_data: personal,
      supplementary,
      detector_actions: Object.fromEntries(
        Object.entries(actions).filter(([k]) => detectors.includes(k) || personal.includes(k) || supplementary.includes(k)),
      ),
      rules: rules.map((r) => ({ ...r, name: r.name.trim(), group: r.group ?? 0 })),
      allow: allow.map(({ detectorsText, ...a }) => ({ ...a, name: a.name.trim(), detectors: splitList(detectorsText) })),
      restore_in_responses: restore,
      tool_arg_restore: { enabled: tarOn, tools: splitList(tarTools), fields: splitList(tarFields), allowed_hosts: splitList(tarHosts) },
      secret_values_ref: secretMode === 'reference' ? secretRef.trim() : '',
    }
    let secrets: Record<string, string> | undefined
    if (secretMode === 'stored' && secretValues.trim()) secrets = { secret_values: secretValues }
    else if (secretMode !== 'keep' && secretMode !== 'stored' && stored) secrets = { secret_values: '' }
    save.mutate({ existing: policy ?? undefined, body: { name, spec, secrets } }, { onSuccess: onClose })
  }

  const setRule = (i: number, patch: Partial<PrivacyRule>) => setRules(rules.map((r, j) => (j === i ? { ...r, ...patch } : r)))
  const setAllowRule = (i: number, patch: Partial<(typeof allow)[number]>) => setAllow(allow.map((r, j) => (j === i ? { ...r, ...patch } : r)))

  return (
    <Dialog open wide title={policy ? `Edit privacy policy ${policy.name}` : 'Add privacy policy'} onClose={onClose}>
      <TerraformEditWarning r={policy} />
      <div className={f.grid}>
        <TextField label="Name" value={name} onChange={setName} disabled={!!policy} required error={errs['body.name']} />
        <TextField label="Description" value={description} onChange={setDescription} maxLength={1000} />
      </div>

      <fieldset className={c.checks} style={{ flexDirection: 'column' }}>
        <legend className={c.legend}>Enforcement</legend>
        <Checkbox label="Masking is required" checked={required} onChange={setRequired} />
        <span className={p.hint}>When masking cannot run safely (for example on a byte-for-byte passthrough route) the request is blocked instead of being sent unmasked.</span>
        {required && (
          <>
            <Checkbox label="Availability first: dispatch unmasked when masking cannot run" checked={fallback} onChange={setFallback} />
            <span className={p.hint}>Pre-authorizes unmasked dispatch during incidents. Each such request is marked on its activity record.</span>
          </>
        )}
        {required && fallback && (
          <Banner tone="warning" icon={AlertTriangle}>
            With availability-first fallback, secrets can reach providers unmasked whenever masking cannot run.
          </Banner>
        )}
      </fieldset>

      <DetectorGroup legend="Credential detectors" items={credentialDetectors} selected={detectors} onSelected={setDetectors}
        actions={actions} onActions={setActions} defaultAction="mask" />
      {detectors.length === 0 && <Banner tone="info">With no credential detector selected the server enables all of them.</Banner>}
      {err('detectors') && <Banner tone="error">{err('detectors')}</Banner>}
      <DetectorGroup legend="Personal data (optional)" items={personalDetectors} selected={personal} onSelected={setPersonal}
        actions={actions} onActions={setActions} defaultAction="mask" />
      <DetectorGroup legend="Heuristics (supplementary)" items={heuristicDetectors} selected={supplementary} onSelected={setSupplementary}
        actions={actions} onActions={setActions} defaultAction="warn" />

      <fieldset className={c.checks} style={{ flexDirection: 'column' }}>
        <legend className={c.legend}>Custom rules (RE2 regular expressions)</legend>
        <div className={c.rows}>
          {rules.map((r, i) => (
            <div key={i} className={c.row}>
              <div className={c.rowHeader}>
                <strong className="mono">{r.name || `Rule ${i + 1}`}</strong>
                <Button size="small" variant="ghost" icon={Trash2} aria-label={`Remove rule ${r.name || i + 1}`} onClick={() => setRules(rules.filter((_, j) => j !== i))} />
              </div>
              <div className={f.grid}>
                <TextField label="Name" value={r.name} onChange={(v) => setRule(i, { name: v })} placeholder="internal_host" error={err(`rules[${i}].name`)}
                  hint="Lowercase identifier; shown in summaries." />
                <TextField label="Pattern" value={r.pattern} onChange={(v) => setRule(i, { pattern: v })} placeholder="prod-db-[0-9]+" error={err(`rules[${i}].pattern`)} />
                <TextField label="Capture group (0 = whole match)" type="number" value={String(r.group ?? 0)}
                  onChange={(v) => setRule(i, { group: Math.max(0, Math.min(9, Number(v) || 0)) })} error={err(`rules[${i}].group`)} />
                <SelectField<PrivacyRule['action']> label="Action" value={r.action} onChange={(v) => setRule(i, { action: v })}
                  options={[{ value: 'mask', label: 'Mask' }, { value: 'block', label: 'Block the request' }, { value: 'warn', label: 'Warn only' }]} />
              </div>
            </div>
          ))}
        </div>
        <div>
          <Button size="small" icon={Plus} onClick={() => setRules([...rules, { name: '', pattern: '', group: 0, action: 'mask' }])}>Add rule</Button>
        </div>
        {err('rules') && <Banner tone="error">{err('rules')}</Banner>}
      </fieldset>

      <fieldset className={c.checks} style={{ flexDirection: 'column' }}>
        <legend className={c.legend}>Allow rules (narrow exemptions)</legend>
        <span className="muted" style={{ fontSize: 'var(--text-xs)' }}>
          A detection whose whole text matches an allow pattern is not masked or warned about. Precedence: block, then allow, then mask, then warn —
          an allow rule never overrides a block.
        </span>
        <div className={c.rows}>
          {allow.map((a, i) => (
            <div key={i} className={c.row}>
              <div className={c.rowHeader}>
                <strong className="mono">{a.name || `Allow rule ${i + 1}`}</strong>
                <Button size="small" variant="ghost" icon={Trash2} aria-label={`Remove allow rule ${a.name || i + 1}`} onClick={() => setAllow(allow.filter((_, j) => j !== i))} />
              </div>
              <div className={f.grid}>
                <TextField label="Name" value={a.name} onChange={(v) => setAllowRule(i, { name: v })} error={err(`allow[${i}].name`)} />
                <TextField label="Pattern (whole detection must match)" value={a.pattern} onChange={(v) => setAllowRule(i, { pattern: v })}
                  placeholder="sk-ant-api03-TEST.*" error={err(`allow[${i}].pattern`)} />
                <TextField label="Only for detectors or rules (comma separated, empty = all)" value={a.detectorsText}
                  onChange={(v) => setAllowRule(i, { detectorsText: v })} placeholder="anthropic_key" error={err(`allow[${i}].detectors`)} />
              </div>
            </div>
          ))}
        </div>
        <div>
          <Button size="small" icon={Plus} onClick={() => setAllow([...allow, { name: '', pattern: '', detectors: [], detectorsText: '' }])}>Add allow rule</Button>
        </div>
      </fieldset>

      <fieldset className={c.checks} style={{ flexDirection: 'column' }}>
        <legend className={c.legend}>Restoration</legend>
        <Checkbox label="Restore placeholders in text returned to the same client" checked={restore} onChange={setRestore} />
        <span className={p.hint}>The client sees its own values again when the model repeats a placeholder. Placeholders from other sessions are never restored.</span>
        <Checkbox label="Restore originals into tool-call arguments (off by default)" checked={tarOn} onChange={setTarOn} />
        {tarOn && (
          <>
            <Banner tone="warning" icon={AlertTriangle}>
              Restored tool arguments let the model pass your secrets to tools on the client — commands, file writes or HTTP calls. Limit this
              to specific tools and argument fields. Values are never restored inside URLs to hosts you have not listed.
            </Banner>
            <div className={f.grid}>
              <TextField label="Tools allowed to receive originals" value={tarTools} onChange={setTarTools} placeholder="set_env, write_secret"
                error={err('tool_arg_restore.tools')} hint="Comma separated tool names. Required." />
              <TextField label="Argument fields (empty = any top-level field)" value={tarFields} onChange={setTarFields} placeholder="value" />
              <TextField label="Hosts allowed inside URLs" value={tarHosts} onChange={setTarHosts} placeholder="api.internal.example" />
            </div>
          </>
        )}
      </fieldset>

      <fieldset className={c.checks} style={{ flexDirection: 'column' }}>
        <legend className={c.legend}>Configured secret values (write-only)</legend>
        <span className="muted" style={{ fontSize: 'var(--text-xs)' }}>
          Exact values to mask, one per line (six characters or more). Used when the “Configured secret values” detector is on.
        </span>
        <SelectField<SecretMode>
          label="Secret values"
          value={secretMode}
          onChange={setSecretMode}
          options={[
            ...(stored ? [{ value: 'keep' as SecretMode, label: `Keep stored values (${policy?.secrets?.secret_values?.fingerprint ?? 'set'})` }] : []),
            { value: 'stored', label: stored ? 'Replace stored values' : 'Store values (encrypted)' },
            { value: 'reference', label: 'Reference (env/file)' },
            { value: 'none', label: stored ? 'Remove the stored values' : 'None' },
          ]}
        />
        {secretMode === 'stored' && (
          <Field label="Values (one per line)" hint="Never shown again after saving.">
            {(fp) => (
              <textarea {...fp} className={f.textarea} rows={4} autoComplete="off" spellCheck={false} value={secretValues}
                onChange={(e) => setSecretValues(e.target.value)} style={{ fontFamily: 'var(--font-mono)' }} />
            )}
          </Field>
        )}
        {secretMode === 'reference' && (
          <TextField label="Reference" value={secretRef} onChange={setSecretRef} placeholder="file:/run/secrets/mask-values" error={err('secret_values_ref')} />
        )}
      </fieldset>

      <MutationError error={save.error} />
      <div className={f.actions}>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} disabled={!name || (tarOn && splitList(tarTools).length === 0) || (secretMode === 'reference' && !secretRef.trim())}
          onClick={submit}>
          Save policy
        </Button>
      </div>
    </Dialog>
  )
}
