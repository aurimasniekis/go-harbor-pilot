import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ActionIcon, Group, MultiSelect, SegmentedControl, Select, Stack, Text, TextInput } from '@mantine/core'
import { Plus, X } from 'lucide-react'
import { Button } from '@/components/ui'
import { Advanced } from '@/components/form'
import { eventCatalogQuery } from '@/lib/api/queries'
import type { EventFilter, EventKindInfo } from '@/lib/api/types'
import { eventAreas, eventLabel, eventPatternRE, loopingPattern, matchingKinds, rulePresets } from './events'

type Severity = 'info' | 'warning' | 'critical'

/** Grouped options for the event picker: "any <area> event" plus each kind. */
function pickerData(catalog: EventKindInfo[], selected: string[]) {
  const known = new Set<string>()
  const groups = eventAreas
    .map((a) => {
      const kinds = catalog.filter((k) => k.name.split('.')[0] === a.prefix && !k.name.includes('<'))
      const items = [{ value: `${a.prefix}.*`, label: eventLabel(`${a.prefix}.*`) }, ...kinds.map((k) => ({ value: k.name, label: eventLabel(k.name) }))]
      if (a.prefix === 'config') items.splice(1)
      items.forEach((i) => known.add(i.value))
      return { group: a.label, items }
    })
    .filter((g) => g.items.length > 0)
  const custom = selected.filter((s) => !known.has(s))
  if (custom.length) groups.push({ group: 'Custom patterns', items: custom.map((c) => ({ value: c, label: c })) })
  return groups
}

/** Key = value conditions on event subjects, as rows. */
function SubjectRows({ value, keys, onChange }: { value: Record<string, string>; keys: string[]; onChange: (v: Record<string, string>) => void }) {
  const rows = Object.entries(value)
  const [draftKey, setDraftKey] = useState<string | null>(null)
  const [draftValue, setDraftValue] = useState('')
  const add = () => {
    if (!draftKey || !draftValue.trim()) return
    onChange({ ...value, [draftKey]: draftValue.trim() })
    setDraftKey(null)
    setDraftValue('')
  }
  return (
    <Stack gap={6}>
      <Stack gap={0}>
        <Text size="sm" fw={500}>
          Only about
        </Text>
        <Text size="xs" c="dimmed">
          Limit to events about one account, provider, client or other identity. Leave empty for all.
        </Text>
      </Stack>
      {rows.map(([k, v]) => (
        <Group key={k} gap="xs" wrap="nowrap">
          <Text size="sm" ff="monospace" style={{ flex: '0 0 auto' }}>
            {k}
          </Text>
          <Text size="sm" c="dimmed">
            =
          </Text>
          <Text size="sm" ff="monospace" truncate style={{ flex: 1 }}>
            {v}
          </Text>
          <ActionIcon variant="subtle" color="gray" aria-label={`Remove ${k}`} onClick={() => {
            const next = { ...value }
            delete next[k]
            onChange(next)
          }}>
            <X size={14} />
          </ActionIcon>
        </Group>
      ))}
      <Group gap="xs" wrap="nowrap" align="flex-start">
        <Select aria-label="Identity" placeholder="Identity" data={keys} value={draftKey} onChange={setDraftKey} searchable w={200}
          comboboxProps={{ withinPortal: true }} />
        <TextInput aria-label="Identity value" placeholder="e.g. acc_…" value={draftValue} onChange={(e) => setDraftValue(e.currentTarget.value)} style={{ flex: 1 }}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              add()
            }
          }} />
        <Button icon={Plus} disabled={!draftKey || !draftValue.trim()} onClick={add}>
          Add
        </Button>
      </Group>
    </Stack>
  )
}

/**
 * Which events a rule or relay reacts to: a picker of plain-language event
 * kinds grouped by area (with presets), a severity floor, and optional
 * identity conditions and custom name patterns.
 */
