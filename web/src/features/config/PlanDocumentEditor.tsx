import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ArrowDown, ArrowUp, Plus, Trash2 } from 'lucide-react'
import { Banner, Button, Tabs } from '@/components/ui'
import { Checkbox, Dialog, MutationError, SelectField, TextField, formStyles as f } from '@/components/form'
import { resourceListQuery } from '@/lib/api/queries'
import type { AliasRes, FieldErrorDTO, ModelMapping, PlanDocument, PrivacyPolicyRes, ProviderRes, TracePolicyRes } from '@/lib/api/types'
import { Link } from '@tanstack/react-router'
import { traceModes } from '@/features/privacy/privacy'
import { EngineEditor } from '@/features/routing/EngineEditor'
import { CompatEditor } from '@/features/compat/CompatEditor'
import { isDefault } from '@/features/compat/compat'
import { fieldErrors } from '@/lib/api/client'
import { parseEngine, serializeEngine } from '@/features/routing/engine'
import { billingClasses, capabilities, classifications, fallbackSteps, toInt, toggle } from './constants'
import c from './config.module.css'

/** The default document for a plan's first draft. */
export function defaultDocument(): PlanDocument {
  return {
    schema_version: 1,
    model_mappings: [],
    default_alias_id: '',
    restrictions: { allowed_provider_ids: [], denied_provider_ids: [], allowed_billing: [], allowed_classifications: [], required_capabilities: [] },
    fallback: { steps: ['alternate_account', 'alternate_model', 'stop'], max_attempts: 3, allow_paid_fallback: false, wait_max_seconds: 0 },
  }
}

function Checks({ legend, options, value, onChange }: {
  legend: string
  options: readonly { value: string; label: string }[]
  value: string[]
  onChange: (v: string[]) => void
}) {
  return (
    <fieldset className={c.checks}>
      <legend className={c.legend}>{legend}</legend>
      {options.map((o) => (
        <Checkbox key={o.value} label={o.label} checked={value.includes(o.value)} onChange={(on) => onChange(toggle(value, o.value, on))} />
      ))}
    </fieldset>
  )
}

