import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { FlaskConical, ShieldOff, SlidersHorizontal } from 'lucide-react'
import { Badge, Banner, Button, EmptyState, KeyValues, Panel, TableScroll } from '@/components/ui'
import { Dialog, MutationError, SelectField, TextField } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { ProtocolBadge } from '@/features/activity/bits'
import { ApiError, apiSend } from '@/lib/api/client'
import { breakersQuery, resourceListQuery } from '@/lib/api/queries'
import { byId } from '@/lib/api/resources'
import type { Breaker, ProviderRes } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { fmtDateTime, fmtRelative } from '@/lib/format'
import { breakerLabel, breakerTone, countdown, effectiveState, splitPath } from './compat'
import { useNow } from './useNow'
import c from './compat.module.css'

export function BreakerState({ b }: { b: Breaker }) {
  const eff = effectiveState(b.state, b.override)
  return (
    <span className={c.inline}>
      <Badge tone={breakerTone[eff] ?? 'neutral'}>{breakerLabel[eff] ?? eff}</Badge>
      {b.override !== 'auto' && (
        <Badge tone="purple" title={b.override_reason ? `Reason: ${b.override_reason}` : undefined}>
          {b.override === 'force_open' ? 'forced open' : 'forced closed'}
        </Badge>
      )}
    </span>
  )
}

export function PathLabel({ path }: { path: string }) {
  const { inbound, upstream } = splitPath(path)
  return (
    <span className={c.inline}>
      <ProtocolBadge p={inbound} /> <span aria-hidden="true">→</span>
      <span className="visually-hidden">to</span> <ProtocolBadge p={upstream} />
    </span>
  )
}

const overrideOptions = [
  { value: 'auto', label: 'Automatic (thresholds, cooldown, probes)' },
  { value: 'force_open', label: 'Force open (stop normal routing on this path)' },
  { value: 'force_closed', label: 'Force closed (ignore failures on this path)' },
] as const

type Override = (typeof overrideOptions)[number]['value']

function ManageBreaker({ b, providerName, onClose }: { b: Breaker; providerName: string; onClose: () => void }) {
  const qc = useQueryClient()
  const canManage = useCan('compat.manage')
  const [override, setOverride] = useState<Override>(b.override)
  const [reason, setReason] = useState('')
  const [confirm, setConfirm] = useState<'override' | 'probe' | null>(null)
  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: ['compat'] })
  }
  const ov = useMutation({
    mutationFn: () => apiSend<Breaker>('POST', '/compat/breakers/override', { provider_id: b.provider_id, path: b.path, override, reason: reason.trim() }),
    onSuccess: () => {
      setConfirm(null)
      invalidate()
    },
  })
  const probe = useMutation({
    mutationFn: () => apiSend<{ breaker: Breaker; result: 'succeeded' | 'failed'; error?: string }>('POST', '/compat/breakers/probe', { provider_id: b.provider_id, path: b.path }),
    onSuccess: () => {
      setConfirm(null)
      invalidate()
    },
  })
  const eff = effectiveState(b.state, b.override)
  const reasonMissing = override !== b.override && reason.trim() === ''
  return (
    <Dialog open wide title={`Breaker: ${providerName}`} onClose={onClose}>
      <KeyValues
        items={[
          ['Path', <PathLabel path={b.path} />],
          ['State', <BreakerState b={b} />],
          ['Recent failures', <span className="num">{b.recent_failures}</span>],
          ['Last failure', b.last_class ? <span>{b.last_class}{b.last_reason ? ` — ${b.last_reason}` : ''}</span> : <span className="muted">—</span>],
          ['Override reason', b.override_reason || <span className="muted">—</span>],
        ]}
      />
      {!canManage ? (
        <Banner tone="info">Changing breakers needs the compat.manage permission.</Banner>
      ) : (
        <>
          <h3 style={{ fontSize: 'var(--text-md)' }}>Override</h3>
          <SelectField label="Mode" value={override} onChange={(v) => { setOverride(v); setConfirm(null) }} options={[...overrideOptions]} />
          <TextField label="Reason" value={reason} onChange={setReason} maxLength={500} required
            hint="Recorded in the audit log and shown with the breaker." error={reasonMissing && confirm === 'override' ? 'A reason is required' : undefined} />
          {override === 'force_closed' && override !== b.override && (
            <Banner tone="warning">Forcing closed sends requests through the normal path even if the upstream is still incompatible.</Banner>
          )}
          {confirm === 'override' && !reasonMissing ? (
            <div className={c.confirm} role="group" aria-label="Confirm override">
              <span>
                Set this breaker to <strong>{overrideOptions.find((o) => o.value === override)?.label}</strong>?
              </span>
              <Button variant="primary" busy={ov.isPending} onClick={() => ov.mutate()}>Confirm override</Button>
              <Button variant="ghost" onClick={() => setConfirm(null)}>Cancel</Button>
            </div>
          ) : (
            <div>
              <Button icon={SlidersHorizontal} disabled={override === b.override && reason.trim() === ''} onClick={() => setConfirm('override')}>
                Apply override
              </Button>
            </div>
          )}
          <MutationError error={ov.error} />

          <h3 style={{ fontSize: 'var(--text-md)' }}>Live canary</h3>
          <p className="secondary">
            A live canary sends <strong>one minimal synthetic request</strong> (“Reply with OK.”) through the normal, inspected path using an
            account of this provider. It consumes a few tokens on that account and never replays client content. Success closes the breaker;
            failure re-opens it with a longer cooldown.
          </p>
          {eff === 'closed' ? (
            <p className="muted">The breaker is closed; a canary is only needed while it is open or half-open.</p>
          ) : confirm === 'probe' ? (
            <div className={c.confirm} role="group" aria-label="Confirm live canary">
              <span>Send one synthetic request to this provider now?</span>
              <Button variant="primary" icon={FlaskConical} busy={probe.isPending} onClick={() => probe.mutate()}>Run live canary</Button>
              <Button variant="ghost" onClick={() => setConfirm(null)}>Cancel</Button>
            </div>
          ) : (
            <div>
              <Button icon={FlaskConical} onClick={() => setConfirm('probe')}>Run live canary…</Button>
            </div>
          )}
          {probe.data && (
            <Banner tone={probe.data.result === 'succeeded' ? 'info' : 'error'} role="status">
              Canary {probe.data.result}
              {probe.data.error ? `: ${probe.data.error}` : '.'}
            </Banner>
          )}
          {probe.error instanceof ApiError && probe.error.status === 409 ? (
            <Banner tone="warning">The breaker closed or another probe is already running.</Banner>
          ) : (
            <MutationError error={probe.error} />
          )}
        </>
      )}
    </Dialog>
  )
}

