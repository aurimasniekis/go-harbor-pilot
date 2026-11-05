import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Plus, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui'
import { Dialog, MutationError, SelectField, TextField, formStyles as f } from '@/components/form'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { resourceListQuery } from '@/lib/api/queries'
import { useSaveResource } from '@/lib/api/resources'
import type { AccountRes, EntitlementRes, EntitlementSpec, WindowSpec } from '@/lib/api/types'
import { toInt } from './constants'
import c from './config.module.css'

const units: WindowSpec['unit'][] = ['percent', 'tokens', 'requests', 'credits', 'unknown']

/** Create or edit a subscription entitlement (what a subscription includes and costs). */
export function EntitlementDialog({ ent, onClose, accountID: presetAccount }: { ent: EntitlementRes | null; onClose: () => void; accountID?: string }) {
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const init = ent?.spec
  const [name, setName] = useState(ent?.name ?? '')
  const [accountID, setAccountID] = useState(init?.account_id ?? presetAccount ?? '')
  const [planName, setPlanName] = useState(init?.plan_name ?? '')
  const [fee, setFee] = useState(init?.fee ?? '')
  const [currency, setCurrency] = useState(init?.currency ?? 'USD')
  const [period, setPeriod] = useState<EntitlementSpec['period']>(init?.period ?? 'monthly')
  const [startsOn, setStartsOn] = useState(init?.starts_on ?? new Date().toISOString().slice(0, 10))
  const [endsOn, setEndsOn] = useState(init?.ends_on ?? '')
  const [windows, setWindows] = useState<WindowSpec[]>(init?.windows ?? [])
  const save = useSaveResource<EntitlementSpec>('subscription-entitlements')
  const setWin = (i: number, p: Partial<WindowSpec>) => setWindows(windows.map((w, j) => (j === i ? { ...w, ...p } : w)))

  const submit = () => {
    const spec: EntitlementSpec = { account_id: accountID, plan_name: planName, fee, currency, period, starts_on: startsOn, ends_on: endsOn, windows }
    save.mutate({ existing: ent ?? undefined, body: { name, spec } }, { onSuccess: onClose })
  }
  return (
    <Dialog open wide title={ent ? `Edit subscription ${ent.name}` : 'Add subscription'} onClose={onClose}>
      <TerraformEditWarning r={ent} />
      <div className={f.grid}>
        <TextField label="Name" value={name} onChange={setName} disabled={!!ent} required />
        <SelectField label="Account" value={accountID} onChange={setAccountID}
          options={[{ value: '', label: 'Select an account' }, ...(accounts.data?.items ?? []).map((a) => ({ value: a.id, label: a.name }))]} />
        <TextField label="Plan name" value={planName} onChange={setPlanName} placeholder="e.g. Max 20x" required />
        <TextField label="Fee per period" value={fee} onChange={setFee} placeholder="200.00" required />
        <TextField label="Currency" value={currency} onChange={(v) => setCurrency(v.toUpperCase())} maxLength={3} />
        <SelectField label="Period" value={period} onChange={setPeriod} options={[{ value: 'monthly', label: 'Monthly' }, { value: 'yearly', label: 'Yearly' }]} />
        <TextField label="Starts on" type="date" value={startsOn} onChange={setStartsOn} required />
        <TextField label="Ends on" type="date" value={endsOn} onChange={setEndsOn} hint="Empty = open-ended." />
      </div>
      <fieldset className={c.checks} style={{ flexDirection: 'column' }}>
        <legend className={c.legend}>Quota windows</legend>
        <div className={c.rows}>
          {windows.map((w, i) => (
            <div key={i} className={c.row}>
              <div className={c.rowHeader}>
                <strong className="mono">{w.name || `Window ${i + 1}`}</strong>
                <Button size="small" variant="ghost" icon={Trash2} aria-label={`Remove window ${w.name || i + 1}`} onClick={() => setWindows(windows.filter((_, j) => j !== i))} />
              </div>
              <div className={f.grid}>
                <TextField label="Name" value={w.name} onChange={(v) => setWin(i, { name: v })} placeholder="five_hour" />
                <TextField label="Length (seconds)" type="number" value={String(w.seconds)} onChange={(v) => setWin(i, { seconds: toInt(v) })} />
                <SelectField label="Unit" value={w.unit} onChange={(v) => setWin(i, { unit: v })} options={units.map((u) => ({ value: u, label: u }))} />
                <TextField label="Limit (optional)" type="number" value={w.limit === undefined ? '' : String(w.limit)}
                  onChange={(v) => setWin(i, { limit: v === '' ? undefined : toInt(v) })} hint="Leave empty when the provider does not publish one." />
              </div>
            </div>
          ))}
        </div>
        <div>
          <Button size="small" icon={Plus} onClick={() => setWindows([...windows, { name: '', seconds: 18000, unit: 'percent' }])}>Add window</Button>
        </div>
      </fieldset>
      <MutationError error={save.error} />
      <div className={f.actions}>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} disabled={!name || !accountID || !planName} onClick={submit}>Save subscription</Button>
      </div>
    </Dialog>
  )
}
