import { Banner } from '@/components/ui'
import { Checkbox, SelectField, formStyles as f } from '@/components/form'
import type { PlanCompat } from '@/lib/api/types'
import { degradableFeatures, missingForPassthrough, passthroughRequired, withDefaults, lossLabel } from './compat'
import c from './compat.module.css'

/** Editor for a plan document's compatibility (degradation) policy. */
export function CompatEditor({ value, onChange, errors }: {
  value: PlanCompat | undefined
  onChange: (v: PlanCompat) => void
  /** Server field errors for body.document.compatibility.* */
  errors: { location: string; message: string }[]
}) {
  const v = withDefaults(value)
  const set = (p: Partial<PlanCompat>) => onChange({ ...v, ...p })
  const missing = missingForPassthrough(v)
  const toggle = (feature: string, on: boolean) =>
    set({ allowed_losses: on ? [...new Set([...v.allowed_losses, feature])] : v.allowed_losses.filter((x) => x !== feature) })
  return (
    <div className={c.stack}>
      <p className="secondary">
        When repeated compatibility failures open a provider path&apos;s circuit breaker, this policy decides what the plan does. Nothing here
        weakens a mandatory requirement silently: required masking or an unenforceable blocking budget still refuse passthrough.
      </p>
      {errors.map((e) => (
        <Banner key={e.location + e.message} tone="error" role="alert">
          {e.location.replace('body.document.compatibility.', '').replace('body.document.compatibility', 'compatibility')}: {e.message}
        </Banner>
      ))}
      <SelectField label="When a breaker is open" value={v.on_breaker_open} onChange={(x) => set({ on_breaker_open: x })}
        hint="Passthrough forwards the client's original payload unchanged to a native upstream; other upstreams are skipped either way."
        options={[
          { value: 'fail', label: 'Skip the candidate (fail over or fail)' },
          { value: 'passthrough', label: 'Use native passthrough (pre-authorized losses)' },
        ]} />
      <fieldset className={c.stack} style={{ border: 0, padding: 0, margin: 0 }}>
        <legend style={{ fontWeight: 600, marginBottom: 6 }}>Pre-authorized losses while degraded</legend>
        <div className={c.losses}>
          {degradableFeatures.map((feat) => (
            <div key={feat.value}>
              <Checkbox label={feat.label} checked={v.allowed_losses.includes(feat.value)} onChange={(on) => toggle(feat.value, on)} />
              <div className={c.hint}>{feat.hint}</div>
            </div>
          ))}
        </div>
      </fieldset>
      {missing.length > 0 && (
        <Banner tone="warning">
          Native passthrough always loses {passthroughRequired.map(lossLabel).join(' and ')}; list {missing.map(lossLabel).join(' and ')} above to
          authorize it, or the draft will be rejected.
        </Banner>
      )}
      <div className={f.grid}>
        <SelectField label="Unknown usage" value={v.unknown_usage} onChange={(x) => set({ unknown_usage: x })}
          hint="When passthrough cannot read token counts from the raw stream."
          options={[
            { value: 'record', label: 'Record as unknown usage' },
            { value: 'estimate', label: 'Estimate from request and response size' },
          ]} />
        <SelectField label="Unenforceable token budgets" value={v.unenforceable_budget} onChange={(x) => set({ unenforceable_budget: x })}
          hint="A blocking budget relies on exact usage, which passthrough cannot guarantee."
          options={[
            { value: 'block', label: 'Refuse passthrough when a blocking budget applies' },
            { value: 'allow', label: 'Allow passthrough anyway' },
          ]} />
      </div>
      <p className="muted">Concurrency and request-rate limits stay enforced in every mode.</p>
    </div>
  )
}
