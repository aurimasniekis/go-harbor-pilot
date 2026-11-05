import { describe, expect, it } from 'vitest'
import { ApiError } from './apiError'
import { fieldErrorMap, fieldProblems, humanizeMessage, stripPrefixes, summarizeError } from './errors'

const validation = (fields: { location: string; message: string }[], message = 'validation failed') =>
  new ApiError(422, 'validation_failed', message, 'req-1', { fields })

describe('humanizeMessage', () => {
  it.each([
    ['expected number >= 300', 'Must be at least 300'],
    ['expected number <= 10', 'Must be at most 10'],
    ['expected string length >= 1', 'Required'],
    ['expected string length <= 80', 'Use at most 80 characters'],
    ['expected string to match pattern ^[a-z0-9][a-z0-9._-]{0,62}$', 'Use lowercase letters, digits, dots, dashes or underscores, starting with a letter or digit'],
    ['expected string to be RFC 3986 absolute uri', 'Enter a full URL, such as https://example.com'],
    ['expected value to be one of "day", "week", "month"', 'Choose one of: day, week, month'],
    ['expected required property issuer to be present', 'Issuer is required'],
    ['unexpected property', 'Not allowed here'],
    ['expected integer', 'Enter a whole number'],
    ['validation failed: body.spec.secret_ref: environment variables used as secrets must start with HP_SECRET_', 'Environment variables used as secrets must start with HP_SECRET_'],
  ])('%s', (input, want) => {
    expect(humanizeMessage(input)).toBe(want)
  })

  it('strips nested prefixes', () => {
    expect(stripPrefixes('validation failed: body.name: name must be lowercase')).toBe('name must be lowercase')
  })
})

describe('fieldProblems and fieldErrorMap', () => {
  it('maps every field error, names missing properties, and offers short keys', () => {
    const err = validation([
      { location: 'body.spec.base_url', message: 'expected string to be RFC 3986 absolute uri' },
      { location: 'body.spec', message: 'expected required property webhook_url to be present' },
      { location: 'body.spec.models.0.id', message: 'expected string length >= 1' },
    ])
    expect(fieldProblems(err).map((p) => [p.location, p.field])).toEqual([
      ['body.spec.base_url', 'base_url'],
      ['body.spec.webhook_url', 'webhook_url'],
      ['body.spec.models.0.id', 'models.0.id'],
    ])
    const m = fieldErrorMap(err)
    expect(m['body.spec.base_url']).toBe('Enter a full URL, such as https://example.com')
    expect(m.base_url).toBe(m['body.spec.base_url'])
    expect(m.webhook_url).toBe('Webhook url is required')
    expect(m['models.0.id']).toBe('Required')
  })

  it('returns nothing for non-API errors', () => {
    expect(fieldProblems(new Error('x'))).toEqual([])
  })
})

describe('summarizeError', () => {
  it('counts field problems instead of saying "validation failed"', () => {
    const s = summarizeError(validation([{ location: 'body.name', message: 'x' }, { location: 'body.spec.ttl_seconds', message: 'expected number >= 0' }]))
    expect(s.title).toBe('Fix the 2 highlighted fields.')
    expect(s.fields).toHaveLength(2)
    expect(s.requestId).toBe('req-1')
  })

  it('keeps a validation message without fields, minus its prefixes', () => {
    expect(summarizeError(validation([], 'validation failed: body.spec.owner_id: must be a user id')).title).toBe('Must be a user id')
  })

  it('asks to sign in again when the session ended', () => {
    const s = summarizeError(new ApiError(401, 'unauthenticated', 'authentication required'))
    expect(s.signIn).toBe(true)
    expect(s.title).toMatch(/session ended/)
  })

  it('names what still references an object', () => {
    const err = new ApiError(409, 'conflict', 'object is referenced', 'r', { referenced_by: ['routing_plan team', 'client_binding laptop'] })
    expect(summarizeError(err).title).toBe('Still in use by routing plan team, client laptop. Remove those references first.')
  })

  it('explains a concurrent change and missing permissions', () => {
    expect(summarizeError(new ApiError(412, 'revision_mismatch', 'x')).title).toMatch(/Someone else changed this/)
    expect(summarizeError(new ApiError(403, 'forbidden', 'permission users.manage required')).title).toBe("You don't have permission to do this (users.manage).")
  })
})
