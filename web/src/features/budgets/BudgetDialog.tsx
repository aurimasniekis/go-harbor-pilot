import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Select, SimpleGrid, Text } from '@mantine/core'
import { Button } from '@/components/ui'
import { Advanced, Checkbox, DateTimeField, Dialog, DialogActions, MutationError, NameField, nameError, NumberField, SelectField, useFieldErrors } from '@/components/form'
import { fmtDateTime, serverNow } from '@/lib/format'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { resourceListQuery, type ResourcePlural } from '@/lib/api/queries'
import { useSaveResource } from '@/lib/api/resources'
import type { BudgetRes, BudgetSpec, ResourceMeta } from '@/lib/api/types'
import { toggle } from '@/features/config/constants'
import c from '@/features/config/config.module.css'
import { buildBudgetSpec, periodOptions, windowOnPeriodChange } from './budgetSpec'

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

/** Currencies offered for budgets (prices on providers use these). */
export const CURRENCIES = ['USD', 'EUR', 'GBP'] as const

/** IANA time zones for period boundaries (UTC first). */
export function timeZones(): string[] {
  let zones: string[] = []
  try {
    zones = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf?.('timeZone') ?? []
  } catch {
    zones = []
  }
  return ['UTC', ...zones.filter((z) => z !== 'UTC')]
}

/** Problems with a budget limit, or undefined when it is a positive amount. */
export function limitError(limit: number | null): string | undefined {
  if (limit === null) return undefined
  if (!(limit > 0)) return 'Enter an amount greater than 0.'
  return undefined
}

