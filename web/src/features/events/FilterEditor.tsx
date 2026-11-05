import { useId, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Plus, X } from 'lucide-react'
import { Button, uiStyles } from '@/components/ui'
import { Field, SelectField } from '@/components/form'
import { KeyValueEditor } from '@/components/KeyValueEditor'
import { eventCatalogQuery } from '@/lib/api/queries'
import type { EventFilter } from '@/lib/api/types'
import { eventPatternRE, matchingKinds, patternSuggestions } from './events'
import e from './events.module.css'

/** Edits an event filter: name globs (with catalog suggestions), minimum severity and subject matches. */
export function FilterEditor({ value, onChange, errors, legend = 'Events' }: {
  value: EventFilter
  onChange: (f: EventFilter, valid: boolean) => void
  errors?: Record<string, string | undefined>
  legend?: string
}) {
  const catalog = useQuery(eventCatalogQuery)
  const [draft, setDraft] = useState('')
  const [subjectsValid, setSubjectsValid] = useState(true)
  const listId = useId()
  const events = value.events ?? []
  const draftValid = draft === '' || eventPatternRE.test(draft)
  const add = () => {
    const p = draft.trim()
    if (!p || !eventPatternRE.test(p) || events.includes(p)) return
    onChange({ ...value, events: [...events, p] }, subjectsValid)
    setDraft('')
  }
  const kinds = catalog.data ? matchingKinds(value, catalog.data.items) : []
  return (
    <fieldset className={e.fieldset}>
      <legend className={e.legend}>{legend}</legend>
      <div className={e.chips} aria-label="Event patterns" role="list">
        {events.length === 0 && <span className="muted">All events</span>}
        {events.map((p) => (
          <span key={p} className={e.chip} role="listitem">
            {p}
            <button type="button" aria-label={`Remove ${p}`} onClick={() => onChange({ ...value, events: events.filter((x) => x !== p) }, subjectsValid)}>
              <X size={12} aria-hidden="true" />
            </button>
          </span>
        ))}
      </div>
      <Field label="Add event pattern" hint="An event name or glob: quota.*, routing.account.switched, *.breaker.opened."
        error={draftValid ? errors?.['events'] : 'Use lowercase name segments separated by dots; * is a wildcard.'}>
        {(p) => (
          <div className={e.addRow}>
            <input {...p} className={uiStyles.input} list={listId} value={draft} placeholder="quota.*" style={{ flex: '1 1 240px', maxWidth: 420 }}
              onChange={(ev) => setDraft(ev.target.value)}
              onKeyDown={(ev) => {
                if (ev.key === 'Enter') {
                  ev.preventDefault()
                  add()
                }
              }} />
            <Button icon={Plus} disabled={!draft.trim() || !draftValid} onClick={add}>Add</Button>
            <datalist id={listId}>
              {patternSuggestions(catalog.data?.items ?? []).map((s) => (
                <option key={s} value={s} />
              ))}
            </datalist>
          </div>
        )}
      </Field>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 12 }}>
        <SelectField label="Minimum severity" value={(value.min_severity || 'info') as 'info' | 'warning' | 'critical'}
          onChange={(v) => onChange({ ...value, min_severity: v }, subjectsValid)}
          options={[{ value: 'info', label: 'Info and above' }, { value: 'warning', label: 'Warning and above' }, { value: 'critical', label: 'Critical only' }]} />
        <KeyValueEditor label="Subjects (optional)" value={value.subjects ?? {}}
          hint="Only events about these identities, e.g. provider_id=prv_…  One key=value per line."
          onChange={(v, ok) => {
            setSubjectsValid(ok)
            onChange({ ...value, subjects: v }, ok)
          }} />
      </div>
      {catalog.data && (
        <span className={e.preview}>
          Matches {kinds.length} of {catalog.data.items.length} event kinds
          {kinds.length > 0 && kinds.length <= 8 ? `: ${kinds.map((k) => k.name).join(', ')}` : ''}.
        </span>
      )}
    </fieldset>
  )
}
