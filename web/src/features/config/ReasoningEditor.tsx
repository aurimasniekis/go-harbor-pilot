import { Checkbox as MCheckbox, Group, SimpleGrid, Stack, Switch, Text } from '@mantine/core'
import { Advanced, NumberField, SelectField, TextField } from '@/components/form'
import type { ModelReasoningSpec } from '@/lib/api/types'
import {
  defaultHistoryField,
  emptyReasoning,
  historyAllowed,
  levelOnly,
  levelsFor,
  modeOptions,
  styleLabels,
  stylesFor,
  type ReasoningMode,
} from './reasoning'

/**
 * Editor for a model's reasoning option (D-047): how the client's reasoning
 * settings (Claude Code's adaptive thinking and effort, reasoning_effort, …)
 * are written for this model's upstream.
 */
export function ReasoningEditor({ value, providerType, onChange }: {
  value: ModelReasoningSpec | undefined
  providerType: string
  onChange: (r: ModelReasoningSpec | undefined) => void
}) {
  const r = value ?? emptyReasoning()
  const mode = (r.mode || 'auto') as ReasoningMode
  const set = (p: Partial<ModelReasoningSpec>) => onChange({ ...r, ...p })
  const styles = stylesFor(providerType)
  const levels = levelsFor(providerType)
  const usesLevel = mode === 'client' || mode === 'fixed'
  // A fixed budget only for styles that send one.
  const budgetOK = !(mode === 'fixed' && levelOnly(r.style ?? '', providerType))
  const history = r.history === 'field'
  const configured = mode !== 'auto' || history

  return (
    <Advanced label={`Reasoning${configured ? ' (set)' : ''}`} defaultOpen={configured}>
      <Text size="xs" c="dimmed">
        How the client’s reasoning settings, such as Claude Code’s adaptive thinking and effort, are sent to this
        model. Built in keeps the gateway’s default translation, which sends adaptive thinking only to Anthropic
        models.
      </Text>
      <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
        <SelectField label="Reasoning translation" value={mode} onChange={(v) => set({ mode: v })} options={modeOptions} />
        {mode !== 'auto' && (
          <SelectField label="Upstream parameter" hint="The shape this provider understands."
            value={r.style ?? ''} onChange={(v) => set({ style: v as ModelReasoningSpec['style'] })}
            options={[{ value: '', label: 'Provider default' }, ...styles.map((s) => ({ value: s, label: styleLabels[s] ?? s }))]} />
        )}
      </SimpleGrid>
      {usesLevel && (
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
          <SelectField label={mode === 'fixed' ? 'Level' : 'Default level'}
            hint={mode === 'fixed' ? 'Sent on every request.' : 'Used when the client names no level.'}
            value={r.effort ?? ''} onChange={(v) => set({ effort: v as ModelReasoningSpec['effort'], budget_tokens: v ? 0 : r.budget_tokens })}
            options={[{ value: '', label: mode === 'fixed' ? (budgetOK ? 'Use a budget instead' : 'Choose a level') : 'None' }, ...levels.map((l) => ({ value: l, label: l }))]} />
          {budgetOK && (
            <NumberField label="Or a thinking budget" unit="tokens" min={1} placeholder="None" disabled={!!r.effort}
              value={r.budget_tokens || null} onChange={(n) => set({ budget_tokens: n ?? 0 })} w="100%" />
          )}
        </SimpleGrid>
      )}
      {mode === 'client' && (
        <Stack gap="sm">
          <MCheckbox.Group label="Accepted levels" description="Higher levels are lowered to the nearest one here. None ticked: the parameter’s usual levels."
            value={r.efforts ?? []} onChange={(v) => set({ efforts: v })}>
            <Group gap="md" mt={6}>
              {levels.map((l) => (
                <MCheckbox key={l} value={l} label={l} />
              ))}
            </Group>
          </MCheckbox.Group>
          <NumberField label="Largest thinking budget" unit="tokens" min={1} placeholder="No cap"
            value={r.max_budget_tokens || null} onChange={(n) => set({ max_budget_tokens: n ?? 0 })} w={220} />
        </Stack>
      )}
      {historyAllowed(providerType) && (
        <Stack gap={6}>
          <Switch label="Send earlier reasoning back" description="Some APIs need the model’s earlier reasoning on assistant messages."
            checked={history} onChange={(e) => set({ history: e.currentTarget.checked ? 'field' : '', history_field: '' })} />
          {history && (
            <TextField label="In the field" value={r.history_field ?? ''} onChange={(v) => set({ history_field: v })}
              placeholder={defaultHistoryField(r.style ?? '') || 'reasoning_content'} mono
              hint={defaultHistoryField(r.style ?? '') ? 'Empty uses the parameter’s usual field.' : 'Required for this parameter.'} />
          )}
        </Stack>
      )}
    </Advanced>
  )
}
