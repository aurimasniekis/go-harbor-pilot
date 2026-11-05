/**
 * The one vocabulary of the UI. Internal and API names (binding, service
 * principal, destination, dead-lettered) map to the words operators see.
 * Use these instead of writing labels inline, so a flow never switches
 * names halfway through.
 */

export const TERMS = {
  /** client_binding: a tool or app connected to the gateway. */
  client: { one: 'client', many: 'clients' },
  /** gateway_credential: the token a client sends to the gateway. */
  accessToken: { one: 'access token', many: 'access tokens' },
  /** service_principal: a non-human user of the management API. */
  automationAccount: { one: 'automation account', many: 'automation accounts' },
  /** service principal token: what an automation account signs in with. */
  apiToken: { one: 'API token', many: 'API tokens' },
  /** event_destination: a webhook or Pulse target that receives every matching event. */
  eventRelay: { one: 'event relay', many: 'event relays' },
  /** notification_channel: where alerts are sent (Slack, Telegram, …). */
  channel: { one: 'channel', many: 'channels' },
  /** notification_rule: which events alert which channels. */
  alertRule: { one: 'alert rule', many: 'alert rules' },
  /** subscription_entitlement. */
  subscription: { one: 'subscription', many: 'subscriptions' },
  /** model_alias. */
  modelName: { one: 'model name', many: 'model names' },
  /** pool. */
  fallbackGroup: { one: 'fallback group', many: 'fallback groups' },
  /** quota_monitor. */
  quotaTracker: { one: 'quota tracker', many: 'quota trackers' },
  /** privacy_policy. */
  maskingPolicy: { one: 'masking policy', many: 'masking policies' },
  /** trace_policy. */
  captureRule: { one: 'content capture rule', many: 'content capture rules' },
  /** trace. */
  capturedContent: { one: 'captured request', many: 'captured content' },
  /** harness. */
  tool: { one: 'tool', many: 'tools' },
  /** compat breaker. */
  pausedPath: { one: 'paused protocol path', many: 'paused protocol paths' },
} as const

export type Term = keyof typeof TERMS

/** "1 attempt", "3 attempts". */
export function plural(n: number, one: string, many = `${one}s`): string {
  return `${n.toLocaleString()} ${n === 1 ? one : many}`
}

/** Counted term: count(2, 'client') → "2 clients". */
export function count(n: number, term: Term): string {
  return plural(n, TERMS[term].one, TERMS[term].many)
}

/** Capitalized singular, for titles: title('eventRelay') → "Event relay". */
export function title(term: Term, many = false): string {
  const w = many ? TERMS[term].many : TERMS[term].one
  return w.charAt(0).toUpperCase() + w.slice(1)
}

/** Delivery states of event relays and alerts, in plain words. */
export const deliveryStatus: Record<string, string> = {
  pending: 'Waiting',
  deferred: 'Retrying',
  sending: 'Sending',
  delivered: 'Delivered',
  sent: 'Sent',
  digest: 'Held for digest',
  digested: 'Sent in a digest',
  dead: 'Failed',
}

/** Who last changed a managed object. */
export const managedByName: Record<string, string> = {
  ui: 'Web UI',
  api: 'API',
  terraform: 'Terraform',
  cli: 'CLI',
  import: 'Import',
  demo: 'Demo data',
}

/** Resource kinds as they appear in references and errors. */
export const kindName: Record<string, string> = {
  workspace: 'workspace',
  project: 'project',
  provider: 'provider',
  provider_account: 'provider account',
  subscription_entitlement: TERMS.subscription.one,
  pool: TERMS.fallbackGroup.one,
  model_alias: TERMS.modelName.one,
  routing_plan: 'routing plan',
  client_binding: TERMS.client.one,
  quota_monitor: TERMS.quotaTracker.one,
  budget: 'budget',
  privacy_policy: TERMS.maskingPolicy.one,
  trace_policy: TERMS.captureRule.one,
  event_destination: TERMS.eventRelay.one,
  notification_channel: TERMS.channel.one,
  notification_rule: TERMS.alertRule.one,
}
