import type { AuditEntry } from '@/lib/api/types'

/** Plain names of configuration kinds, as they read in a sentence. */
export const kindNoun: Record<string, string> = {
  workspace: 'workspace',
  project: 'project',
  provider: 'provider',
  provider_account: 'provider account',
  subscription_entitlement: 'subscription',
  pool: 'fallback group',
  model_alias: 'model name',
  routing_plan: 'routing plan',
  client_binding: 'client',
  quota_monitor: 'quota tracker',
  budget: 'budget',
  privacy_policy: 'masking policy',
  trace_policy: 'content capture rule',
  event_destination: 'event relay',
  notification_channel: 'alert channel',
  notification_rule: 'alert rule',
  user: 'user',
  service_principal: 'automation account',
  oidc_provider: 'single sign-on provider',
}

/** Action groups offered as a filter (the value is the action prefix). */
export const actionGroups: { value: string; label: string }[] = [
  { value: 'auth.', label: 'Sign-in and sessions' },
  { value: 'user.', label: 'People' },
  { value: 'api_token.', label: 'Automation API tokens' },
  { value: 'service_principal.', label: 'Automation accounts' },
  { value: 'gateway_credential.', label: 'Client access tokens' },
  { value: 'routing_plan.', label: 'Routing plans' },
  { value: 'client_binding.', label: 'Clients' },
  { value: 'provider', label: 'Providers and accounts' },
  { value: 'budget.', label: 'Budgets' },
  { value: 'quota_monitor.', label: 'Quota tracking' },
  { value: 'privacy_policy.', label: 'Masking policies' },
  { value: 'trace', label: 'Captured content' },
  { value: 'notification', label: 'Alerts' },
  { value: 'events.', label: 'Event delivery' },
  { value: 'compat.', label: 'Diagnostics' },
  { value: 'system.', label: 'System and backups' },
]

export interface Names {
  /** Display name for an actor or target id; undefined when unknown. */
  name: (id: string | undefined | null) => string | undefined
}

function str(v: unknown): string | undefined {
  return typeof v === 'string' && v !== '' ? v : typeof v === 'number' ? String(v) : undefined
}

/** A short form of an unknown id, e.g. "usr_01m6…h3k2". */
export function shortId(id: string): string {
  if (id.length <= 14) return id
  return `${id.slice(0, 8)}…${id.slice(-4)}`
}

export function actorName(e: AuditEntry, n: Names): string {
  if (e.actor_kind === 'system' || e.actor_id === 'system' || !e.actor_id) return 'System'
  return n.name(e.actor_id) ?? shortId(e.actor_id)
}

/** Name of the target: resolved name, then the name recorded in details, then a short id. */
export function targetName(e: AuditEntry, n: Names): string {
  const d = (e.details ?? {}) as Record<string, unknown>
  return n.name(e.target_id) ?? str(d.name) ?? str(d.email) ?? (e.target_id ? shortId(e.target_id) : 'something')
}

const verbs: Record<string, string> = { create: 'created', update: 'changed', delete: 'deleted' }

/**
 * One readable sentence for an audit entry, e.g. "Demo Admin signed in with
 * a password" or "System issued an access token for claude-code-laptop".
 * The dotted action name stays available in the details.
 */
