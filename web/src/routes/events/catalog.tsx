import { useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Text } from '@mantine/core'
import { Badge, SearchInput, Skeleton } from '@/components/ui'
import { QueryError } from '@/components/states'
import { eventCatalogQuery } from '@/lib/api/queries'
import { severityTone } from '@/features/events/events'
import { EventsTabs } from '@/features/events/EventsTabs'
import { CopyText, Section } from '@/ui/kit'
import e from '@/features/events/events.module.css'

export const Route = createFileRoute('/events/catalog')({
  component: Catalog,
})

const severityName: Record<string, string> = { info: 'Info', warning: 'Warning', critical: 'Critical' }

function Catalog() {
  const catalog = useQuery(eventCatalogQuery)
  const [q, setQ] = useState('')
  const needle = q.trim().toLowerCase()
  const items = (catalog.data?.items ?? []).filter((k) => !needle || k.name.includes(needle) || k.description.toLowerCase().includes(needle))
  return (
    <Section title="Event catalog" info="Every event kind, with its subjects (identities you can filter on) and data fields (available to message templates). Events never carry prompts or secrets."
      actions={<EventsTabs />}>
      <SearchInput value={q} onChange={setQ} placeholder="Filter event kinds" label="Filter event kinds" />
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
                <Text size="sm" fw={600}>
                  {k.description.replace(/\.$/, '')}
                </Text>
                <Badge tone={severityTone(k.severity)}>{severityName[k.severity] ?? k.severity}</Badge>
              </div>
              <CopyText value={k.name} maw="100%" />
              {k.subjects && k.subjects.length > 0 && (
                <span className={e.kindMeta}>About <span className="mono">{k.subjects.join(', ')}</span></span>
              )}
              {k.data && k.data.length > 0 && <span className={e.kindMeta}>Template fields <span className="mono">{k.data.join(', ')}</span></span>}
              {k.pair && <span className={e.kindMeta}>Recovers with <span className="mono">{k.pair}</span></span>}
              <span className={e.kindMeta}>CloudEvent type <span className="mono">{k.type}</span></span>
            </li>
          ))}
        </ul>
      )}
    </Section>
  )
}
