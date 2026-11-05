import { SegmentedControl } from '@mantine/core'
import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Button } from '@/components/ui'
import { Checkbox, Dialog, MutationError, SelectField, TextField, formStyles as f } from '@/components/form'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { resourceListQuery } from '@/lib/api/queries'
import { useSaveResource } from '@/lib/api/resources'
import type { AliasRes, AliasSpec, PoolRes } from '@/lib/api/types'
import { capabilities, toggle } from './constants'
import c from './config.module.css'

/** Create or edit a model alias (client-facing model name). */
export function AliasDialog({ alias, onClose }: { alias: AliasRes | null; onClose: () => void }) {
  const pools = useQuery(resourceListQuery<PoolRes>('pools'))
  const aliases = useQuery(resourceListQuery<AliasRes>('model-aliases'))
  const init = alias?.spec
  const [name, setName] = useState(alias?.name ?? '')
  const [description, setDescription] = useState(init?.description ?? '')
  const [target, setTarget] = useState<'pool' | 'alias'>(init?.alias_id ? 'alias' : 'pool')
  const [poolID, setPoolID] = useState(init?.pool_id ?? '')
  const [aliasID, setAliasID] = useState(init?.alias_id ?? '')
  const [caps, setCaps] = useState<string[]>(init?.required_capabilities ?? [])
  const save = useSaveResource<AliasSpec>('model-aliases')
  const submit = () => {
    const spec: AliasSpec = { description, pool_id: target === 'pool' ? poolID : '', alias_id: target === 'alias' ? aliasID : '', required_capabilities: caps }
    save.mutate({ existing: alias ?? undefined, body: { name, spec } }, { onSuccess: onClose })
  }
  return (
    <Dialog open title={alias ? `Edit model name ${alias.name}` : 'Add model name'} onClose={onClose}>
      <TerraformEditWarning r={alias} />
      <TextField label="Name" value={name} onChange={setName} disabled={!!alias} required placeholder="coding/default"
        hint="The model name clients request, e.g. coding/default, coding/fast, review/deep." />
      <TextField label="Description" value={description} onChange={setDescription} maxLength={500} />
      <SegmentedControl aria-label="Goes to" size="sm" value={target} onChange={(v) => setTarget(v as typeof target)}
        data={[{ value: 'pool', label: 'A fallback group' }, { value: 'alias', label: 'Another model name' }]} />
      {target === 'pool' ? (
        <SelectField label="Fallback group" value={poolID} onChange={setPoolID}
          options={[{ value: '', label: 'Select a fallback group' }, ...(pools.data?.items ?? []).map((p) => ({ value: p.id, label: p.name }))]} />
      ) : (
        <SelectField label="Model name" value={aliasID} onChange={setAliasID} hint="Chains are allowed; loops are rejected."
          options={[{ value: '', label: 'Select a model name' }, ...(aliases.data?.items ?? []).filter((a) => a.id !== alias?.id).map((a) => ({ value: a.id, label: a.name }))]} />
      )}
      <fieldset className={c.checks}>
        <legend className={c.legend}>Every account must support</legend>
        {capabilities.map((cap) => (
          <Checkbox key={cap} label={<span className="mono">{cap}</span>} checked={caps.includes(cap)} onChange={(on) => setCaps(toggle(caps, cap, on))} />
        ))}
      </fieldset>
      <MutationError error={save.error} />
      <div className={f.actions}>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} disabled={!name || (target === 'pool' ? !poolID : !aliasID)} onClick={submit}>Save model name</Button>
      </div>
    </Dialog>
  )
}
