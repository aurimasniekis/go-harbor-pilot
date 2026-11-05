import { expect, test } from '@playwright/test'

// Patterns that must never appear in any API response.
const forbidden: [string, RegExp][] = [
  ['bearer token', /Bearer\s+[A-Za-z0-9._-]{12,}/],
  ['provider api key', /\b(sk-[A-Za-z0-9]{8,}|sk-ant-[A-Za-z0-9-]{8,}|xai-[A-Za-z0-9]{8,})/],
  ['harbor pilot token', /\bhp[ak]_[a-z2-7]{8}_[A-Za-z0-9_-]{43}\b/],
  ['argon2 hash', /\$argon2id\$/],
  ['sealed envelope', /\bv1\.k[0-9a-f]{12}\./],
  ['private key', /BEGIN [A-Z ]*PRIVATE KEY/],
]

test('no secrets in API responses', async ({ request }) => {
  const login = await request.post('/api/v1/session', { data: { email: 'admin@example.test', password: 'harbor-pilot-demo' } })
  expect(login.ok()).toBe(true)
  const paths = ['/api/v1/session', '/api/v1/meta', '/api/v1/users', '/api/v1/service-principals', '/api/v1/auth/settings',
    '/api/v1/auth/oidc-providers', '/api/v1/audit', '/api/v1/system/status', '/api/v1/security-alerts', '/api/v1/session/all']
  for (const p of paths) {
    const res = await request.get(p)
    expect(res.status(), p).toBeLessThan(400)
    const body = await res.text()
    for (const [name, re] of forbidden) {
      expect(re.test(body), `${name} in ${p}`).toBe(false)
    }
  }
})

test('mutations require a session and a CSRF token', async ({ request }) => {
  const anon = await request.post('/api/v1/service-principals', { data: { name: 'x', permissions: [] } })
  expect(anon.status()).toBe(401)
  await request.post('/api/v1/session', { data: { email: 'admin@example.test', password: 'harbor-pilot-demo' } })
  const noCsrf = await request.post('/api/v1/service-principals', { data: { name: 'x', permissions: [] } })
  expect(noCsrf.status()).toBe(403)
})

test('security headers are set', async ({ request }) => {
  const res = await request.get('/')
  const h = res.headers()
  expect(h['content-security-policy']).toContain("default-src 'self'")
  expect(h['x-content-type-options']).toBe('nosniff')
  expect(h['referrer-policy']).toBeTruthy()
})

test('unknown API route returns the error envelope', async ({ request }) => {
  const res = await request.get('/api/v1/does-not-exist')
  expect(res.status()).toBe(404)
  const body = (await res.json()) as { error: { code: string; request_id: string } }
  expect(body.error.code).toBe('not_found')
  expect(body.error.request_id).toBeTruthy()
})
