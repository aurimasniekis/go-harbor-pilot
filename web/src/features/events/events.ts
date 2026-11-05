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

export const channelTypes: { value: ChannelSpec['type']; label: string; secret: string; secretHint: string }[] = [
  { value: 'telegram', label: 'Telegram', secret: 'Bot token', secretHint: 'From @BotFather, e.g. 123456:ABC… Stored encrypted; never shown again.' },
  { value: 'slack', label: 'Slack', secret: 'Incoming webhook URL', secretHint: 'https://hooks.slack.com/services/… The URL is the credential, so it is stored as a secret.' },
  { value: 'discord', label: 'Discord', secret: 'Webhook URL', secretHint: 'https://discord.com/api/webhooks/… Mentions are disabled in every message.' },
  { value: 'webhook', label: 'Signed webhook', secret: 'Signing secret', secretHint: 'Used for the X-Harbor-Signature HMAC. Receivers verify it with the same secret.' },
]

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