/** Structured editor for a routing plan document (draft versions only). */
export function PlanDocumentEditor({ title, initial, note: initialNote, busy, error, onSave, onClose, validationErrors = [] }: {
  title: string
  initial: PlanDocument
  note: string
  busy: boolean
  error: unknown
  onSave: (doc: PlanDocument, note: string) => void
  onClose: () => void
  /** Errors from this draft's last validation, shown next to what they refer to. */
  validationErrors?: FieldErrorDTO[]
}) {
  const aliases = useQuery(resourceListQuery<AliasRes>('model-aliases'))
  const providers = useQuery(resourceListQuery<ProviderRes>('providers'))
  const [doc, setDoc] = useState<PlanDocument>(() => structuredClone(initial))
  const [note, setNote] = useState(initialNote)
  const [engine, setEngine] = useState(() => parseEngine(initial.engine))
  const engineErrors = validationErrors.filter((e) => e.location.startsWith('body.document.engine'))
  const privacyErrors = validationErrors.filter((e) => e.location.startsWith('body.document.privacy'))
  const saveErrors = Object.entries(fieldErrors(error)).map(([location, message]) => ({ location, message }))
  const compatErrors = [...validationErrors, ...saveErrors].filter((e) => e.location.startsWith('body.document.compatibility'))
  const [tab, setTab] = useState<'routing' | 'rules' | 'privacy' | 'compat'>(
    engineErrors.length > 0 ? 'rules' : privacyErrors.length > 0 ? 'privacy' : compatErrors.length > 0 ? 'compat' : 'routing')
  const compatErrorKey = compatErrors.map((e) => e.location + e.message).join('|')
  useEffect(() => {
    if (compatErrorKey) setTab('compat')
  }, [compatErrorKey])
  const privacyPolicies = useQuery(resourceListQuery<PrivacyPolicyRes>('privacy-policies'))
  const tracePolicies = useQuery(resourceListQuery<TracePolicyRes>('trace-policies'))
  const priv = doc.privacy ?? { policy_id: '', trace_policy_id: '' }
  const selectedTrace = (tracePolicies.data?.items ?? []).find((t) => t.id === priv.trace_policy_id)
  const save = () => {
    const out: PlanDocument = { ...doc }
    if (!out.privacy?.policy_id && !out.privacy?.trace_policy_id) delete out.privacy
    if (isDefault(out.compatibility)) delete out.compatibility
    const e = serializeEngine(engine)
    if (e) out.engine = e
    else delete out.engine
    onSave(out, note)
  }
  const aliasOptions = [{ value: '', label: 'Select an alias' }, ...(aliases.data?.items ?? []).map((a) => ({ value: a.id, label: a.name }))]
  const providerOptions = (providers.data?.items ?? []).map((p) => ({ value: p.id, label: p.name }))
  const r = doc.restrictions
  const fb = doc.fallback
  const setMapping = (i: number, p: Partial<ModelMapping>) =>
    setDoc({ ...doc, model_mappings: doc.model_mappings.map((m, j) => (j === i ? { ...m, ...p } : m)) })
  const moveStep = (i: number, d: -1 | 1) => {
    const steps = [...fb.steps]
    const j = i + d
    if (j < 0 || j >= steps.length) return
    ;[steps[i], steps[j]] = [steps[j] as string, steps[i] as string]
    setDoc({ ...doc, fallback: { ...fb, steps } })
  }
  const [addStep, setAddStep] = useState('alternate_provider')

  return (
    <Dialog open wide title={title} onClose={onClose}>
      <TextField label="Note" value={note} onChange={setNote} maxLength={500} hint="Describe the change for reviewers and the audit log." />
      <Tabs label="Plan sections" value={tab} onChange={setTab}
        items={[
          { value: 'routing', label: 'Models and fallback' },
          { value: 'rules', label: 'Rules and tables', count: engine.rules.length + engine.tables.length },
          { value: 'privacy', label: 'Privacy' },
          { value: 'compat', label: 'Compatibility' },
        ]} />
      {tab === 'compat' ? (
        <CompatEditor value={doc.compatibility} onChange={(v) => setDoc({ ...doc, compatibility: v })} errors={compatErrors} />
      ) : tab === 'rules' ? (
        <EngineEditor value={engine} onChange={setEngine} errors={engineErrors} />
      ) : tab === 'privacy' ? (
        <>
          <p className="secondary">
            Masking and content capture are independent. Manage policies under <Link to="/privacy" style={{ textDecoration: 'underline' }}>Privacy</Link>.
          </p>
          {privacyErrors.map((e) => (
            <Banner key={e.location} tone="error">{e.message}</Banner>
          ))}
          <div className={f.grid}>
            <SelectField label="Masking policy" value={priv.policy_id ?? ''} onChange={(v) => setDoc({ ...doc, privacy: { ...priv, policy_id: v } })}
              hint="Masks credentials and other sensitive values before requests leave the gateway."
              options={[{ value: '', label: 'None — no outbound masking' }, ...(privacyPolicies.data?.items ?? []).map((x) => ({ value: x.id, label: x.name }))]} />
            <SelectField label="Trace policy" value={priv.trace_policy_id ?? ''} onChange={(v) => setDoc({ ...doc, privacy: { ...priv, trace_policy_id: v } })}
              hint="Captures short-lived, encrypted content. Off unless selected."
              options={[{ value: '', label: 'None — no content capture' }, ...(tracePolicies.data?.items ?? []).map((x) => ({ value: x.id, label: x.name }))]} />
          </div>
          {selectedTrace && (
            <Banner tone={selectedTrace.spec.mode === 'unredacted' ? 'warning' : 'info'}>
              {traceModes.find((m) => m.value === selectedTrace.spec.mode)?.hint}
            </Banner>
          )}
        </>
      ) : (
        <>

        <fieldset className={c.checks} style={{ flexDirection: 'column' }}>
          <legend className={c.legend}>Model mappings (first match wins; exact alias names always match)</legend>
          <div className={c.rows}>
            {doc.model_mappings.map((m, i) => (
              <div key={i} className={c.row}>
                <div className={c.rowHeader}>
                  <strong className="mono">{m.match || `Mapping ${i + 1}`}</strong>
                  <Button size="small" variant="ghost" icon={Trash2} aria-label={`Remove mapping ${m.match || i + 1}`}
                    onClick={() => setDoc({ ...doc, model_mappings: doc.model_mappings.filter((_, j) => j !== i) })} />
                </div>
                <div className={f.grid}>
                  <TextField label="Requested model (exact or glob)" value={m.match} onChange={(v) => setMapping(i, { match: v })} placeholder="claude-sonnet-*" />
                  <SelectField label="Alias" value={m.alias_id} onChange={(v) => setMapping(i, { alias_id: v })} options={aliasOptions} />
                  <TextField label="Note" value={m.note} onChange={(v) => setMapping(i, { note: v })} maxLength={200} />
                </div>
              </div>
            ))}
          </div>
          <div>
            <Button size="small" icon={Plus} onClick={() => setDoc({ ...doc, model_mappings: [...doc.model_mappings, { match: '', alias_id: '', note: '' }] })}>
              Add mapping
            </Button>
          </div>
        </fieldset>
        <SelectField label="Default alias" value={doc.default_alias_id} onChange={(v) => setDoc({ ...doc, default_alias_id: v })}
          options={[{ value: '', label: 'None — reject unmapped models' }, ...aliasOptions.slice(1)]}
          hint="Used when a requested model matches no mapping and no alias name." />

        <h3 style={{ fontSize: 'var(--text-md)' }}>Hard restrictions</h3>
        <p className="secondary">Restrictions can never be overridden by scores or fallback.</p>
        <Checks legend="Allowed providers (none = any)" options={providerOptions} value={r.allowed_provider_ids}
          onChange={(v) => setDoc({ ...doc, restrictions: { ...r, allowed_provider_ids: v } })} />
        <Checks legend="Denied providers" options={providerOptions} value={r.denied_provider_ids}
          onChange={(v) => setDoc({ ...doc, restrictions: { ...r, denied_provider_ids: v } })} />
        <Checks legend="Allowed billing (none = any)" options={billingClasses} value={r.allowed_billing}
          onChange={(v) => setDoc({ ...doc, restrictions: { ...r, allowed_billing: v } })} />
        <Checks legend="Allowed classifications (none = any)" options={classifications.map((x) => ({ value: x, label: x }))} value={r.allowed_classifications}
          onChange={(v) => setDoc({ ...doc, restrictions: { ...r, allowed_classifications: v } })} />
        <Checks legend="Required capabilities" options={capabilities.map((x) => ({ value: x, label: x }))} value={r.required_capabilities}
          onChange={(v) => setDoc({ ...doc, restrictions: { ...r, required_capabilities: v } })} />

        <h3 style={{ fontSize: 'var(--text-md)' }}>Fallback</h3>
        <p className="secondary">
          Fallback only happens before any byte reaches the client and only for provably unexecuted failures. Ambiguous executions are
          never replayed.
        </p>
        <ol className={c.steps} aria-label="Fallback steps in order">
          {fb.steps.map((st, i) => (
            <li key={`${st}-${i}`} className={c.step}>
              <span>
                {i + 1}. {fallbackSteps.find((x) => x.value === st)?.label ?? st}
              </span>
              <Button size="small" variant="ghost" icon={ArrowUp} aria-label={`Move ${st} up`} disabled={i === 0} onClick={() => moveStep(i, -1)} />
              <Button size="small" variant="ghost" icon={ArrowDown} aria-label={`Move ${st} down`} disabled={i === fb.steps.length - 1} onClick={() => moveStep(i, 1)} />
              <Button size="small" variant="ghost" icon={Trash2} aria-label={`Remove ${st}`}
                onClick={() => setDoc({ ...doc, fallback: { ...fb, steps: fb.steps.filter((_, j) => j !== i) } })} />
            </li>
          ))}
        </ol>
        <div className={c.inline} style={{ alignItems: 'flex-end' }}>
          <SelectField label="Add step" value={addStep} onChange={setAddStep} options={fallbackSteps.map((s) => ({ value: s.value, label: s.label }))} />
          <Button size="small" icon={Plus} onClick={() => setDoc({ ...doc, fallback: { ...fb, steps: [...fb.steps, addStep] } })}>Add</Button>
        </div>
        <div className={f.grid}>
          <TextField label="Max attempts" type="number" value={String(fb.max_attempts)} onChange={(v) => setDoc({ ...doc, fallback: { ...fb, max_attempts: toInt(v) } })} />
          <TextField label="Wait max (seconds)" type="number" value={String(fb.wait_max_seconds)} onChange={(v) => setDoc({ ...doc, fallback: { ...fb, wait_max_seconds: toInt(v) } })}
            hint="Required when the wait step is used." />
        </div>
        <Checkbox label="Allow paid fallback (from subscription, free or local candidates to metered API candidates)" checked={fb.allow_paid_fallback}
          onChange={(v) => setDoc({ ...doc, fallback: { ...fb, allow_paid_fallback: v } })} />
        {fb.allow_paid_fallback && <Banner tone="warning">Requests may incur metered API charges when subscription or local capacity is unavailable.</Banner>}
        </>
      )}

      <MutationError error={error} />
      <div className={f.actions}>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={busy} onClick={save}>Save draft</Button>
      </div>
    </Dialog>
  )
}
