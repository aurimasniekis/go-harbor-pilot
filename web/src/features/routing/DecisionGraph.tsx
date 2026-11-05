import { ArrowRight, ChevronRight } from 'lucide-react'
import { Link } from '@tanstack/react-router'
import { Badge } from '@/components/ui'
import { BillingBadge } from '@/components/ResourceBadges'
import { fallbackSteps } from '@/features/config/constants'
import type { PlanDocument } from '@/lib/api/types'
import { parseEngine } from './engine'
import type { Lane } from './graph'
import r from './routing.module.css'

function Edge() {
  return (
    <div className={r.edge} aria-hidden="true">
      <ArrowRight size={16} />
    </div>
  )
}

function LaneView({ lane }: { lane: Lane }) {
  const alias = lane.chain[lane.chain.length - 1]
  return (
    <li className={r.lane} aria-label={`${lane.entry.kind === 'default' ? 'Default' : `Mapping ${lane.entry.match}`} route`}>
      <div className={r.node}>
        <span className={r.nodeKind}>{lane.entry.kind === 'default' ? 'Default' : 'Requested model'}</span>
        <span className="mono">{lane.entry.match || <span className="muted">(empty pattern)</span>}</span>
        {lane.entry.note && <span className="secondary">{lane.entry.note}</span>}
      </div>
      <Edge />
      <div className={lane.chain.length ? r.node : r.nodeMuted}>
        <span className={r.nodeKind}>Alias</span>
        {lane.chain.length === 0 ? (
          <span>{lane.problem ?? 'none'}</span>
        ) : (
          <span className="mono">
            {lane.chain.map((a, i) => (
              <span key={a.id}>
                {i > 0 && (
                  <>
                    <ChevronRight size={12} aria-hidden="true" />
                    <span className="visually-hidden">resolves to</span>
                  </>
                )}
                {a.name}
              </span>
            ))}
          </span>
        )}
        {lane.required.length > 0 && (
          <span>
            {lane.required.map((c) => (
              <Badge key={c} tone="info">
                needs {c}
              </Badge>
            ))}
          </span>
        )}
        {alias && lane.problem && <Badge tone="error">{lane.problem}</Badge>}
      </div>
      <Edge />
      <div className={r.members}>
        {lane.pool ? (
          <div className={r.node}>
            <span className={r.nodeKind}>Pool</span>
            <span>
              <span className="mono">{lane.pool.name}</span> · {lane.pool.spec.strategy || 'ordered'}
              {lane.pool.spec.affinity === 'session' && ' · session affinity'}
            </span>
          </div>
        ) : (
          <div className={r.nodeMuted}>
            <span className={r.nodeKind}>Pool</span>
            <span>{lane.problem ?? 'unresolved'}</span>
          </div>
        )}
        {lane.members.map((m) => (
          <div key={m.accountId + m.model} className={r.member} style={m.blocked.length ? { opacity: 0.65 } : undefined}>
            <span>
              <span className="secondary">P{m.priority}</span> {m.provider?.name ?? '?'} / {m.account?.name ?? <span className="mono">{m.accountId}</span>}{' '}
              · <span className="mono">{m.model}</span>
            </span>
            <span style={{ display: 'inline-flex', gap: 4, flexWrap: 'wrap' }}>
              {m.account && <BillingBadge value={m.account.spec.billing} />}
              {m.blocked.map((b) => (
                <Badge key={b} tone="error">
                  {b}
                </Badge>
              ))}
            </span>
          </div>
        ))}
        {lane.pool && lane.members.length === 0 && <span className="secondary">The pool has no members.</span>}
      </div>
    </li>
  )
}

/**
 * Read-only picture of how a plan document routes: requested model → alias
 * chain → pool → members, then rule stages and fallback steps.
 */
export function DecisionGraph({ doc, lanes }: { doc: PlanDocument; lanes: Lane[] }) {
  const fb = doc.fallback
  const engine = parseEngine(doc.engine)
  const reqRules = engine.rules.filter((x) => x.enabled && x.stage === 'request').length + engine.tables.filter((x) => x.enabled && x.stage === 'request').length
  const candRules = engine.rules.filter((x) => x.enabled && x.stage === 'candidate').length + engine.tables.filter((x) => x.enabled && x.stage === 'candidate').length
  return (
    <div className={r.graph}>
      {lanes.length === 0 ? (
        <p className="secondary">This version maps no models and has no default alias, so every request is rejected.</p>
      ) : (
        <ol style={{ listStyle: 'none', margin: 0, padding: 0, display: 'flex', flexDirection: 'column', gap: 'var(--space-3)' }} aria-label="Routes">
          {lanes.map((l) => (
            <LaneView key={l.key} lane={l} />
          ))}
        </ol>
      )}
      <div className={r.fallback} aria-label="Evaluation stages">
        <span className={r.nodeKind}>Stages</span>
        <Badge tone="info">{reqRules} request rule{reqRules === 1 ? '' : 's'}</Badge>
        <ChevronRight size={14} aria-hidden="true" />
        <Badge>hard restrictions</Badge>
        <ChevronRight size={14} aria-hidden="true" />
        <Badge>health</Badge>
        <ChevronRight size={14} aria-hidden="true" />
        <Badge tone="purple">{candRules} candidate rule{candRules === 1 ? '' : 's'}</Badge>
        <ChevronRight size={14} aria-hidden="true" />
        <Badge>{engine.score.trim() ? 'score expression' : 'pool order'}</Badge>
      </div>
      <div className={r.fallback} aria-label="Fallback steps">
        <span className={r.nodeKind}>Fallback</span>
        {fb.steps.length === 0 && <span className="secondary">none</span>}
        {fb.steps.map((s, i) => (
          <span key={`${s}-${i}`} style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
            {i > 0 && <ChevronRight size={14} aria-hidden="true" />}
            <Badge tone={s === 'stop' ? 'neutral' : 'accent'}>{fallbackSteps.find((x) => x.value === s)?.label ?? s}</Badge>
          </span>
        ))}
        <span className="secondary">· at most {fb.max_attempts} attempts</span>
        {fb.allow_paid_fallback ? (
          <Badge tone="warning">paid fallback allowed</Badge>
        ) : (
          <Badge tone="success" title="A metered API account may not follow a non-metered first choice.">no paid fallback</Badge>
        )}
      </div>
      <p className="secondary" style={{ margin: 0 }}>
        Members marked in red are removed by this plan's hard restrictions or configuration before ranking. Live account health is applied per request;
        use the simulator or the <Link to="/routing/pools" style={{ textDecoration: 'underline' }}>fallback groups</Link> to check it.
      </p>
    </div>
  )
}
