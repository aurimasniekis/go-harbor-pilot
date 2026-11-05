import { useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Badge, Panel, SearchInput, Skeleton } from '@/components/ui'
import { QueryError } from '@/components/states'
import { eventCatalogQuery } from '@/lib/api/queries'
import { severityTone } from '@/features/events/events'
import e from '@/features/events/events.module.css'

export const Route = createFileRoute('/events/catalog')({
  component: Catalog,
})

function Catalog() {
  const catalog = useQuery(eventCatalogQuery)
  const [q, setQ] = useState('')
  const needle = q.trim().toLowerCase()
  const items = (catalog.data?.items ?? []).filter((k) => !needle || k.name.includes(needle) || k.description.toLowerCase().includes(needle))
  return (
    <Panel title="Event catalog" subtitle="Every event kind, with its subjects (identities you can filter on) and data fields (available to templates). Events never carry prompts or secrets."
      actions={<SearchInput value={q} onChange={setQ} placeholder="Filter kinds" label="Filter event kinds" />}>
      {catalog.isError ? (
        <QueryError error={catalog.error} retry={() => void catalog.refetch()} />
      ) : !catalog.data ? (
        <Skeleton height={160} />
      ) : items.length === 0 ? (
        <p className="muted">No event kinds match “{q}”.</p>
      ) : (
        <ul className={e.kindList} aria-label="Event kinds" style={{ listStyle: 'none', margin: 0, padding: 0 }}>
          {items.map((k) => (
            <li key={k.name} className={e.kind}>
              <div className={e.kindHead}>
                <strong className="mono" style={{ overflowWrap: 'anywhere' }}>{k.name}</strong>
                <Badge tone={severityTone(k.severity)}>{k.severity}</Badge>
              </div>
              <span className="secondary">{k.description}</span>
              <span className={e.kindMeta}>Type <span className="mono">{k.type}</span></span>
              {k.subjects && k.subjects.length > 0 && (
                <span className={e.kindMeta}>Subjects <span className="mono">{k.subjects.join(', ')}</span></span>
              )}
              {k.data && k.data.length > 0 && <span className={e.kindMeta}>Data <span className="mono">{k.data.join(', ')}</span></span>}
              {k.pair && <span className={e.kindMeta}>Resolved by <span className="mono">{k.pair}</span></span>}
            </li>
          ))}
        </ul>
      )}
    </Panel>
  )
}
