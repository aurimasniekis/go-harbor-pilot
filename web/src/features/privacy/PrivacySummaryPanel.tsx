import { useQuery } from '@tanstack/react-query'
import { AlertTriangle } from 'lucide-react'
import { Badge, Banner, KeyValues, Panel, TableScroll } from '@/components/ui'
import { resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { PrivacyPolicyRes, PrivacySummary } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { actionTone, detectorLabel } from './privacy'

/** Narrows the request detail's free-form privacy JSON. */
export function asPrivacySummary(v: unknown): PrivacySummary | null {
  if (!v || typeof v !== 'object') return null
  const s = v as Partial<PrivacySummary>
  if (!s.policy_id) return null
  return { masked: 0, warned: 0, allowed: 0, blocked: false, uninspected_parts: 0, unmasked_fallback: false, rules: [], ...s } as PrivacySummary
}

/** Masking summary of one request: rule names and counts, never values. */
export function PrivacySummaryPanel({ privacy }: { privacy: unknown }) {
  const s = asPrivacySummary(privacy)
  const canConfig = useCan('config.read')
  const policies = useQuery({ ...resourceListQuery<PrivacyPolicyRes>('privacy-policies'), enabled: canConfig && !!s })
  const name = s ? (byId(policies.data?.items)[s.policy_id]?.name ?? s.policy_id) : ''
  return (
    <Panel title="Privacy" subtitle="Outbound masking applied before the request left the gateway. Only rule names and counts are recorded.">
      {!s ? (
        <p className="secondary">No privacy policy applied to this request.</p>
      ) : (
        <>
          {s.blocked && <Banner tone="warning">The privacy policy blocked this request; nothing was sent upstream.</Banner>}
          {s.unmasked_fallback && (
            <Banner tone="warning" icon={AlertTriangle}>
              Masking could not run and the policy permitted availability-first dispatch, so this request was sent unmasked.
            </Banner>
          )}
          <KeyValues
            items={[
              ['Policy', <span className="mono">{name}</span>],
              ['Masked', <span className="num">{s.masked}</span>],
              ['Warned', <span className="num">{s.warned}</span>],
              ['Allowed by exemptions', <span className="num">{s.allowed}</span>],
              ['Not inspected (images, documents)', <span className="num">{s.uninspected_parts}</span>],
            ]}
          />
          {(s.rules ?? []).length > 0 && (
            <TableScroll label="Detections by rule">
              <table className="data-table" style={{ width: '100%' }}>
                <thead>
                  <tr>
                    <th>Detector or rule</th>
                    <th>Action</th>
                    <th>Count</th>
                  </tr>
                </thead>
                <tbody>
                  {s.rules.map((r) => (
                    <tr key={`${r.rule}-${r.action}`}>
                      <td>{detectorLabel(r.rule)} <span className="mono muted">{r.rule}</span></td>
                      <td><Badge tone={actionTone(r.action)}>{r.action}</Badge></td>
                      <td className="num">{r.count}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </TableScroll>
          )}
        </>
      )}
    </Panel>
  )
}
