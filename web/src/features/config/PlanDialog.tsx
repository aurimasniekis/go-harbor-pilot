import { useState } from 'react'
import { Button } from '@/components/ui'
import { Dialog, DialogActions, MutationError, NameField, TextAreaField, TextField, nameError, slugify, useFieldErrors } from '@/components/form'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { useSaveResource } from '@/lib/api/resources'
import type { PlanRes, PlanSpec } from '@/lib/api/types'

/** Create or edit routing plan metadata (routing logic lives in versions). */
export function PlanDialog({ plan, onClose, onCreated }: { plan: PlanRes | null; onClose: () => void; onCreated?: (p: PlanRes) => void }) {
  const [name, setName] = useState(plan?.name ?? '')
  const [nameTouched, setNameTouched] = useState(!!plan)
  const [displayName, setDisplayName] = useState(plan?.spec.display_name ?? '')
  const [description, setDescription] = useState(plan?.spec.description ?? '')
  const save = useSaveResource<PlanSpec, PlanRes>('routing-plans')
  const errs = useFieldErrors(save.error)
  const invalid = !name || !!nameError(name)
  return (
    <Dialog open title={plan ? `Edit plan ${plan.spec.display_name || plan.name}` : 'New routing plan'} onClose={onClose}>
      <TerraformEditWarning r={plan} />
      <TextField label="Display name" value={displayName} maxLength={120} placeholder="e.g. Team default" autoFocus
        onChange={(v) => {
          setDisplayName(v)
          if (!nameTouched) setName(slugify(v))
        }} />
      <NameField value={name} locked={!!plan} error={errs['body.name']}
        onChange={(v) => {
          setNameTouched(true)
          setName(v)
        }} />
      <TextAreaField label="Description" value={description} onChange={setDescription} maxLength={1000} minRows={2} placeholder="Optional"
        hint={plan ? undefined : 'Next you draft where its requests go.'} />
      <MutationError error={save.error} />
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} disabled={invalid}
          onClick={() => save.mutate({ existing: plan ?? undefined, body: { name, spec: { display_name: displayName, description } } }, { onSuccess: (r) => (plan || !onCreated ? onClose() : onCreated(r)) })}>
          {plan ? 'Save plan' : 'Create plan'}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
