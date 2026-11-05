import type { Tone } from '@/components/ui'
import type { ChannelSpec, DestinationSpec, EventFilter, EventKindInfo, NotificationRuleSpec } from '@/lib/api/types'

export const severities = ['info', 'warning', 'critical'] as const

export function severityTone(s: string): Tone {
  return s === 'critical' ? 'error' : s === 'warning' ? 'warning' : 'info'
}

/**
 * Mirrors the server's event-filter matching: dotted globs where `*`
 * matches one segment, and a trailing `.*` matches any deeper name.
 */
export function globMatches(pattern: string, name: string): boolean {
  if (pattern.endsWith('.*') && name.startsWith(pattern.slice(0, -1))) return true
  const p = pattern.split('.')
  const n = name.split('.')
  if (p.length !== n.length) return false
  return p.every((seg, i) => {
    const re = new RegExp('^' + seg.split('*').map((x) => x.replace(/[.+?^${}()|[\]\\]/g, '\\$&')).join('[^.]*') + '$')
    return re.test(n[i] ?? '')
  })
}

/** Catalog kinds that a filter would accept (ignoring subjects). */
export function matchingKinds(f: EventFilter, catalog: EventKindInfo[]): EventKindInfo[] {
  const minIdx = severities.indexOf((f.min_severity || 'info') as (typeof severities)[number])
  return catalog.filter((k) => {
    const sev = severities.indexOf(k.severity as (typeof severities)[number])
    if (sev >= 0 && sev < minIdx) return false
    const pats = f.events ?? []
    return pats.length === 0 || pats.some((p) => globMatches(p, k.name))
  })
}

/** Suggested patterns: every event name plus one wildcard per area. */
export function patternSuggestions(catalog: EventKindInfo[]): string[] {
  const out = new Set<string>()
  for (const k of catalog) {
    if (k.name.includes('<')) continue
    out.add(k.name.split('.')[0] + '.*')
    out.add(k.name)
  }
  return [...out].sort()
}

export const eventPatternRE = /^[a-z0-9_*-]+(\.[a-z0-9_*-]+){0,2}$/

export const hhmmRE = /^([01]\d|2[0-3]):[0-5]\d$/

export function defaultFilter(): EventFilter {
  return { events: [], min_severity: 'info', subjects: {} }
}

export function defaultDestination(): DestinationSpec {
  return { type: 'webhook', enabled: true, filter: defaultFilter(), webhook_url: '', allow_private_network: false, timeout_seconds: 10, headers: {}, max_attempts: 50, max_age_hours: 24 }
}

export function defaultChannel(): ChannelSpec {
  return { type: 'slack', enabled: true, timezone: '', quiet_hours: { enabled: false, start: '22:00', end: '07:00', critical_bypass: true }, digest_minutes: 0, rate_per_minute: 20 }
}

export function defaultRule(): NotificationRuleSpec {
  return { enabled: true, filter: { ...defaultFilter(), min_severity: 'warning' }, channel_ids: [], group_seconds: 0, cooldown_seconds: 900, recovery: true, template: '' }
}

export const channelTypes: {
  value: ChannelSpec['type']
  label: string
  blurb: string
  secret: string
  secretHint: string
  /** Variable name suffix for the env:HP_SECRET_… example. */
  refExample: string
  /** Provider API address, as the placeholder of the override. */
  apiBase: string
}[] = [
  { value: 'slack', label: 'Slack', blurb: 'Post to a channel through an incoming webhook.', secret: 'Incoming webhook URL',
    secretHint: 'From Slack: Apps → Incoming Webhooks. Starts with https://hooks.slack.com/services/. Stored encrypted; never shown again.',
    refExample: 'SLACK_WEBHOOK', apiBase: 'https://hooks.slack.com' },
  { value: 'discord', label: 'Discord', blurb: 'Post to a channel through a webhook. Mentions are disabled.', secret: 'Webhook URL',
    secretHint: 'From Discord: channel settings → Integrations → Webhooks. Starts with https://discord.com/api/webhooks/. Stored encrypted.',
    refExample: 'DISCORD_WEBHOOK', apiBase: 'https://discord.com' },
  { value: 'telegram', label: 'Telegram', blurb: 'A bot sends messages to a chat, group or channel.', secret: 'Bot token',
    secretHint: 'From @BotFather, for example 123456:ABC… Stored encrypted; never shown again.',
    refExample: 'TELEGRAM_BOT_TOKEN', apiBase: 'https://api.telegram.org' },
  { value: 'webhook', label: 'Signed webhook', blurb: 'POST each alert as JSON to your own endpoint, signed with HMAC.', secret: 'Signing secret',
    secretHint: 'Optional. Receivers verify the X-Harbor-Signature header with the same secret.',
    refExample: 'WEBHOOK_SIGNING_SECRET', apiBase: '' },
]

