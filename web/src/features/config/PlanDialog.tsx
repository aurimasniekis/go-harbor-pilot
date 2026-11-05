import { useState } from 'react'
import { Button } from '@/components/ui'
import { Dialog, MutationError, TextField, formStyles as f } from '@/components/form'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { useSaveResource } from '@/lib/api/resources'
import type { PlanRes, PlanSpec } from '@/lib/api/types'

/** Create or edit routing plan metadata (routing logic lives in versions). */
export function PlanDialog({ plan, onClose }: { plan: PlanRes | null; onClose: () => void }) {
  const [name, setName] = useState(plan?.name ?? '')
  const [displayName, setDisplayName] = useState(plan?.spec.display_name ?? '')
  const [description, setDescription] = useState(plan?.spec.description ?? '')
  const save = useSaveResource<PlanSpec>('routing-plans')
  return (
    <Dialog open title={plan ? `Edit plan ${plan.name}` : 'Add routing plan'} onClose={onClose}>
      <TerraformEditWarning r={plan} />
      <TextField label="Name" value={name} onChange={setName} disabled={!!plan} required />
      <TextField label="Display name" value={displayName} onChange={setDisplayName} maxLength={120} />
      <TextField label="Description" value={description} onChange={setDescription} maxLength={1000} />
      <MutationError error={save.error} />
      <div className={f.actions}>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} disabled={!name}
          onClick={() => save.mutate({ existing: plan ?? undefined, body: { name, spec: { display_name: displayName, description } } }, { onSuccess: onClose })}>
          Save plan
        </Button>
      </div>
    </Dialog>
  )
}