/** Create or edit a spend budget. */
export function BudgetDialog({ budget, onClose }: { budget: BudgetRes | null; onClose: () => void }) {
  const init = budget?.spec
  const [name, setName] = useState(budget?.name ?? '')
  const [scope, setScope] = useState<Scope>(init?.scope ?? 'workspace')
  const [scopeID, setScopeID] = useState(init?.scope_id ?? '')
  const [period, setPeriod] = useState<BudgetSpec['period']>(init?.period ?? 'month')
  const [limit, setLimit] = useState<number | null>(init?.limit ? Number(init.limit) : null)
  const [currency, setCurrency] = useState(init?.currency ?? 'USD')
  const [kinds, setKinds] = useState<string[]>(init?.cost_kinds ?? ['actual_api_charge', 'estimated_api_charge'])
  const [enforcement, setEnforcement] = useState<NonNullable<BudgetSpec['enforcement']>>(init?.enforcement ?? 'block')
  const [warnAt, setWarnAt] = useState<number | null>(Math.round((init?.warn_at ?? 0.8) * 100))
  const [tz, setTz] = useState(init?.timezone ?? '')
  const [enabled, setEnabled] = useState(init?.enabled ?? true)
  const [startsAt, setStartsAt] = useState(init?.starts_at ?? '')
  const [endsAt, setEndsAt] = useState(init?.ends_at ?? '')
  const once = period === 'once'
  const target = scope === 'global' ? undefined : scopeKinds[scope]
  const options = useQuery({ ...resourceListQuery<ResourceMeta>(target?.plural ?? 'workspaces'), enabled: !!target })
  const save = useSaveResource<BudgetSpec>('budgets')
  const errs = useFieldErrors(save.error)
  const zones = useMemo(() => timeZones(), [])
  // A stale server error goes away as soon as the user changes something.
  const edit =
    <T,>(f: (v: T) => void) =>
    (v: T) => {
      if (save.error) save.reset()
      f(v)
    }
  const warnValid = warnAt !== null && warnAt >= 1 && warnAt <= 100
  const currencies = CURRENCIES.includes(currency as (typeof CURRENCIES)[number]) ? [...CURRENCIES] : [...CURRENCIES, currency]

  const submit = () => {
    const spec = buildBudgetSpec({ scope, scopeID, period, limit, currency, kinds, enforcement, warnAt, tz, startsAt, endsAt, enabled })
    save.mutate({ existing: budget ?? undefined, body: { name, spec } }, { onSuccess: onClose })
  }
  const ready = !!name && !nameError(name) && limit !== null && !limitError(limit) && (scope === 'global' || !!scopeID) && kinds.length > 0 && warnValid
  return (
    <Dialog open wide title={budget ? `Edit budget ${budget.name}` : 'Add budget'} onClose={onClose}>
      <TerraformEditWarning r={budget} />
      <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
        <NameField label="Name" value={name} onChange={edit(setName)} locked={!!budget} error={errs.name} autoFocus={!budget} />
        <NumberField
          label="Limit"
          value={limit}
          onChange={edit(setLimit)}
          integer={false}
          min={0}
          unit={currency}
          placeholder="50.00"
          required
          w="100%"
          error={limitError(limit) ?? errs.limit}
        />
        <SelectField<Scope>
          label="Applies to"
          value={scope}
          onChange={edit((v: Scope) => {
            setScope(v)
            setScopeID('')
          })}
          options={[{ value: 'global', label: 'Everything' }, ...Object.entries(scopeKinds).map(([k, v]) => ({ value: k as Scope, label: v.label }))]}
        />
        {target && (
          <SelectField
            label={target.label}
            value={scopeID}
            onChange={edit(setScopeID)}
            placeholder={options.isLoading ? 'Loading…' : `Choose a ${target.label.toLowerCase()}`}
            error={errs.scope_id}
            options={(options.data?.items ?? []).map((o) => ({ value: o.id, label: o.name }))}
          />
        )}
        <div>
          <SelectField
            label="Repeats"
            value={period}
            onChange={edit((v: BudgetSpec['period']) => {
              const w = windowOnPeriodChange(v, { startsAt, endsAt }, init, new Date(serverNow()))
              setPeriod(v)
              setStartsAt(w.startsAt)
              setEndsAt(w.endsAt)
            })}
            options={periodOptions}
          />
          {once && (
            <Text size="xs" c="dimmed" mt={4}>
              Counts all spend{' '}
              {startsAt ? `from ${fmtDateTime(startsAt)}` : init ? 'from when the budget was created' : 'from when the budget is created'} and
              never resets.
            </Text>
          )}
        </div>
        <SelectField
          label="When the limit is reached"
          value={enforcement}
          onChange={edit(setEnforcement)}
          options={[
            { value: 'block', label: 'Block new requests' },
            { value: 'warn', label: 'Warn only' },
          ]}
        />
      </SimpleGrid>
      <Advanced>
        <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="md">
          <NumberField
            label="Warn at"
            unit="%"
            min={1}
            max={100}
            value={warnAt}
            onChange={edit(setWarnAt)}
            w="100%"
            error={warnValid ? undefined : 'Between 1 and 100.'}
          />
          <Select
            label="Currency"
            description="Only costs in this currency count."
            data={currencies}
            value={currency}
            allowDeselect={false}
            onChange={(v) => v && edit(setCurrency)(v)}
            comboboxProps={{ withinPortal: true }}
          />
          {!once && (
            <Select
              label="Time zone"
              description="Where each period starts."
              data={zones}
              value={tz || 'UTC'}
              searchable
              allowDeselect={false}
              onChange={(v) => v && edit(setTz)(v)}
              error={errs.timezone}
              comboboxProps={{ withinPortal: true }}
            />
          )}
        </SimpleGrid>
        {once && (
          <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
            <DateTimeField label="Count spend from" value={startsAt} onChange={edit(setStartsAt)} placeholder="When the budget is created" error={errs.starts_at} />
            <DateTimeField label="Ends" value={endsAt} onChange={edit(setEndsAt)} placeholder="Never" error={errs.ends_at} />
          </SimpleGrid>
        )}
        <fieldset className={c.checks} style={{ flexDirection: 'column' }}>
          <legend className={c.legend}>What counts</legend>
          {costKinds.map((k) => (
            <Checkbox key={k.value} label={k.label} description={k.hint} checked={kinds.includes(k.value)} onChange={(on) => edit(setKinds)(toggle(kinds, k.value, on))} />
          ))}
        </fieldset>
        <Checkbox label="Budget on" checked={enabled} onChange={edit(setEnabled)} />
      </Advanced>
      <MutationError error={save.error} />
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} disabled={!ready} onClick={submit}>
          Save budget
        </Button>
      </DialogActions>
    </Dialog>
  )
}