/**
 * A plain-language problem with a pasted Slack or Discord webhook URL, or
 * undefined when it looks right (or an override or private network is in use).
 */
export function secretShapeError(type: ChannelSpec['type'], value: string, custom: boolean): string | undefined {
  const v = value.trim()
  if (custom) return undefined
  if (type === 'slack' && !/^https:\/\/hooks\.slack\.com\/services\/\S+$/.test(v)) return 'A Slack webhook URL starts with https://hooks.slack.com/services/. For a test receiver, turn on “Webhook is on a private network” under Delivery.'
  if (type === 'discord' && !/^https:\/\/(discord|discordapp)\.com\/api\/webhooks\/\S+$/.test(v)) return 'A Discord webhook URL starts with https://discord.com/api/webhooks/. For a test receiver, turn on “Webhook is on a private network” under Delivery.'
  if (type === 'telegram' && !/^\d+:\S{8,}$/.test(v)) return 'A bot token looks like 123456789:AA… (digits, a colon, then letters).'
  return undefined
}

/** "Sends immediately · at most 20 per min · quiet 22:00–07:00 Europe/Vilnius" */
export function deliverySummary(spec: ChannelSpec): string {
  const parts: string[] = []
  const qh = spec.quiet_hours
  parts.push(spec.digest_minutes ? `digest every ${spec.digest_minutes} min` : 'sends immediately')
  parts.push(`at most ${spec.rate_per_minute ?? 20} per min`)
  if (qh?.enabled) parts.push(`quiet ${qh.start}–${qh.end} ${spec.timezone || 'UTC'}`)
  const s = parts.join(' · ')
  return s.charAt(0).toUpperCase() + s.slice(1)
}

/** Variables available to notification templates. */
export const templateVariables: { name: string; description: string }[] = [
  { name: '{{.Name}}', description: 'Event name, e.g. quota.window.exhausted' },
  { name: '{{.Severity}}', description: 'info, warning or critical' },
  { name: '{{.Kind}}', description: 'alert, recovery, digest or test' },
  { name: '{{.OccurredAt}}', description: 'When the event happened' },
  { name: '{{.Subjects.provider_id}}', description: 'An identity from the event subjects' },
  { name: '{{.Data.reason}}', description: 'A field from the event data (see the catalog)' },
  { name: '{{.Data.label | upper}}', description: 'upper is the only helper function' },
]

export function fmtSeconds(s: number | undefined): string {
  if (!s) return '—'
  if (s % 3600 === 0) return `${s / 3600} h`
  if (s % 60 === 0) return `${s / 60} min`
  return `${s} s`
}

const eventTopic: [RegExp, string][] = [
  [/^quota\./, 'quota'],
  [/^budget\./, 'budget'],
  [/^provider\.account\./, 'account'],
  [/^provider\./, 'provider'],
  [/^compat\./, 'protocol'],
  [/^protocol\./, 'protocol change'],
  [/^routing\./, 'routing'],
  [/^access\./, 'access'],
  [/^policy\./, 'plan publishing'],
  [/^config\./, 'configuration'],
]

