/* global process, console, fetch */
// Captures the documentation screenshots from a running demo instance.
//
//   node web/scripts/screenshots.mjs <base-url> <output-dir> [only-name ...]
//
// Run through scripts/screenshots.sh, which builds the binary and starts a
// fresh demo instance with its synthetic dataset. Every shot is
// taken in the dark and light themes at 1440×900, one right after the other so
// both show the same state; shots marked `mobile` are also taken at 390×844 in
// the dark theme. After signing in, the script seeds states the plain demo
// lacks (see seed()) through the API and this instance's gateway only. Data
// is the synthetic demo dataset: no real providers, accounts, prompts or
// credentials appear in any image. Tokens shown in reveal dialogs belong to
// the throwaway instance and die with it.
import { chromium } from '@playwright/test'
import { mkdirSync } from 'node:fs'
import { createServer } from 'node:http'

const [base, out, ...only] = process.argv.slice(2)
if (!base || !out) {
  console.error('usage: node scripts/screenshots.mjs <base-url> <output-dir> [name ...]')
  process.exit(64)
}
mkdirSync(out, { recursive: true })

const EMAIL = 'admin@example.test'
const PASSWORD = 'harbor-pilot-demo'

async function signIn(page) {
  await page.goto(base + '/login')
  await page.getByLabel('Email').fill(EMAIL)
  await page.getByLabel('Password', { exact: true }).fill(PASSWORD)
  await page.getByRole('button', { name: /sign in/i }).click()
  await page.waitForURL(base + '/')
}

async function api(page, path) {
  const r = await page.request.get(base + '/api/v1' + path)
  return r.ok() ? r.json() : {}
}

