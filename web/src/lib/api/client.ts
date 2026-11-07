/**
 * Minimal typed fetch wrapper for the same-origin Harbor Pilot API.
 *
 * - Credentials travel only as the HttpOnly session cookie (never stored in
 *   localStorage).
 * - Mutations carry the CSRF token obtained from GET /api/v1/session.
 * - Errors are normalized from the server's error envelope.
 * - Optimistic concurrency: pass the object's revision as `ifMatch`.
 */

import { ApiError } from './apiError'
import { fieldErrorMap, fieldProblems } from './errors'

export { ApiError }

let csrfToken: string | undefined

export function setCsrfToken(token: string | undefined): void {
  csrfToken = token
}

let onUnauthenticated: (() => void) | undefined

/**
 * Called when the API reports that the session ended (401) on anything but
 * the session probe itself; the session module re-reads the session so the
 * app returns to the sign-in page.
 */
export function setUnauthenticatedHandler(fn: (() => void) | undefined): void {
  onUnauthenticated = fn
}

function sessionEnded(path: string, status: number): void {
  if (status === 401 && path !== '/session') onUnauthenticated?.()
}

/** Query parameters; an array repeats the parameter once per item (`ref=a&ref=b`). */
type Query = Record<string, string | number | boolean | readonly string[] | undefined | null>

export const API = '/api/v1'

export function buildUrl(path: string, query?: Query): string {
  const url = new URL(API + path, window.location.origin)
  if (query) {
    for (const [k, v] of Object.entries(query)) {
      if (Array.isArray(v)) {
        for (const item of v) if (item !== '') url.searchParams.append(k, item)
      } else if (v !== undefined && v !== null && v !== '') url.searchParams.set(k, String(v))
    }
  }
  return url.pathname + url.search
}

async function parseError(res: Response): Promise<ApiError> {
  try {
    const body = (await res.json()) as { error?: { code?: string; message?: string; request_id?: string; details?: unknown } }
    const e = body.error ?? {}
    return new ApiError(res.status, e.code ?? 'error', e.message ?? res.statusText, e.request_id, e.details)
  } catch {
    return new ApiError(res.status, 'error', res.statusText || 'Request failed')
  }
}

export async function apiGet<T>(path: string, query?: Query, signal?: AbortSignal): Promise<T> {
  const res = await fetch(buildUrl(path, query), {
    method: 'GET',
    credentials: 'same-origin',
    headers: { Accept: 'application/json' },
    signal,
  })
  if (!res.ok) {
    sessionEnded(path, res.status)
    throw await parseError(res)
  }
  return (await res.json()) as T
}

/** Re-reads the session's CSRF token (after a sign-in in another tab). */
async function refreshCsrfToken(): Promise<boolean> {
  try {
    const res = await fetch(buildUrl('/session'), { credentials: 'same-origin', headers: { Accept: 'application/json' } })
    if (!res.ok) return false
    const s = (await res.json()) as { csrf_token?: string }
    if (!s.csrf_token || s.csrf_token === csrfToken) return false
    csrfToken = s.csrf_token
    return true
  } catch {
    return false
  }
}

export async function apiSend<T>(
  method: 'POST' | 'PUT' | 'PATCH' | 'DELETE',
  path: string,
  body?: unknown,
  opts?: { ifMatch?: number },
): Promise<T> {
  const send = () => {
    const headers: Record<string, string> = { Accept: 'application/json' }
    if (body !== undefined) headers['Content-Type'] = 'application/json'
    if (csrfToken) headers['X-CSRF-Token'] = csrfToken
    if (opts?.ifMatch !== undefined) headers['If-Match'] = `"r${opts.ifMatch}"`
    return fetch(buildUrl(path), {
      method,
      credentials: 'same-origin',
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  }
  let res = await send()
  if (!res.ok) {
    let err = await parseError(res)
    // The session changed under this tab (signed in elsewhere, server
    // reset): pick up the current token and try once more.
    if (err.code === 'csrf_invalid' && (await refreshCsrfToken())) {
      res = await send()
      if (!res.ok) err = await parseError(res)
    }
    if (!res.ok) {
      sessionEnded(path, res.status)
      throw err
    }
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

/**
 * Field errors from a validation_failed envelope, keyed by full location
 * (body.spec.base_url), with plain-language messages.
 */
export function fieldErrors(err: unknown): Record<string, string> {
  const out: Record<string, string> = {}
  for (const p of fieldProblems(err)) out[p.location] = p.message
  return out
}

export { fieldErrorMap }
