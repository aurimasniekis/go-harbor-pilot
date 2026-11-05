import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Radar } from 'lucide-react'
import { Badge, Banner, EmptyState, Panel, TableScroll } from '@/components/ui'
import { SelectField } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { ProtocolBadge } from '@/features/activity/bits'
import { protocolChangesQuery, resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { ProviderRes } from '@/lib/api/types'
import { fmtCompact, fmtDateTime, fmtRelative } from '@/lib/format'
import { changeKinds, confidenceTone, impactTone, prettyJSON } from './compat'
import c from './compat.module.css'

export function CoverageNote({ processed, dropped }: { processed: number; dropped: number }) {
  if (dropped > 0) {
    const pct = processed + dropped > 0 ? Math.round((processed / (processed + dropped)) * 100) : 0
    return (
      <Banner tone="warning" role="status">
        <strong>Reduced coverage.</strong> {fmtCompact(dropped)} observations were dropped under load since the server started ({pct}% processed).
        Forwarding is never delayed for observation, so some changes may be missing here.
      </Banner>
    )
  }
  return <p className="secondary">Coverage: {fmtCompact(processed)} observations processed since start, none dropped.</p>
}

/** The observed compatibility changelog. */
export function ChangesPanel() {
  const q = useQuery(protocolChangesQuery)
  const providers = useQuery(resourceListQuery<ProviderRes>('providers'))
  const prov = byId(providers.data?.items)
  const [kind, setKind] = useState('')
  const [impact, setImpact] = useState('')
  const items = useMemo(
    () => (q.data?.items ?? []).filter((x) => (!kind || x.kind === kind) && (!impact || x.impact === impact)),
    [q.data, kind, impact],
  )
  return (
    <Panel title="Observed changes"
      subtitle="An observed compatibility changelog — not a provider release history. Structure only: content values are never recorded.">
      <Banner tone="info">
        “Not observed recently” means only that: it is not proof a field or event was removed. “Expected field absent” is reported with low
        confidence for the same reason. Correlation with an incident is not causation.
      </Banner>
      {q.data && <CoverageNote processed={q.data.processed} dropped={q.data.dropped} />}
      <div className={c.filters}>
        <SelectField label="Kind" value={kind} onChange={setKind}
          options={[{ value: '', label: 'All kinds' }, ...Object.entries(changeKinds).map(([value, label]) => ({ value, label }))]} />
        <SelectField label="Impact" value={impact} onChange={setImpact}
          options={[{ value: '', label: 'All' }, { value: 'breaking', label: 'Breaking' }, { value: 'additive', label: 'Additive' }, { value: 'unknown', label: 'Unknown' }]} />
      </div>
      {q.isError ? (
        <QueryError error={q.error} retry={() => void q.refetch()} />
      ) : !q.data ? (
        <TableSkeleton rows={4} />
      ) : items.length === 0 ? (
        <EmptyState icon={Radar} title={q.data.items.length === 0 ? 'No protocol changes observed' : 'No changes match the filters'}>
          Changes appear when an upstream or client sends structure this build does not recognize, rejects a request schema, or recovers
          after a breaker.
        </EmptyState>
      ) : (
        <TableScroll label="Observed protocol changes">
          <table className="data-table" style={{ width: '100%' }}>
            <thead>
              <tr>
                <th>Change</th>
                <th>Impact</th>
                <th>Confidence</th>
                <th>Where</th>
                <th>Path</th>
                <th>Seen</th>
                <th>Count</th>
                <th>Recency</th>
              </tr>
            </thead>
            <tbody>
              {items.map((x) => (
                <tr key={x.id}>
                  <td>
                    <Badge tone={x.kind === 'restored' ? 'success' : 'neutral'}>{changeKinds[x.kind] ?? x.kind}</Badge>
                  </td>
                  <td><Badge tone={impactTone[x.impact] ?? 'neutral'}>{x.impact}</Badge></td>
                  <td><Badge tone={confidenceTone[x.confidence] ?? 'neutral'}>{x.confidence}</Badge></td>
                  <td>
                    <div className={c.inline}>
                      <Badge>{x.direction}</Badge> <ProtocolBadge p={x.protocol} />
                    </div>
                    <div className="muted">{prov[x.provider_id]?.name ?? x.provider_id ?? '—'}</div>
                    {x.client_harness && <div className="muted">client {x.client_harness}{x.client_version ? ` ${x.client_version}` : ''}</div>}
                  </td>
                  <td style={{ maxWidth: 360 }}>
                    <span className={c.path}>{x.path}</span>
                    <details>
                      <summary className="muted">Structural detail</summary>
                      <pre className={c.json}>{prettyJSON(x.detail)}</pre>
                    </details>
                  </td>
                  <td style={{ whiteSpace: 'nowrap' }}>
                    <div title={fmtDateTime(x.first_seen)}>first {fmtRelative(x.first_seen)}</div>
                    <div title={fmtDateTime(x.last_seen)} className="muted">last {fmtRelative(x.last_seen)}</div>
                  </td>
                  <td className="num">{fmtCompact(x.count)}</td>
                  <td>
                    {x.recency === 'current' ? (
                      <Badge tone="info">current</Badge>
                    ) : (
                      <Badge title="Not observed in the last 7 days. This is not proof of removal.">not observed recently</Badge>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableScroll>
      )}
      <p className="muted">Adapter version and endpoint identity are recorded per change for later correlation.</p>
    </Panel>
  )
}
