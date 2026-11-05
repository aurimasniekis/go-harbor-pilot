import { ArrowRight, ChevronRight } from 'lucide-react'
import { Link } from '@tanstack/react-router'
import { Badge } from '@/components/ui'
import { BillingBadge } from '@/components/ResourceBadges'
import type { PlanDocument } from '@/lib/api/types'
import { parseEngine } from './engine'
import type { Lane } from './graph'
import r from './routing.module.css'
import { orderedMembers } from './flow'
import { accountLabel } from './names'
import { plural } from '@/ui/glossary'

const strategyWords: Record<string, string> = {
  ordered: 'tried in order',
  weighted: 'spread by weight',
  reset_soonest: 'quota that resets soonest first',
  least_used: 'least used first',
  round_robin: 'take turns',
}

const stepWords: Record<string, string> = {
  retry: 'Retry the same account',
  alternate_account: 'Another account, same model',
  alternate_model: 'Another model in the group',
  alternate_provider: 'Another provider',
  wait: 'Wait for capacity',
}

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
        <span className={r.nodeKind}>Model name</span>
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
            <span className={r.nodeKind}>Fallback group</span>
            <span>
              {lane.pool.spec.display_name || lane.pool.name} · {strategyWords[lane.pool.spec.strategy || 'ordered'] ?? lane.pool.spec.strategy}
              {lane.pool.spec.affinity === 'session' && ' · keeps a conversation on one account'}
            </span>
          </div>
        ) : (
          <div className={r.nodeMuted}>
            <span className={r.nodeKind}>Fallback group</span>
            <span>{lane.problem ?? 'unresolved'}</span>
          </div>
        )}
        {orderedMembers(lane).map((m, i) => (
          <div key={m.accountId + m.model} className={r.member} style={m.blocked.length ? { opacity: 0.65 } : undefined}>
            <span>
              <span className="secondary">{i + 1}.</span> {accountLabel(m.account, m.provider ? { [m.provider.id]: m.provider } : {}, m.accountId)}{' '}
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
        {lane.pool && lane.members.length === 0 && <span className="secondary">The fallback group has no accounts.</span>}
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
        <p className="secondary">This version maps no models and has nothing for other models, so every request is rejected.</p>
      ) : (
        <ol style={{ listStyle: 'none', margin: 0, padding: 0, display: 'flex', flexDirection: 'column', gap: 'var(--space-3)' }} aria-label="Routes">
          {lanes.map((l) => (
            <LaneView key={l.key} lane={l} />
          ))}
        </ol>
      )}
      <div className={r.fallback} aria-label="Evaluation stages">
        <span className={r.nodeKind}>Order</span>
        <Badge tone="info">{plural(reqRules, 'rule')} before choosing an account</Badge>
        <ChevronRight size={14} aria-hidden="true" />
        <Badge>hard restrictions</Badge>
        <ChevronRight size={14} aria-hidden="true" />
        <Badge>health</Badge>
        <ChevronRight size={14} aria-hidden="true" />
        <Badge tone="purple">{plural(candRules, 'rule')} for each account</Badge>
        <ChevronRight size={14} aria-hidden="true" />
        <Badge>{engine.score.trim() ? 'score expression' : 'fallback group order'}</Badge>
      </div>
      <div className={r.fallback} aria-label="Fallback steps">
        <span className={r.nodeKind}>Fallback</span>
        {fb.steps.length === 0 && <span className="secondary">none</span>}
        {fb.steps.map((s, i) => (
          <span key={`${s}-${i}`} style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
            {i > 0 && <ChevronRight size={14} aria-hidden="true" />}
            <Badge tone={s === 'stop' ? 'neutral' : 'accent'}>{s === 'stop' ? 'Stop' : (stepWords[s] ?? s)}</Badge>
          </span>
        ))}
        <span className="secondary">· at most {fb.max_attempts} attempts</span>
        {fb.allow_paid_fallback ? (
          <Badge tone="warning">paid fallback allowed</Badge>
        ) : (
          <Badge tone="success" title="A metered API account may not follow a non-metered first choice.">no paid fallback</Badge>
        )}
      </div>
      {lanes.some((l) => l.members.some((m) => m.blocked.length > 0)) && (
        <p className="secondary" style={{ margin: 0 }}>
          Accounts marked in red are ruled out by this plan's hard restrictions or configuration. Live account health is applied per request; use Test a request
          or the <Link to="/routing/pools" style={{ textDecoration: 'underline' }}>fallback groups</Link> to check it.
        </p>
      )}
    </div>
  )
}