/** "Quota, budget and account warnings" from event patterns and severity. */
export function eventsPhrase(events: string[] | undefined, minSeverity: string | undefined): string {
  const topics = [...new Set((events ?? []).map((e) => eventTopic.find(([re]) => re.test(e))?.[1] ?? e))]
  const sev = minSeverity === 'critical' ? 'critical problems' : minSeverity === 'warning' ? 'warnings' : 'events'
  if (topics.length === 0) return minSeverity === 'warning' || minSeverity === 'critical' ? `All ${sev}` : 'Every event'
  const list = topics.length === 1 ? topics[0]! : `${topics.slice(0, -1).join(', ')} and ${topics[topics.length - 1]}`
  return `${list.charAt(0).toUpperCase()}${list.slice(1)} ${sev}`
}

/** "at most once per 30 min · also when resolved" */
export function noisePhrase(cooldown: number | undefined, group: number | undefined, recovery: boolean | undefined): string {
  const parts: string[] = []
  if (cooldown) parts.push(`at most once per ${fmtSeconds(cooldown)}`)
  if (group) parts.push(`repeats grouped for ${fmtSeconds(group)}`)
  if (recovery) parts.push('also when resolved')
  return parts.join(' · ')
}

/** Plain-language names of event kinds, for pickers and lists. */
export const eventLabels: Record<string, string> = {
  'quota.window.exhausted': 'Quota used up',
  'quota.window.recovered': 'Quota available again',
  'quota.monitor.auth_failed': 'Quota tracking cannot sign in',
  'budget.threshold.warning': 'Budget near its limit',
  'budget.threshold.exceeded': 'Budget used up',
  'provider.account.cooldown_started': 'Account paused after errors',
  'provider.account.recovered': 'Account back in service',
  'routing.account.switched': 'Serving account switched',
  'routing.fallback.selected': 'Fell back to another model or provider',
  'access.request.blocked': 'Request refused by policy',
  'access.credential.issued': 'Access token issued',
  'access.credential.revoked': 'Access token revoked',
  'policy.plan.published': 'Routing plan published',
  'compat.breaker.opened': 'Protocol path paused',
  'compat.breaker.half_opened': 'Protocol path testing recovery',
  'compat.breaker.closed': 'Protocol path restored',
  'compat.passthrough.activated': 'Direct passthrough turned on',
  'compat.passthrough.blocked': 'Direct passthrough refused',
  'compat.passthrough.deactivated': 'Direct passthrough turned off',
  'protocol.change.detected': 'Protocol change detected',
  'config.*': 'Any configuration change',
}

/** Area headings for event pickers, in display order. */
export const eventAreas: { prefix: string; label: string }[] = [
  { prefix: 'quota', label: 'Quota' },
  { prefix: 'budget', label: 'Budget' },
  { prefix: 'provider', label: 'Provider accounts' },
  { prefix: 'routing', label: 'Routing' },
  { prefix: 'access', label: 'Access' },
  { prefix: 'compat', label: 'Protocol paths' },
  { prefix: 'protocol', label: 'Protocol changes' },
  { prefix: 'policy', label: 'Routing plans' },
  { prefix: 'config', label: 'Configuration' },
]

/** "Quota used up", or the raw name for kinds without a label. */
export function eventLabel(name: string): string {
  if (eventLabels[name]) return eventLabels[name]
  if (name.endsWith('.*')) {
    const area = eventAreas.find((a) => a.prefix === name.slice(0, -2))
    if (area) return `Any ${area.label.toLowerCase()} event`
  }
  return name
}

/** One-click starting points for alert rules. */
export const rulePresets: { label: string; events: string[]; severity: string }[] = [
  { label: 'Quota used up', events: ['quota.window.exhausted', 'quota.monitor.auth_failed'], severity: 'info' },
  { label: 'Budget warnings', events: ['budget.threshold.warning', 'budget.threshold.exceeded'], severity: 'info' },
  { label: 'Account paused', events: ['provider.account.cooldown_started'], severity: 'info' },
  { label: 'Protocol path paused', events: ['compat.breaker.opened'], severity: 'info' },
]

/** Patterns that would make notifications trigger notifications. */
export const loopingPattern = (p: string) => p.startsWith('notification.') || p.startsWith('event.') || p === '*' || p === '*.*'
