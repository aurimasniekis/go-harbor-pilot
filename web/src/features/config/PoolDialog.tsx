import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Plus, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui'
import { Dialog, MutationError, SelectField, TextField, formStyles as f } from '@/components/form'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { resourceListQuery } from '@/lib/api/queries'
import { byId, useSaveResource } from '@/lib/api/resources'
import type { AccountRes, PoolMember, PoolRes, PoolSpec, ProviderRes } from '@/lib/api/types'
import { toInt } from './constants'
import c from './config.module.css'

const strategies: { value: PoolSpec['strategy']; label: string }[] = [
  { value: 'ordered', label: 'Ordered (priority)' },
  { value: 'weighted', label: 'Weighted' },
  { value: 'reset_soonest', label: 'Quota reset soonest' },
  { value: 'least_used', label: 'Least used' },
  { value: 'round_robin', label: 'Round robin' },
]

/** Create or edit a pool of account/model candidates. */
export function PoolDialog({ pool, onClose }: { pool: PoolRes | null; onClose: () => void }) {
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const providers = useQuery(resourceListQuery<ProviderRes>('providers'))
  const provById = byId(providers.data?.items)
  const accById = byId(accounts.data?.items)
  const init = pool?.spec
  const [name, setName] = useState(pool?.name ?? '')
  const [displayName, setDisplayName] = useState(init?.display_name ?? '')
  const [strategy, setStrategy] = useState<PoolSpec['strategy']>(init?.strategy || 'ordered')
  const [affinity, setAffinity] = useState<PoolSpec['affinity']>(init?.affinity || 'session')
  const [members, setMembers] = useState<PoolMember[]>(init?.members ?? [])
  const save = useSaveResource<PoolSpec>('pools')
  const setMember = (i: number, p: Partial<PoolMember>) => setMembers(members.map((m, j) => (j === i ? { ...m, ...p } : m)))

  const modelsFor = (accountID: string): string[] => {
    const acc = accById[accountID]
    if (!acc) return []
    const declared = (provById[acc.spec.provider_id]?.spec.models ?? []).map((m) => m.id)
    return acc.spec.allowed_models.length ? declared.filter((m) => acc.spec.allowed_models.includes(m)) : declared
  }

  const submit = () => {
    const spec: PoolSpec = { display_name: displayName, strategy, affinity, members }
    save.mutate({ existing: pool ?? undefined, body: { name, spec } }, { onSuccess: onClose })
  }
  return (
    <Dialog open wide title={pool ? `Edit fallback group ${pool.name}` : 'Add fallback group'} onClose={onClose}>
      <TerraformEditWarning r={pool} />
      <div className={f.grid}>
        <TextField label="Name" value={name} onChange={setName} disabled={!!pool} required />
        <TextField label="Display name" value={displayName} onChange={setDisplayName} maxLength={120} />
        <SelectField label="Order" value={strategy} onChange={setStrategy} options={strategies} hint="How members are tried; plans may override." />
        <SelectField label="Sessions" value={affinity} onChange={setAffinity}
          options={[{ value: 'session', label: 'Keep a conversation on the same member' }, { value: 'none', label: 'None' }]} />
      </div>
      <fieldset className={c.checks} style={{ flexDirection: 'column' }}>
        <legend className={c.legend}>Members</legend>
        <div className={c.rows}>
          {members.length === 0 && <p className="secondary">A pool without members cannot serve requests.</p>}
          {members.map((m, i) => {
            const models = modelsFor(m.account_id)
            return (
              <div key={i} className={c.row}>
                <div className={c.rowHeader}>
                  <strong>Member {i + 1}</strong>
                  <Button size="small" variant="ghost" icon={Trash2} aria-label={`Remove member ${i + 1}`} onClick={() => setMembers(members.filter((_, j) => j !== i))} />
                </div>
                <div className={f.grid}>
                  <SelectField label="Account" value={m.account_id} onChange={(v) => setMember(i, { account_id: v, model: '' })}
                    options={[{ value: '', label: 'Select an account' }, ...(accounts.data?.items ?? []).map((a) => ({ value: a.id, label: a.name }))]} />
                  {models.length > 0 ? (
                    <SelectField label="Model" value={m.model} onChange={(v) => setMember(i, { model: v })}
                      options={[{ value: '', label: 'Select a model' }, ...models.map((id) => ({ value: id, label: id }))]} />
                  ) : (
                    <TextField label="Model id" value={m.model} onChange={(v) => setMember(i, { model: v })} hint="The provider declares no models; enter the upstream id." />
                  )}
                  <TextField label="Try order (lower first)" type="number" value={String(m.priority)} onChange={(v) => setMember(i, { priority: toInt(v) })} />
                  <TextField label="Weight" type="number" value={String(m.weight)} onChange={(v) => setMember(i, { weight: toInt(v) })} />
                </div>
              </div>
            )
          })}
        </div>
        <div>
          <Button size="small" icon={Plus} onClick={() => setMembers([...members, { account_id: '', model: '', priority: members.length, weight: 1 }])}>Add member</Button>
        </div>
      </fieldset>
      <MutationError error={save.error} />
      <div className={f.actions}>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} disabled={!name} onClick={submit}>Save fallback group</Button>
      </div>
    </Dialog>
  )
}
