/**
 * Minimal typed fetch wrapper for the same-origin Harbor Pilot API.
 *
 * - Credentials travel only as the HttpOnly session cookie (never stored in
 *   localStorage).
 * - Mutations carry the CSRF token obtained from GET /api/v1/session.
 * - Errors are normalized from the server's error envelope.
 * - Optimistic concurrency: pass the object's revision as `ifMatch`.
 */

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly requestId: string | undefined
  readonly details: unknown

  constructor(status: number, code: string, message: string, requestId?: string, details?: unknown) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.requestId = requestId
    this.details = details
  }
}

let csrfToken: string | undefined

export function setCsrfToken(token: string | undefined): void {
  csrfToken = token
}

type Query = Record<string, string | number | boolean | undefined | null>

export const API = '/api/v1'

function buildUrl(path: string, query?: Query): string {
  const url = new URL(API + path, window.location.origin)
  if (query) {
    for (const [k, v] of Object.entries(query)) {
      if (v !== undefined && v !== null && v !== '') url.searchParams.set(k, String(v))
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
  if (!res.ok) throw await parseError(res)
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
    if (!res.ok) throw err
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

/** Field errors from a validation_failed envelope, keyed by location. */
export function fieldErrors(err: unknown): Record<string, string> {
  const out: Record<string, string> = {}
  if (err instanceof ApiError && err.details && typeof err.details === 'object') {
    const fields = (err.details as { fields?: { location?: string; message?: string }[] }).fields ?? []
    for (const f of fields) if (f.location) out[f.location] = f.message ?? 'invalid'
  }
  return out
}
