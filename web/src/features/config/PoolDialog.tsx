import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Select, Stack, Table, Text, TextInput } from '@mantine/core'
import { ArrowDown, ArrowUp, Plus, Trash2 } from 'lucide-react'
import { Banner, Button } from '@/components/ui'
import { Dialog, DialogActions, MutationError, NameField, NumberField, SelectField, TextField, nameError, slugify, useFieldErrors } from '@/components/form'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { resourceListQuery } from '@/lib/api/queries'
import { byId, useSaveResource } from '@/lib/api/resources'
import type { AccountRes, PoolMember, PoolRes, PoolSpec, ProviderRes } from '@/lib/api/types'
import { move } from '@/features/routing/engine'
import { accountLabel } from '@/features/routing/names'
import c from './config.module.css'

export const strategies: { value: PoolSpec['strategy']; label: string }[] = [
  { value: 'ordered', label: 'Tried in order' },
  { value: 'weighted', label: 'Spread by weight' },
  { value: 'reset_soonest', label: 'Quota that resets soonest first' },
  { value: 'least_used', label: 'Least used first' },
  { value: 'round_robin', label: 'Take turns' },
]

/** Members in try order: by priority, then list position. */
function sortedMembers(members: PoolMember[]): PoolMember[] {
  return members.map((m, i) => ({ m, i })).sort((a, b) => (a.m.priority ?? 0) - (b.m.priority ?? 0) || a.i - b.i).map((x) => x.m)
}

