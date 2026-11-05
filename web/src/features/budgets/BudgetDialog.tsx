import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Button } from '@/components/ui'
import { Advanced, Checkbox, Dialog, MutationError, SelectField, TextField, formStyles as f } from '@/components/form'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { resourceListQuery, type ResourcePlural } from '@/lib/api/queries'
import { useSaveResource } from '@/lib/api/resources'
import type { BudgetRes, BudgetSpec, ResourceMeta } from '@/lib/api/types'
import { toggle } from '@/features/config/constants'
import c from '@/features/config/config.module.css'

type Scope = BudgetSpec['scope']

const scopeKinds: Record<Exclude<Scope, 'global'>, { plural: ResourcePlural; label: string }> = {
  workspace: { plural: 'workspaces', label: 'Workspace' },
  project: { plural: 'projects', label: 'Project' },
  binding: { plural: 'client-bindings', label: 'Client' },
  account: { plural: 'provider-accounts', label: 'Provider account' },
}

export const costKinds = [
  { value: 'estimated_api_charge', label: 'Estimated cost', hint: 'Metered usage priced at the prices configured on providers.' },
  { value: 'actual_api_charge', label: 'Billed cost', hint: 'Charges reported by the providers’ billing APIs.' },
  { value: 'list_equivalent', label: 'Subscription usage at list prices', hint: 'Not money spent; count it only to cap how much of a subscription is used.' },
] as const

/** Create or edit a spend budget. */
export function BudgetDialog({ budget, onClose }: { budget: BudgetRes | null; onClose: () => void }) {
  const init = budget?.spec
  const [name, setName] = useState(budget?.name ?? '')
  const [scope, setScope] = useState<Scope>(init?.scope ?? 'workspace')
  const [scopeID, setScopeID] = useState(init?.scope_id ?? '')
  const [period, setPeriod] = useState<BudgetSpec['period']>(init?.period ?? 'month')
  const [limit, setLimit] = useState(init?.limit ?? '')
  const [currency, setCurrency] = useState(init?.currency ?? 'USD')
  const [kinds, setKinds] = useState<string[]>(init?.cost_kinds ?? ['actual_api_charge', 'estimated_api_charge'])
  const [enforcement, setEnforcement] = useState<NonNullable<BudgetSpec['enforcement']>>(init?.enforcement ?? 'block')
  const [warnAt, setWarnAt] = useState(String(Math.round((init?.warn_at ?? 0.8) * 100)))
  const [tz, setTz] = useState(init?.timezone ?? '')
  const [enabled, setEnabled] = useState(init?.enabled ?? true)
  const target = scope === 'global' ? undefined : scopeKinds[scope]
  const options = useQuery({ ...resourceListQuery<ResourceMeta>(target?.plural ?? 'workspaces'), enabled: !!target })
  const save = useSaveResource<BudgetSpec>('budgets')
  const warnNum = Number(warnAt)
  const warnValid = Number.isFinite(warnNum) && warnNum > 0 && warnNum <= 100

  const submit = () => {
    const spec: BudgetSpec = {
      scope,
      scope_id: scope === 'global' ? '' : scopeID,
      period,
      limit: limit.trim(),
      currency,
      cost_kinds: kinds,
      enforcement,
      warn_at: warnNum / 100,
      timezone: tz.trim(),
      enabled,
    }
    save.mutate({ existing: budget ?? undefined, body: { name, spec } }, { onSuccess: onClose })
  }
  return (
    <Dialog open wide title={budget ? `Edit budget ${budget.name}` : 'Add budget'} onClose={onClose}>
      <TerraformEditWarning r={budget} />
      <div className={f.grid}>
        <TextField label="Name" value={name} onChange={setName} disabled={!!budget} required placeholder="e.g. team-monthly" />
        <TextField label="Limit" value={limit} onChange={setLimit} placeholder="50.00" required suffix={currency} />
        <SelectField<Scope>
          label="Scope"
          value={scope}
          onChange={(v) => {
            setScope(v)
            setScopeID('')
          }}
          options={[{ value: 'global', label: 'Everything' }, ...Object.entries(scopeKinds).map(([k, v]) => ({ value: k as Scope, label: v.label }))]}
        />
        {target && (
          <SelectField label={target.label} value={scopeID} onChange={setScopeID}
            options={[{ value: '', label: options.isLoading ? 'Loading…' : `Select a ${target.label.toLowerCase()}` }, ...(options.data?.items ?? []).map((o) => ({ value: o.id, label: o.name }))]} />
        )}
        <SelectField label="Period" value={period} onChange={setPeriod} options={[{ value: 'day', label: 'Day' }, { value: 'week', label: 'Week (Monday start)' }, { value: 'month', label: 'Month' }]} />
        <SelectField label="Enforcement" value={enforcement} onChange={setEnforcement}
          options={[{ value: 'block', label: 'Block new requests at the limit' }, { value: 'warn', label: 'Warn only' }]} />
      </div>
      <Advanced>
        <div className={f.grid}>
          <TextField label="Warn at" type="number" suffix="%" value={warnAt} onChange={setWarnAt} error={warnValid ? undefined : 'Between 1 and 100.'} />
          <TextField label="Currency" value={currency} onChange={(v) => setCurrency(v.toUpperCase())} maxLength={3} hint="Only costs in this currency count." />
          <TextField label="Timezone" value={tz} onChange={setTz} placeholder="UTC" hint="Period boundaries use this IANA timezone, e.g. Europe/Vilnius." />
        </div>
        <fieldset className={c.checks} style={{ flexDirection: 'column' }}>
          <legend className={c.legend}>What counts</legend>
          {costKinds.map((k) => (
            <Checkbox key={k.value} label={k.label} description={k.hint} checked={kinds.includes(k.value)} onChange={(on) => setKinds(toggle(kinds, k.value, on))} />
          ))}
        </fieldset>
        <Checkbox label="Budget on" checked={enabled} onChange={setEnabled} />
      </Advanced>
      <MutationError error={save.error} />
      <div className={f.actions}>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} disabled={!name || !limit || (scope !== 'global' && !scopeID) || kinds.length === 0 || !warnValid || currency.length !== 3} onClick={submit}>
          Save budget
        </Button>
      </div>
    </Dialog>
  )
}