export function sentence(e: AuditEntry, n: Names): string {
  const who = actorName(e, n)
  const what = targetName(e, n)
  const d = (e.details ?? {}) as Record<string, unknown>
  const a = e.action
  const version = str(d.version)
  switch (a) {
    case 'auth.login': {
      const m = str(d.method)
      return `${who} signed in${m === 'password' ? ' with a password' : m === 'oidc' ? ' with single sign-on' : m ? ` (${m})` : ''}`
    }
    case 'auth.logout':
      return `${who} signed out`
    case 'auth.bootstrap':
      return `${who} set up the first administrator`
    case 'auth.breakglass':
      return 'Break-glass recovery was used'
    case 'auth.session.revoke':
      return `${who} ended a session`
    case 'auth.settings.update':
      return `${who} changed sign-in settings`
    case 'api_token.issue':
      return `${who} issued an API token for ${what}`
    case 'api_token.revoke':
      return `${who} revoked an API token of ${what}`
    case 'gateway_credential.issue':
      return `${who} issued an access token for ${what}`
    case 'gateway_credential.revoke':
      return `${who} revoked an access token of ${what}`
    case 'user.create':
      return `${who} invited ${what}`
    case 'user.update':
      return `${who} changed ${what}`
    case 'user.delete':
      return `${who} removed ${what}`
    case 'user.grants.set':
      return `${who} changed content access for ${what}`
    case 'user.identity.link':
      return `${who} linked a single sign-on identity to ${what}`
    case 'user.identity.unlink':
      return `${who} unlinked a single sign-on identity from ${what}`
    case 'user.password_reset.issue':
      return `${who} created a password reset link for ${what}`
    case 'user.password.change':
      return `${who} changed their password`
    case 'user.sessions.revoke':
      return `${who} signed ${what} out everywhere`
    case 'routing_plan.publish':
      return `${who} published ${what}${version ? ` v${version}` : ''}`
    case 'routing_plan.version.create':
      return `${who} saved a draft of ${what}${version ? ` (v${version})` : ''}`
    case 'routing_plan.version.update':
      return `${who} edited a draft of ${what}`
    case 'routing_plan.version.discard':
      return `${who} discarded a draft of ${what}`
    case 'compat.breaker.override':
      return `${who} changed a protocol breaker`
    case 'compat.breaker.probe':
      return `${who} probed a protocol path`
    case 'config.import':
      return `${who} imported configuration`
    case 'events.replay':
      return `${who} re-sent events to ${what}`
    case 'notification.replay':
      return `${who} retried a notification`
    case 'notification.test':
      return `${who} sent a test alert to ${what}`
    case 'protocol_watch.work_item.update':
      return `${who} updated a protocol change`
    case 'security_alert.acknowledge':
      return `${who} acknowledged a security alert`
    case 'system.backup':
      return `${who} took a backup`
  }
  if (a.startsWith('auth.accept_')) return a.includes('invite') ? `${who} accepted an invitation` : `${who} set a new password`
  if (a.startsWith('auth.oidc_provider.')) {
    const op = a.split('.').pop() ?? ''
    return `${who} ${op === 'create' ? 'added' : op === 'delete' ? 'removed' : 'changed'} single sign-on provider ${what}`
  }
  if (a.startsWith('service_principal.')) {
    const op = a.split('.').pop() ?? ''
    return `${who} ${verbs[op] ?? op} automation account ${what}`
  }
  if (a.startsWith('trace')) return `${who} ${a.includes('export') ? 'exported' : a.includes('delete') ? 'deleted' : a.includes('search') ? 'searched' : 'viewed'} captured content`
  // Configuration changes: "<kind>.<create|update|delete>".
  const dot = a.lastIndexOf('.')
  if (dot > 0) {
    const kind = a.slice(0, dot)
    const op = a.slice(dot + 1)
    const noun = kindNoun[kind]
    if (noun && verbs[op]) return `${who} ${verbs[op]} ${noun} ${what}`
  }
  const plain = a.replaceAll('_', ' ').replaceAll('.', ' ')
  return `${who}: ${plain}${e.target_id ? ` (${what})` : ''}`
}

/** Flattens details into label/value pairs for display (no raw JSON). */
export function detailPairs(details: unknown): [string, string][] {
  const out: [string, string][] = []
  const walk = (v: unknown, path: string) => {
    if (v === null || v === undefined || v === '') return
    if (Array.isArray(v)) {
      if (v.length === 0) return
      if (v.every((x) => typeof x !== 'object' || x === null)) out.push([path, v.join(', ')])
      else v.forEach((x, i) => walk(x, `${path} ${i + 1}`))
      return
    }
    if (typeof v === 'object') {
      for (const [k, x] of Object.entries(v as Record<string, unknown>)) walk(x, path ? `${path} › ${label(k)}` : label(k))
      return
    }
    out.push([path, String(v)])
  }
  walk(details, '')
  return out
}

function label(k: string): string {
  const t = k.replaceAll('_', ' ')
  return t.charAt(0).toUpperCase() + t.slice(1)
}