/** Create or edit a fallback group: accounts and models tried for a model name. */
export function PoolDialog({ pool, onClose }: { pool: PoolRes | null; onClose: () => void }) {
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const providers = useQuery(resourceListQuery<ProviderRes>('providers'))
  const provById = byId(providers.data?.items)
  const accById = byId(accounts.data?.items)
  const init = pool?.spec
  const [name, setName] = useState(pool?.name ?? '')
  const [nameTouched, setNameTouched] = useState(!!pool)
  const [displayName, setDisplayName] = useState(init?.display_name ?? '')
  const [strategy, setStrategy] = useState<PoolSpec['strategy']>(init?.strategy || 'ordered')
  const [affinity, setAffinity] = useState<PoolSpec['affinity']>(init?.affinity || 'session')
  // One ordering: the list position. Priorities are derived from it on save.
  const [members, setMembers] = useState<PoolMember[]>(sortedMembers(init?.members ?? []))
  const save = useSaveResource<PoolSpec>('pools')
  const errs = useFieldErrors(save.error)
  const setMember = (i: number, p: Partial<PoolMember>) => setMembers(members.map((m, j) => (j === i ? { ...m, ...p } : m)))

  const modelsFor = (accountID: string): string[] => {
    const acc = accById[accountID]
    if (!acc) return []
    const declared = (provById[acc.spec.provider_id]?.spec.models ?? []).map((m) => m.id)
    return acc.spec.allowed_models.length ? declared.filter((m) => acc.spec.allowed_models.includes(m)) : declared
  }
  const accountOptions = (accounts.data?.items ?? []).map((a) => ({ value: a.id, label: accountLabel(a, provById) }))
  const incomplete = members.some((m) => !m.account_id || !m.model.trim())
  const problem = members.length === 0 ? 'Add at least one account; a fallback group without accounts rejects every request.' : incomplete ? 'Every row needs an account and a model.' : ''

  const submit = () => {
    const spec: PoolSpec = { display_name: displayName, strategy, affinity, members: members.map((m, i) => ({ ...m, priority: i, weight: strategy === 'weighted' ? m.weight : m.weight || 1 })) }
    save.mutate({ existing: pool ?? undefined, body: { name, spec } }, { onSuccess: onClose })
  }
  return (
    <Dialog open wide title={pool ? `Edit fallback group ${pool.spec.display_name || pool.name}` : 'Add fallback group'} onClose={onClose}>
      <TerraformEditWarning r={pool} />
      <TextField label="Display name" value={displayName} maxLength={120} placeholder="e.g. Coding default"
        onChange={(v) => {
          setDisplayName(v)
          if (!nameTouched) setName(slugify(v))
        }} />
      <NameField value={name} locked={!!pool} error={errs['body.name']}
        onChange={(v) => {
          setNameTouched(true)
          setName(v)
        }} />
      <SelectField label="How accounts are chosen" value={strategy} onChange={setStrategy} options={strategies} hint="Plans may override this." />
      <SelectField label="Conversations" value={affinity} onChange={setAffinity}
        options={[{ value: 'session', label: 'Keep a conversation on the same account (prompt-cache friendly)' }, { value: 'none', label: 'Choose freely for every request' }]} />
      <Stack gap="xs">
        <Text size="sm" fw={600}>
          Accounts{strategy === 'ordered' ? ', in the order they are tried' : ''}
        </Text>
        {members.length > 0 && (
          <Table className={c.mapTable} verticalSpacing={6} withRowBorders={false}>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Account</Table.Th>
                <Table.Th>Model</Table.Th>
                {strategy === 'weighted' && <Table.Th>Weight</Table.Th>}
                <Table.Th>
                  <span className="visually-hidden">Order and remove</span>
                </Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {members.map((m, i) => {
                const models = modelsFor(m.account_id)
                return (
                  <Table.Tr key={i}>
                    <Table.Td>
                      <Select aria-label={`Row ${i + 1}: account`} data={accountOptions} value={m.account_id || null} placeholder="Choose an account" searchable
                        allowDeselect={false} onChange={(v) => setMember(i, { account_id: v ?? '', model: '' })} />
                    </Table.Td>
                    <Table.Td>
                      {models.length > 0 ? (
                        <Select aria-label={`Row ${i + 1}: model`} data={models} value={m.model || null} placeholder="Choose a model" searchable allowDeselect={false}
                          onChange={(v) => setMember(i, { model: v ?? '' })} />
                      ) : (
                        <TextInput aria-label={`Row ${i + 1}: model id`} value={m.model} placeholder="Upstream model id" disabled={!m.account_id}
                          onChange={(e) => setMember(i, { model: e.currentTarget.value })} />
                      )}
                    </Table.Td>
                    {strategy === 'weighted' && (
                      <Table.Td>
                        <NumberField label={<span className="visually-hidden">Row {i + 1}: weight</span>} value={m.weight ?? 0} min={0} max={1000} w={90}
                          onChange={(v) => setMember(i, { weight: v ?? 0 })} />
                      </Table.Td>
                    )}
                    <Table.Td className={c.mapActions}>
                      <Button size="small" variant="ghost" icon={ArrowUp} aria-label={`Move row ${i + 1} up`} disabled={i === 0}
                        className={i === 0 ? c.hiddenAction : undefined} onClick={() => setMembers(move(members, i, -1))} />
                      <Button size="small" variant="ghost" icon={ArrowDown} aria-label={`Move row ${i + 1} down`} disabled={i === members.length - 1}
                        className={i === members.length - 1 ? c.hiddenAction : undefined} onClick={() => setMembers(move(members, i, 1))} />
                      <Button size="small" variant="ghost" icon={Trash2} aria-label={`Remove row ${i + 1}`} onClick={() => setMembers(members.filter((_, j) => j !== i))} />
                    </Table.Td>
                  </Table.Tr>
                )
              })}
            </Table.Tbody>
          </Table>
        )}
        <div>
          <Button size="small" icon={Plus} onClick={() => setMembers([...members, { account_id: '', model: '', priority: members.length, weight: 1 }])}>
            Add account
          </Button>
        </div>
        {problem && <Banner tone={members.length === 0 ? 'warning' : 'info'}>{problem}</Banner>}
      </Stack>
      <MutationError error={save.error} />
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} disabled={!name || !!nameError(name) || !!problem} onClick={submit}>
          Save fallback group
        </Button>
      </DialogActions>
    </Dialog>
  )
}