export function BreakersPanel() {
  const q = useQuery(breakersQuery)
  const providers = useQuery(resourceListQuery<ProviderRes>('providers'))
  const prov = byId(providers.data?.items)
  const now = useNow()
  const [managing, setManaging] = useState<string | null>(null)
  const items = q.data?.items ?? []
  const current = items.find((b) => b.key === managing)
  return (
    <Panel title="Circuit breakers" subtitle="One breaker per provider and protocol path. Only compatibility failures count — not authentication errors, rate limits or outages.">
      {q.isError ? (
        <QueryError error={q.error} retry={() => void q.refetch()} />
      ) : !q.data ? (
        <TableSkeleton rows={3} />
      ) : items.length === 0 ? (
        <EmptyState icon={ShieldOff} title="No breakers yet">
          A breaker appears when a provider path first records a compatibility failure, such as unparseable upstream output or a schema
          rejection. Until then every path routes normally.
        </EmptyState>
      ) : (
        <TableScroll label="Circuit breakers">
          <table className="data-table" style={{ width: '100%' }}>
            <thead>
              <tr>
                <th>Provider</th>
                <th>Path</th>
                <th>State</th>
                <th>Next probe</th>
                <th>Recent failures</th>
                <th>Last failure</th>
                <th>Updated</th>
                <th><span className="visually-hidden">Actions</span></th>
              </tr>
            </thead>
            <tbody>
              {items.map((b) => {
                const name = prov[b.provider_id]?.name ?? b.provider_id
                const eff = effectiveState(b.state, b.override)
                return (
                  <tr key={b.key}>
                    <td>{name}</td>
                    <td><PathLabel path={b.path} /></td>
                    <td><BreakerState b={b} /></td>
                    <td title={b.retry_at ? fmtDateTime(b.retry_at, { seconds: true }) : undefined}>
                      {eff === 'open' && b.override === 'auto' ? countdown(b.retry_at, now) : <span className="muted">—</span>}
                    </td>
                    <td className="num">{b.recent_failures}</td>
                    <td style={{ maxWidth: 320 }}>
                      {b.last_class ? (
                        <>
                          <Badge>{b.last_class}</Badge> <span className="secondary">{b.last_reason}</span>
                        </>
                      ) : (
                        <span className="muted">—</span>
                      )}
                    </td>
                    <td title={fmtDateTime(b.updated_at)}>{fmtRelative(b.updated_at)}</td>
                    <td>
                      <Button size="small" icon={SlidersHorizontal} onClick={() => setManaging(b.key)} aria-label={`Manage breaker for ${name} ${b.path}`}>
                        Manage
                      </Button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </TableScroll>
      )}
      {current && <ManageBreaker b={current} providerName={prov[current.provider_id]?.name ?? current.provider_id} onClose={() => setManaging(null)} />}
    </Panel>
  )
}