export function FilterEditor({ value, onChange, errors, presets, advanced = true }: {
  value: EventFilter
  onChange: (f: EventFilter, valid: boolean) => void
  errors?: Record<string, string | undefined>
  /** Show one-click presets (alert rules). */
  presets?: boolean
  /** Show identity conditions and custom patterns. */
  advanced?: boolean
}) {
  const catalog = useQuery(eventCatalogQuery)
  const items = useMemo(() => catalog.data?.items ?? [], [catalog.data])
  const events = useMemo(() => value.events ?? [], [value.events])
  const data = useMemo(() => pickerData(items, events), [items, events])
  const subjectKeys = useMemo(() => [...new Set(items.flatMap((k) => k.subjects ?? []))].sort(), [items])
  const [custom, setCustom] = useState('')
  const [showCustom, setShowCustom] = useState(false)
  const looping = events.filter(loopingPattern)
  const emit = (f: EventFilter) => onChange(f, !(f.events ?? []).some(loopingPattern))
  const customOK = custom === '' || eventPatternRE.test(custom.trim())
  const addCustom = () => {
    const p = custom.trim()
    if (!p || !eventPatternRE.test(p) || events.includes(p)) return
    emit({ ...value, events: [...events, p] })
    setCustom('')
  }
  const kinds = catalog.data ? matchingKinds(value, items) : []
  const sample = kinds.filter((k) => !k.name.includes('<')).map((k) => eventLabel(k.name))
  return (
    <Stack gap="sm">
      {presets && (
        <Group gap={6} wrap="wrap" aria-label="Presets">
          <Text size="xs" c="dimmed">
            Start from:
          </Text>
          {rulePresets.map((p) => (
            <Button key={p.label} size="small" onClick={() => emit({ ...value, events: [...new Set([...events, ...p.events])] })}>
              {p.label}
            </Button>
          ))}
        </Group>
      )}
      <MultiSelect
        label="Events"
        description={events.length === 0 ? 'No events picked: every event matches.' : undefined}
        placeholder={events.length ? undefined : 'Pick events, e.g. Quota used up'}
        data={data}
        value={events}
        onChange={(v) => emit({ ...value, events: v })}
        searchable
        clearable
        hidePickedOptions
        comboboxProps={{ withinPortal: true }}
        error={
          looping.length
            ? `Remove ${looping.join(', ')}: alerts about alerts would loop.`
            : errors?.['events']
        }
        aria-label="Events"
      />
      <Stack gap={4}>
        <Text size="sm" fw={500}>
          How serious
        </Text>
        <SegmentedControl
          aria-label="Minimum severity"
          value={(value.min_severity || 'info') as Severity}
          onChange={(v) => emit({ ...value, min_severity: v })}
          data={[
            { value: 'info', label: 'Everything' },
            { value: 'warning', label: 'Warnings and critical' },
            { value: 'critical', label: 'Critical only' },
          ]}
          style={{ alignSelf: 'flex-start' }}
        />
      </Stack>
      {catalog.data && (
        <Text size="xs" c="dimmed" aria-live="polite">
          Matches {kinds.length} of {items.length} event kinds{sample.length > 0 && sample.length <= 6 ? `: ${sample.join(', ')}` : ''}.
        </Text>
      )}
      {advanced && (
        <Advanced label="More conditions" defaultOpen={Object.keys(value.subjects ?? {}).length > 0}>
          <SubjectRows value={value.subjects ?? {}} keys={subjectKeys} onChange={(s) => emit({ ...value, subjects: s })} />
          {showCustom ? (
            <Group gap="xs" wrap="nowrap" align="flex-start">
              <TextInput label="Custom pattern" description="Dotted event name; * matches one part, a trailing .* matches the rest." value={custom}
                onChange={(e) => setCustom(e.currentTarget.value)} placeholder="quota.window.*" style={{ flex: 1 }} ff="monospace"
                error={customOK ? undefined : 'Use lowercase name parts separated by dots; * is a wildcard.'}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault()
                    addCustom()
                  }
                }} />
              <Button icon={Plus} disabled={!custom.trim() || !customOK} onClick={addCustom} style={{ marginTop: 46 }}>
                Add
              </Button>
            </Group>
          ) : (
            <div>
              <Button size="small" variant="ghost" onClick={() => setShowCustom(true)}>
                Match by name pattern instead…
              </Button>
            </div>
          )}
        </Advanced>
      )}
    </Stack>
  )
}
