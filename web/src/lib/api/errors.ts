import { ApiError } from './apiError'
import { kindName } from '@/ui/glossary'

/**
 * Turns API errors into plain language for forms.
 *
 * Server validation errors carry field locations such as `body.spec.base_url`
 * and machine messages such as `expected number >= 300`. Forms show them on
 * the matching field, and anything that has no field goes in one summary.
 */

export interface FieldProblem {
  /** Full location, e.g. body.spec.base_url. */
  location: string
  /** Short field key: the last path segment (base_url). */
  field: string
  message: string
}

export interface ErrorSummary {
  /** One sentence for the banner. */
  title: string
  /** Every field problem, humanized. */
  fields: FieldProblem[]
  /** Small print: request id for support. */
  requestId?: string
  /** True when the session ended and the user must sign in again. */
  signIn?: boolean
}

const PREFIXES = [/^validation failed:\s*/i, /^(?:body|query|path|header)\.[\w.[\]-]+:\s*/i]

/** Removes "validation failed:" and "body.x:" prefixes from a message. */
export function stripPrefixes(msg: string): string {
  let out = msg.trim()
  for (let changed = true; changed; ) {
    changed = false
    for (const re of PREFIXES) {
      const next = out.replace(re, '')
      if (next !== out) {
        out = next
        changed = true
      }
    }
  }
  return out
}

/** "allow_private_network" → "Allow private network". */
export function labelize(key: string): string {
  const s = key.replace(/[_-]+/g, ' ').trim()
  return s.charAt(0).toUpperCase() + s.slice(1)
}

function capitalize(s: string): string {
  return s ? s.charAt(0).toUpperCase() + s.slice(1) : s
}

const SLUG_PATTERN = /expected string to match pattern \^\[a-z0-9\]/

/** Rewrites one validator message in plain words. */
export function humanizeMessage(message: string): string {
  const m = stripPrefixes(message)
  let r: RegExpMatchArray | null
  if ((r = m.match(/^expected required property (\S+) to be present$/))) return `${labelize(r[1] ?? '')} is required`
  if ((r = m.match(/^expected number >= (\S+)$/))) return `Must be at least ${r[1]}`
  if ((r = m.match(/^expected number <= (\S+)$/))) return `Must be at most ${r[1]}`
  if ((r = m.match(/^expected number > (\S+)$/))) return `Must be more than ${r[1]}`
  if ((r = m.match(/^expected number < (\S+)$/))) return `Must be less than ${r[1]}`
  if ((r = m.match(/^expected string length >= (\d+)$/))) return r[1] === '1' ? 'Required' : `Use at least ${r[1]} characters`
  if ((r = m.match(/^expected string length <= (\d+)$/))) return `Use at most ${r[1]} characters`
  if ((r = m.match(/^expected array length >= (\d+)$/))) return r[1] === '1' ? 'Add at least one' : `Add at least ${r[1]}`
  if ((r = m.match(/^expected array length <= (\d+)$/))) return `Add at most ${r[1]}`
  if (SLUG_PATTERN.test(m)) return 'Use lowercase letters, digits, dots, dashes or underscores, starting with a letter or digit'
  if (/^expected string to match pattern /.test(m)) return 'This value has the wrong format'
  if (/RFC 3986 (absolute )?uri/i.test(m)) return 'Enter a full URL, such as https://example.com'
  if (/RFC 5322 email/i.test(m)) return 'Enter an email address'
  if (/RFC 3339 date-time/i.test(m)) return 'Enter a date and time'
  if ((r = m.match(/^expected value to be one of "(.*)"$/))) return `Choose one of: ${(r[1] ?? '').split('", "').join(', ')}`
  if (/^unexpected property$/.test(m)) return 'Not allowed here'
  if ((r = m.match(/^expected (integer|number|string|boolean|array|object)\b/))) {
    return { integer: 'Enter a whole number', number: 'Enter a number', string: 'Enter text', boolean: 'Choose yes or no', array: 'Enter a list', object: 'Enter a set of values' }[r[1] as 'integer'] ?? capitalize(m)
  }
  return capitalize(m)
}

/** Field problems in a validation error, humanized. */
export function fieldProblems(err: unknown): FieldProblem[] {
  if (!(err instanceof ApiError) || !err.details || typeof err.details !== 'object') return []
  const fields = (err.details as { fields?: { location?: string; message?: string }[] }).fields ?? []
  return fields
    .filter((f) => f.location)
    .map((f) => {
      const location = f.location ?? ''
      // Huma reports a missing property on its parent: name the property.
      const missing = (f.message ?? '').match(/^expected required property (\S+) to be present$/)
      const path = missing ? `${location}.${missing[1]}` : location
      const segs = path.split('.').filter((s) => !['body', 'spec', 'query', 'path', 'header'].includes(s))
      return { location: path, field: segs.join('.') || path, message: humanizeMessage(f.message ?? 'invalid') }
    })
}

/**
 * Field errors keyed for lookup by full location (body.spec.base_url), by
 * path without body/spec (base_url, models.0.id) and by last segment.
 */
export function fieldErrorMap(err: unknown): Record<string, string> {
  const out: Record<string, string> = {}
  for (const p of fieldProblems(err)) {
    const last = p.field.split('.').pop() ?? p.field
    for (const k of [p.location, p.field, last]) if (!(k in out)) out[k] = p.message
  }
  return out
}

function kindLabel(ref: string): string {
  // "routing_plan team" → "routing plan team"
  return ref.replace(/^([a-z_]+)\s/, (_, k: string) => `${kindName[k] ?? k.replaceAll('_', ' ')} `)
}

/** A plain-language summary of any error a mutation can throw. */
export function summarizeError(err: unknown): ErrorSummary {
  if (!(err instanceof ApiError)) {
    return { title: err instanceof Error && err.message ? err.message : 'Something went wrong. Try again.', fields: [] }
  }
  const requestId = err.requestId
  const fields = fieldProblems(err)
  if (err.status === 401) return { title: 'Your session ended. Sign in again to continue.', fields: [], requestId, signIn: true }
  if (err.code === 'validation_failed' || err.status === 422) {
    if (fields.length > 0) return { title: fields.length === 1 ? 'Fix the highlighted field.' : `Fix the ${fields.length} highlighted fields.`, fields, requestId }
    return { title: capitalize(stripPrefixes(err.message)), fields, requestId }
  }
  if (err.status === 412) return { title: 'Someone else changed this since you opened it. Close and reopen to see their changes.', fields, requestId }
  if (err.status === 409) {
    const refs = (err.details as { referenced_by?: string[] } | undefined)?.referenced_by
    if (refs?.length) return { title: `Still in use by ${refs.map(kindLabel).join(', ')}. Remove those references first.`, fields, requestId }
    return { title: capitalize(stripPrefixes(err.message)), fields, requestId }
  }
  if (err.status === 403) {
    const perm = err.message.match(/^permission (\S+) required$/)
    if (perm) return { title: `You don't have permission to do this (${perm[1]}).`, fields, requestId }
    return { title: capitalize(err.message), fields, requestId }
  }
  if (err.status >= 500) return { title: 'The server could not complete this. Try again; if it keeps failing, check the server log.', fields, requestId }
  return { title: capitalize(stripPrefixes(err.message)), fields, requestId }
}
