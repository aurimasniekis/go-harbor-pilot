import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Autocomplete, Button, Group, Popover, Stack, Text, TextInput } from '@mantine/core'
import { Tags } from 'lucide-react'
import { resourceListQuery } from '@/lib/api/queries'
import type { BindingRes } from '@/lib/api/types'
import { MAX_REFS, REF_KEY, type Ref, refKeyOptions, refProblem } from './refs'

/** Key suggestions: common run keys, then the keys set on clients. */
export function useRefKeyOptions(): { value: string; label: string }[] {
  const bindings = useQuery(resourceListQuery<BindingRes>('client-bindings'))
  return refKeyOptions((bindings.data?.items ?? []).map((b) => b.spec.external_refs))
}

/** Reference key and value fields with an Add button. */
export function RefForm({ existing, onAdd, withinPortal = false }: { existing: readonly string[]; onAdd: (r: Ref) => void; withinPortal?: boolean }) {
  const options = useRefKeyOptions()
  const [key, setKey] = useState('spaceman.run')
  const [value, setValue] = useState('')
  const [tried, setTried] = useState(false)
  const k = key.trim()
  const problem = refProblem(k, value, existing)
  const full = existing.length >= MAX_REFS
  const keyOk = REF_KEY.test(k)
  const submit = () => {
    setTried(true)
    if (problem) return
    onAdd({ key: k, value: value.trim() })
    setValue('')
    setTried(false)
  }
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        submit()
      }}
    >
      <Stack gap="xs">
        <Text size="xs" c="dimmed">
          Trusted references come from the access token or the client, for example the run an agent request belongs to.
        </Text>
        <Autocomplete
          label="Reference"
          description="A key such as spaceman.run"
          value={key}
          onChange={setKey}
          data={options}
          comboboxProps={{ withinPortal }}
          error={tried && !keyOk ? problem : undefined}
        />
        <TextInput label="Value" placeholder="run_…" value={value} onChange={(e) => setValue(e.currentTarget.value)} error={tried && keyOk ? (problem ?? undefined) : undefined} />
        <Group justify="flex-end">
          <Button type="submit" size="xs" disabled={full && !existing.some((x) => x.startsWith(`${k}:`))}>
            Add filter
          </Button>
        </Group>
        {full && (
          <Text size="xs" c="dimmed">
            Up to {MAX_REFS} references at once. Remove one to add another.
          </Text>
        )}
      </Stack>
    </form>
  )
}

/** "Filter by reference" button with the form in a popover. */
export function RefFilterButton({ existing, onAdd, size = 'sm' }: { existing: readonly string[]; onAdd: (r: Ref) => void; size?: 'sm' | 'md' }) {
  const [open, setOpen] = useState(false)
  return (
    <Popover opened={open} onChange={setOpen} position="bottom-end" width={280} shadow="md" withinPortal trapFocus>
      <Popover.Target>
        <Button variant="default" size={size} leftSection={<Tags size={14} />} onClick={() => setOpen((o) => !o)}>
          {existing.length ? `References (${existing.length})` : 'Filter by reference'}
        </Button>
      </Popover.Target>
      <Popover.Dropdown>
        <RefForm
          existing={existing}
          onAdd={(r) => {
            onAdd(r)
            setOpen(false)
          }}
        />
      </Popover.Dropdown>
    </Popover>
  )
}
