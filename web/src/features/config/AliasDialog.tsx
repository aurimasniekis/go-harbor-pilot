import { SegmentedControl, SimpleGrid, Stack, Text } from '@mantine/core'
import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Button } from '@/components/ui'
import { Checkbox, Dialog, DialogActions, MutationError, SelectField, TextField, useFieldErrors } from '@/components/form'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { resourceListQuery } from '@/lib/api/queries'
import { useSaveResource } from '@/lib/api/resources'
import type { AliasRes, AliasSpec, PoolRes } from '@/lib/api/types'
import { capabilities, toggle } from './constants'

/** Model names may have up to four path segments, e.g. coding/default. */
const MODEL_NAME_RE = /^[a-z0-9][a-z0-9._-]{0,62}(\/[a-z0-9][a-z0-9._-]{0,62}){0,3}$/

export function modelNameError(name: string): string | undefined {
  if (!name || MODEL_NAME_RE.test(name)) return undefined
  return 'Use lowercase letters, digits, dots, dashes or underscores, optionally in up to four parts separated by "/" (for example coding/default).'
}

/** True when following `startId`'s chain reaches `targetId` (choosing it would make a loop). */
export function reaches(startId: string, targetId: string, aliases: Record<string, AliasRes | undefined>): boolean {
  let id = startId
  for (let i = 0; i < 16 && id; i++) {
    if (id === targetId) return true
    id = aliases[id]?.spec.alias_id ?? ''
  }
  return false
}

const capabilityLabels: Record<string, string> = {
  streaming: 'Streaming', tools: 'Tool use', parallel_tools: 'Parallel tool calls', tool_streaming: 'Streamed tool arguments', vision: 'Images',
  reasoning: 'Reasoning', structured_output: 'Structured output', prompt_cache: 'Prompt caching', pdf: 'PDF input',
}

/** Create or edit a model name (what clients ask for). */
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
  const errs = useFieldErrors(save.error)
  const byId = Object.fromEntries((aliases.data?.items ?? []).map((a) => [a.id, a]))
  // Offer only targets whose chain does not lead back to this model name.
  const targets = (aliases.data?.items ?? []).filter((a) => !alias || !reaches(a.id, alias.id, byId))
  const submit = () => {
    const spec: AliasSpec = { description, pool_id: target === 'pool' ? poolID : '', alias_id: target === 'alias' ? aliasID : '', required_capabilities: caps }
    save.mutate({ existing: alias ?? undefined, body: { name, spec } }, { onSuccess: onClose })
  }
  const nameProblem = errs['body.name'] ?? modelNameError(name)
  return (
    <Dialog open title={alias ? `Edit model name ${alias.name}` : 'Add model name'} onClose={onClose}>
      <TerraformEditWarning r={alias} />
      <TextField label="Model name" value={name} onChange={setName} disabled={!!alias} required placeholder="e.g. coding/default" error={nameProblem}
        hint={alias ? 'Names cannot be changed after creation.' : 'What clients ask for, e.g. coding/default, coding/fast, review/deep.'} />
      <TextField label="Description" value={description} onChange={setDescription} maxLength={500} placeholder="Optional" />
      <Stack gap={4}>
        <Text size="sm" fw={500}>
          Goes to
        </Text>
        <SegmentedControl aria-label="Goes to" size="sm" value={target} onChange={(v) => setTarget(v as typeof target)} w="fit-content"
          data={[{ value: 'pool', label: 'A fallback group' }, { value: 'alias', label: 'Another model name' }]} />
      </Stack>
      {target === 'pool' ? (
        <SelectField label="Fallback group" value={poolID} onChange={setPoolID} error={errs['body.spec.pool_id']}
          options={[{ value: '', label: 'Choose a fallback group' }, ...(pools.data?.items ?? []).map((p) => ({ value: p.id, label: p.spec.display_name || p.name }))]} />
      ) : (
        <SelectField label="Model name" value={aliasID} onChange={setAliasID} error={errs['body.spec.alias_id']}
          hint="Names that would lead back to this one are not offered."
          options={[{ value: '', label: 'Choose a model name' }, ...targets.filter((a) => a.id !== alias?.id).map((a) => ({ value: a.id, label: a.name }))]} />
      )}
      <Stack gap={6}>
        <Text size="sm" fw={500}>
          Every account must support
        </Text>
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="xs" verticalSpacing={6}>
          {capabilities.map((cap) => (
            <Checkbox key={cap} label={capabilityLabels[cap] ?? cap} checked={caps.includes(cap)} onChange={(on) => setCaps(toggle(caps, cap, on))} />
          ))}
        </SimpleGrid>
      </Stack>
      <MutationError error={save.error} />
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} disabled={!name || !!modelNameError(name) || (target === 'pool' ? !poolID : !aliasID)} onClick={submit}>
          Save model name
        </Button>
      </DialogActions>
    </Dialog>
  )
}