// A mutating control-plane call from inside the signed-in page: same origin,
// session cookie and CSRF token, like the UI's own calls. Throws on failure
// with the status and error code only (never a response body).
async function send(page, method, path, body, rev) {
  const r = await page.evaluate(
    async ({ method, url, body, rev }) => {
      const s = await (await fetch('/api/v1/session')).json()
      const headers = { Accept: 'application/json', 'X-CSRF-Token': s.csrf_token }
      if (body !== undefined) headers['Content-Type'] = 'application/json'
      if (rev !== undefined) headers['If-Match'] = `"r${rev}"`
      const res = await fetch(url, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
      const text = await res.text()
      let json = null
      try {
        json = text ? JSON.parse(text) : null
      } catch {
        json = null
      }
      return { status: res.status, json }
    },
    { method, url: '/api/v1' + path, body, rev },
  )
  if (r.status >= 400) throw new Error(`${method} ${path}: ${r.status} ${r.json?.error?.code ?? ''} ${r.json?.error?.message ?? ''}`)
  return r.json
}

const byName = (list, name) => (list.items ?? []).find((x) => x.name === name)

// A local webhook receiver on 127.0.0.1 for the alert channel, so test
// messages are delivered without contacting anything outside this machine.
const sink = createServer((req, res) => {
  req.resume()
  req.on('end', () => res.writeHead(204).end())
})
await new Promise((resolve) => sink.listen(0, '127.0.0.1', resolve))
const sinkURL = `http://127.0.0.1:${sink.address().port}/alerts`

// Seeds the states the plain demo does not have: declared subscription
// windows, captured-content access and a masked capture, an alert channel
// with messages, a relay with failed deliveries, an API token, a run-scoped
// child token with a request, and a backup. Runs once per instance; the demo
// is fresh on every run of scripts/screenshots.sh. Gateway requests go to
// this instance, whose providers are its own synthetic upstreams.
async function seed(page) {
  const accounts = await api(page, '/provider-accounts')
  const providers = await api(page, '/providers')
  const ent = byName(await api(page, '/subscription-entitlements'), 'anthropic-team-1-plan')
  const clients = await api(page, '/client-bindings')
  const me = (await api(page, '/session')).user
  const seeded = {
    account: byName(accounts, 'anthropic-team-1')?.id,
    spaceman: byName(clients, 'spaceman-runs')?.id,
    ci: byName(clients, 'codex-ci')?.id,
    openai: byName(providers, 'openai-demo')?.id,
  }
  if (byName(await api(page, '/notification-channels'), 'on-call-webhook')) return seeded
  const gateway = async (binding, path, body, ua) => {
    const c = await send(page, 'POST', `/client-bindings/${binding}/credentials`, body.credential)
    const res = await fetch(base + path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${c.token}`, 'User-Agent': ua },
      body: JSON.stringify(body.request),
    })
    await res.arrayBuffer()
  }

  // A run-scoped child token with trusted references, and one request made
  // with it.
  if (seeded.spaceman) {
    await gateway(
      seeded.spaceman,
      '/v1/messages',
      {
        credential: {
          name: 'run run_synthetic_2',
          scopes: ['gateway:invoke', 'gateway:models'],
          ttl_seconds: 3600,
          bound_refs: { 'spaceman.space': 'spc_synthetic', 'spaceman.run': 'run_synthetic_2', 'spaceman.task': 'tsk_synthetic_7' },
        },
        request: { model: 'coding/deep', max_tokens: 2048, messages: [{ role: 'user', content: '[synthetic] run step 4' }] },
      },
      'claude-cli/2.1.318 (external, sdk-cli)',
    )
  }
  // A request on the private plan carrying synthetic credentials: masked
  // before dispatch and captured (masked) for three days.
  const notebook = byName(clients, 'private-notebook')?.id
  if (notebook) {
    await gateway(
      notebook,
      '/v1/chat/completions',
      {
        credential: { name: 'notebook kernel', scopes: ['gateway:invoke', 'gateway:models'], ttl_seconds: 3600 },
        request: {
          model: 'anything',
          messages: [
            {
              role: 'user',
              content:
                '[synthetic] Why does this deploy step fail?\nexport ANTHROPIC_API_KEY=sk-ant-api03-EXAMPLEexampleEXAMPLE0000000000\nDB_PASSWORD="correct-horse-battery-staple"\nThe on-call contact is ops@example.test.',
            },
          ],
        },
      },
      'python-requests/2.32 (synthetic)',
    )
  }

  // Relay to an unreachable local port, enabled only while the seeding
  // below produces events, so its deliveries show failed attempts.
  const relay = await send(page, 'POST', '/event-destinations', {
    name: 'siem-webhook',
    spec: {
      type: 'webhook',
      enabled: true,
      webhook_url: 'http://127.0.0.1:9/harbor-pilot',
      allow_private_network: true,
      max_attempts: 3,
      timeout_seconds: 2,
      filter: { events: ['config.*', 'access.*', 'compat.*'], min_severity: 'info' },
    },
  })
  seeded.relay = relay.id

  const channel = await send(page, 'POST', '/notification-channels', {
    name: 'on-call-webhook',
    spec: { type: 'webhook', enabled: true, webhook_url: sinkURL, allow_private_network: true },
  })
  const rule = await send(page, 'POST', '/notification-rules', {
    name: 'paused-protocol-paths',
    spec: { enabled: true, channel_ids: [channel.id], recovery: true, cooldown_seconds: 600, filter: { events: ['compat.*'], min_severity: 'info' } },
  })

  if (ent) {
    await send(
      page,
      'PUT',
      `/subscription-entitlements/${ent.id}`,
      {
        name: ent.name,
        spec: {
          ...ent.spec,
          plan_name: 'Team plan (synthetic)',
          fee: '150',
          windows: [
            { name: 'five_hour', seconds: 18000, unit: 'percent', limit: 100 },
            { name: 'seven_day', seconds: 604800, unit: 'percent', limit: 100 },
          ],
        },
      },
      ent.revision,
    )
  }

  // Captured content needs an explicit grant; this sign-in is recent.
  await send(page, 'PUT', `/users/${me.id}/grants`, { grants: ['traces.content.read'] }, me.revision)

  // A paused protocol path: its alerts and events, then back to automatic.
  // The Diagnostics shot pauses it again while it is taken.
  if (seeded.openai) {
    await setBreaker(page, seeded.openai, 'force_open')
    await page.waitForTimeout(2000)
    await setBreaker(page, seeded.openai, 'auto')
  }

  const sp = byName(await api(page, '/service-principals'), 'demo-terraform')
  if (sp) await send(page, 'POST', `/service-principals/${sp.id}/tokens`, { name: 'ci-main', ttl_seconds: 90 * 86400 })

  for (let i = 0; i < 2; i++) await send(page, 'POST', `/notification-channels/${channel.id}/test`)
  await send(page, 'POST', '/system/backups', { include_traces: false })

  // Let the relay try (and fail) its deliveries and the alerts go out, then
  // turn the relay and the rule off so later shots add nothing.
  await page.waitForTimeout(15000)
  const r = await api(page, `/event-destinations/${relay.id}`)
  await send(page, 'PUT', `/event-destinations/${relay.id}`, { name: r.name, spec: { ...r.spec, enabled: false } }, r.revision)
  const ru = await api(page, `/notification-rules/${rule.id}`)
  await send(page, 'PUT', `/notification-rules/${rule.id}`, { name: ru.name, spec: { ...ru.spec, enabled: false } }, ru.revision)
  return seeded
}

async function setBreaker(page, providerId, override) {
  await send(page, 'POST', '/compat/breakers/override', {
    provider_id: providerId,
    path: 'anthropic_messages>openai_responses',
    override,
    reason: override === 'auto' ? '' : 'Tool call translation under review',
  })
}

// A draft of the team plan whose decision table ends in a catch-all row
// that blocks: what the publish dialog warns about. Removed after the plan
// editor shots so the other plan shots show the published plan only.
const draftDoc = (live) => ({
  ...live,
  engine: {
    rules: [
      {
        name: 'deep-needs-reasoning',
        stage: 'request',
        when: 'request.model == "coding/deep"',
        action: 'require',
        capability: 'reasoning',
        reason: 'Deep requests go to reasoning models only',
        enabled: true,
      },
    ],
    tables: [
      {
        name: 'approved-tools',
        stage: 'request',
        hit_policy: 'first',
        inputs: ['request.harness', 'request.stream'],
        rows: [
          { conditions: ['value == "codex"', '-'], action: 'require', capability: 'tools', reason: 'Codex always sends tools' },
          { conditions: ['value == "pi"', '-'], action: 'set_intent', intent: 'review', reason: 'Pi is used for reviews' },
          { conditions: ['-', '-'], action: 'block', reason: 'Not an approved tool' },
        ],
        enabled: true,
      },
    ],
    score: '',
  },
})

async function ensureDraft(page, planId) {
  const versions = (await api(page, `/routing-plans/${planId}/versions`)).items ?? []
  if (versions.some((v) => v.status === 'draft')) return
  const live = versions.find((v) => v.status === 'published')
  await send(page, 'POST', `/routing-plans/${planId}/versions`, { document: draftDoc(live.document), note: 'Only approved tools' })
}

async function dropDraft(page, planId) {
  const versions = (await api(page, `/routing-plans/${planId}/versions`)).items ?? []
  for (const v of versions.filter((x) => x.status === 'draft')) await send(page, 'DELETE', `/routing-plans/${planId}/versions/${v.id}`)
}

async function settle(page) {
  await page.waitForLoadState('networkidle').catch(() => {})
  // Charts animate in; give them time to finish.
  await page.waitForTimeout(900)
}

async function click(page, name) {
  const b = page.getByRole('button', { name }).first()
  await b.click({ timeout: 4000 })
  await page.waitForTimeout(600)
}

// Each shot: name, path (string or (ids) => string), optional setup(page, ids)
// (runs after navigation, once per theme), after(page, ids) (cleanup, once
// after both themes), fullPage, mobile. Ids come from the demo data and the
// seeded states, once per run.
const shots = [
  // First, before the other shots add their own entries: the seeding's.
  { name: 'audit', path: '/settings/audit' },
  { name: 'home', path: '/', mobile: true },
  { name: 'requests', path: '/activity', mobile: true },
  { name: 'request', path: (ids) => `/activity/${ids.failover}`, fullPage: true, mobile: true },
  { name: 'request-run-refs', path: (ids) => `/activity/${ids.runRequest}`, fullPage: true, setup: (p) => click(p, /^verified context/i) },
  {
    name: 'request-replay',
    path: (ids) => `/activity/${ids.failover}`,
    setup: async (p) => {
      await click(p, /replay with current config/i)
      await p.getByRole('dialog').getByRole('button', { name: /^replay$/i }).click()
      await p.waitForTimeout(2000)
    },
  },
  { name: 'requests-sessions', path: '/activity/sessions' },
  { name: 'spend', path: '/usage', fullPage: true },
  { name: 'spend-breakdown', path: '/usage/breakdown', fullPage: true },
  { name: 'limits', path: '/quotas', fullPage: true, mobile: true },
  { name: 'limits-tracking', path: '/quotas/monitors' },
  { name: 'budget-add', path: '/usage/budgets', setup: (p) => click(p, /add budget/i) },
  { name: 'clients', path: '/clients', mobile: true },
  { name: 'client', path: (ids) => `/clients/${ids.client}`, fullPage: true },
  { name: 'client-add', path: '/clients', setup: (p) => click(p, /add client/i) },
  { name: 'client-setup', path: '/clients/setup', fullPage: true },
  { name: 'client-setup-codex', path: (ids) => `/clients/setup?tool=codex&binding=${ids.ci}`, fullPage: true },
  // Token shots use the CI client, so the client shot above stays the same
  // in both themes.
  { name: 'token-create', path: (ids) => `/clients/${ids.ci}?tab=tokens`, setup: (p) => click(p, /^create token/i) },
  {
    name: 'token-created',
    path: (ids) => `/clients/${ids.ci}?tab=tokens`,
    setup: async (p) => {
      await click(p, /^create token/i)
      await p.getByRole('dialog').getByRole('button', { name: /^create token/i }).click()
      await p.getByRole('dialog', { name: /access token created/i }).waitFor({ timeout: 8000 })
    },
  },
  {
    name: 'token-replace',
    path: (ids) => `/clients/${ids.ci}?tab=tokens`,
    setup: async (p) => {
      await click(p, /^actions for demo ci/i)
      await p.getByRole('menuitem', { name: /replace with a new token/i }).click()
      await p.waitForTimeout(500)
      await p.getByRole('dialog').getByRole('button', { name: /^create token/i }).click()
      await p.getByRole('dialog', { name: /access token created/i }).waitFor({ timeout: 8000 })
    },
  },
  { name: 'providers', path: '/providers', fullPage: true },
  { name: 'provider-connect', path: '/providers', setup: (p) => click(p, /connect a provider|add provider/i) },
  {
    name: 'subscriptions',
    path: (ids) => `/providers?account=${ids.account}`,
    setup: (p) => click(p, /^edit team plan/i),
  },
  { name: 'quota-forecast', path: (ids) => `/providers?account=${ids.account}`, setup: (p) => click(p, /history and forecast for/i) },
  { name: 'subscription-add', path: (ids) => `/providers?account=${ids.account}`, setup: (p) => click(p, /add subscription/i) },
  {
    name: 'account-test',
    path: (ids) => `/providers?account=${ids.account}`,
    setup: async (p) => {
      await click(p, /test connection/i)
      await p.waitForTimeout(1500)
    },
  },
  { name: 'routing', path: '/routing/plans' },
  { name: 'plan', path: (ids) => `/routing/plans/${ids.plan}`, fullPage: true },
  { name: 'plan-versions', path: (ids) => `/routing/plans/${ids.plan}?tab=versions` },
  { name: 'plan-simulator', path: (ids) => `/routing/plans/${ids.plan}?tab=test`, fullPage: true },
  // The draft shots run in this order: the last one removes the draft.
  { name: 'plan-editor', path: (ids) => `/routing/plans/${ids.plan}`, setup: (p, ids) => openDraft(p, ids, /edit draft/i) },
  {
    name: 'plan-rules',
    path: (ids) => `/routing/plans/${ids.plan}`,
    setup: async (p, ids) => {
      await openDraft(p, ids, /edit draft/i)
      const d = p.getByRole('dialog')
      await d.getByRole('tab', { name: /^rules/i }).click()
      await p.waitForTimeout(400)
      await d.getByText('Decision tables', { exact: true }).evaluate((el) => el.scrollIntoView({ block: 'start' }))
      // Back a little, so the table's own header is not under the tabs.
      await p.mouse.move(720, 450)
      await p.mouse.wheel(0, -40)
      await p.waitForTimeout(400)
    },
  },
  {
    name: 'plan-publish',
    path: (ids) => `/routing/plans/${ids.plan}`,
    setup: async (p, ids) => {
      await openDraft(p, ids, /^publish v/i)
      await p.getByText('I understand that clients').waitFor({ timeout: 8000 })
    },
    after: (p, ids) => dropDraft(p, ids.plan),
  },
  { name: 'model-names', path: '/routing' },
  { name: 'fallback-groups', path: '/routing/pools' },
  { name: 'fallback-group-add', path: '/routing/pools', setup: (p) => click(p, /add fallback group/i) },
  { name: 'privacy', path: '/privacy' },
  { name: 'privacy-policy', path: '/privacy', setup: (p) => click(p, /add masking policy|add policy|new policy/i) },
  {
    name: 'privacy-test',
    path: '/privacy?try=true',
    fullPage: true,
    setup: async (p) => {
      await click(p, /use a synthetic sample/i)
      await click(p, /^test$/i)
      await p.waitForTimeout(1000)
    },
  },
  { name: 'capture-rules', path: '/privacy/trace-policies' },
  { name: 'captured', path: '/traces' },
  { name: 'captured-trace', path: (ids) => `/traces/${ids.trace}`, fullPage: true },
  { name: 'alerts', path: '/notifications' },
  { name: 'alert-channel', path: '/notifications', setup: (p) => click(p, /add channel/i) },
  { name: 'alert-rule', path: '/notifications/rules', setup: (p) => click(p, /add rule/i) },
  { name: 'alert-history', path: '/notifications/history' },
  { name: 'relays', path: '/events/destinations' },
  { name: 'relay', path: (ids) => `/events/destinations/${ids.relay}`, fullPage: true },
  { name: 'event-stream', path: '/events' },
  { name: 'people', path: '/settings/users' },
  { name: 'people-invite', path: '/settings/users', setup: (p) => click(p, /invite user/i) },
  { name: 'api-tokens', path: '/settings/service-principals' },
  {
    name: 'api-token-issued',
    path: '/settings/service-principals',
    setup: async (p) => {
      await click(p, /api tokens of demo-terraform/i)
      const d = p.getByRole('dialog')
      await d.getByRole('textbox', { name: /token name/i }).fill('github-actions')
      await d.getByRole('button', { name: /^issue token/i }).click()
      await p.getByRole('dialog', { name: /api token issued/i }).waitFor({ timeout: 8000 })
    },
  },
  { name: 'sso', path: '/settings/auth' },
  { name: 'sso-add', path: '/settings/auth', setup: (p) => click(p, /^add provider/i) },
  {
    name: 'diagnostics',
    path: '/compatibility',
    setup: async (p, ids) => {
      await setBreaker(p, ids.openai, 'force_open')
      await p.reload()
    },
    after: (p, ids) => setBreaker(p, ids.openai, 'auto'),
  },
  { name: 'system', path: '/settings/system', fullPage: true },
  { name: 'account', path: '/account' },
  {
    name: 'palette',
    path: '/',
    setup: async (p) => {
      await p.keyboard.press('ControlOrMeta+k')
      await p.waitForTimeout(500)
      await p.keyboard.type('budget')
      await p.waitForTimeout(400)
    },
  },
]

async function openDraft(page, ids, button) {
  await ensureDraft(page, ids.plan)
  await page.reload()
  await settle(page)
  await click(page, button)
}

async function demoIds(page) {
  const list = await api(page, '/activity/requests?limit=200')
  const reqs = list.items ?? []
  const plans = (await api(page, '/routing-plans')).items ?? []
  const clients = (await api(page, '/client-bindings')).items ?? []
  return {
    total: list.total ?? reqs.length,
    failover: (reqs.find((r) => r.attempts > 1) ?? reqs[0])?.id,
    plan: (plans.find((p) => p.name === 'team-default') ?? plans[0])?.id,
    client: (clients.find((c) => c.name === 'claude-code-laptop') ?? clients[0])?.id,
  }
}

// Ids of the seeded states (after seed()).
async function seededIds(page, seeded) {
  const reqs = (await api(page, '/activity/requests?limit=50')).items ?? []
  const traces = (await api(page, '/traces')).items ?? []
  const relay = seeded.relay ?? byName(await api(page, '/event-destinations'), 'siem-webhook')?.id
  return {
    account: seeded.account,
    ci: seeded.ci,
    openai: seeded.openai,
    relay,
    runRequest: reqs.find((r) => r.binding_id === seeded.spaceman)?.id,
    trace: traces[0]?.id,
  }
}

const browser = await chromium.launch()
const failures = []

async function session(theme, width, height) {
  const ctx = await browser.newContext({ viewport: { width, height }, colorScheme: theme, deviceScaleFactor: 2 })
  const page = await ctx.newPage()
  await signIn(page)
  return { ctx, page }
}

async function shoot(page, s, ids, suffix, mobile) {
  const file = `${out}/${s.name}${suffix}.png`
  try {
    const path = typeof s.path === 'function' ? s.path(ids) : s.path
    await page.goto(base + path)
    await settle(page)
    if (s.setup) await s.setup(page, ids)
    await settle(page)
    await page.screenshot({ path: file, fullPage: !!s.fullPage && !mobile })
    console.log('ok  ', file)
  } catch (e) {
    failures.push(`${s.name}${suffix}: ${e.message.split('\n')[0]}`)
    console.log('FAIL', file, e.message.split('\n')[0])
  }
}

const wanted = (s) => !only.length || only.includes(s.name)
const dark = await session('dark', 1440, 900)
const light = await session('light', 1440, 900)

// Demo traffic starts shortly after boot; wait until all of it has been
// recorded (it includes a failover) so every page shows the full dataset.
let ids = {}
for (let i = 0; i < 60 && !(ids.failover && ids.total >= 18); i++) {
  ids = await demoIds(dark.page)
  if (!(ids.failover && ids.total >= 18)) await dark.page.waitForTimeout(1000)
}
const seeded = await seed(dark.page)
await dark.page.waitForTimeout(1000)
ids = { ...ids, ...(await seededIds(dark.page, seeded)) }

// Each shot in both themes back to back, so both show the same state.
for (const s of shots.filter(wanted)) {
  await shoot(dark.page, s, ids, '-dark')
  await shoot(light.page, s, ids, '-light')
  if (s.after) await s.after(dark.page, ids).catch((e) => failures.push(`${s.name} cleanup: ${e.message}`))
}

// Signed-out screen.
if (!only.length || only.includes('login')) {
  for (const theme of ['dark', 'light']) {
    const anon = await browser.newContext({ viewport: { width: 1440, height: 900 }, colorScheme: theme, deviceScaleFactor: 2 })
    const p = await anon.newPage()
    await p.goto(base + '/login')
    await settle(p)
    await p.screenshot({ path: `${out}/login-${theme}.png` })
    await anon.close()
  }
}
await dark.ctx.close()
await light.ctx.close()

// Phone shots, dark theme.
const phone = await session('dark', 390, 844)
for (const s of shots.filter((x) => x.mobile && wanted(x))) await shoot(phone.page, s, ids, '-mobile', true)
await phone.ctx.close()

await browser.close()
sink.close()

if (failures.length) {
  console.error(`\n${failures.length} shot(s) failed:\n` + failures.join('\n'))
  process.exit(1)
}
