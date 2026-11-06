/* global process, console */
// Captures the documentation screenshots from a running demo instance.
//
//   node web/scripts/screenshots.mjs <base-url> <output-dir> [only-name ...]
//
// Run through scripts/screenshots.sh, which builds the binary and starts a
// fresh demo instance with its synthetic dataset. Every shot is
// taken in the dark and light themes at 1440×900; shots marked `mobile` are
// also taken at 390×844 in the dark theme. Data is the synthetic demo dataset:
// no real providers, accounts, prompts or credentials appear in any image.
import { chromium } from '@playwright/test'
import { mkdirSync } from 'node:fs'

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

// Each shot: name, path (string or async (ids) => string), optional setup
// (runs after navigation), fullPage, mobile. Paths with ids are resolved from
// the demo data once per run.
const shots = [
  { name: 'home', path: '/', mobile: true },
  { name: 'requests', path: '/activity', mobile: true },
  { name: 'request', path: (ids) => `/activity/${ids.failover}`, fullPage: true, mobile: true },
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
  { name: 'providers', path: '/providers', fullPage: true },
  { name: 'provider-connect', path: '/providers', setup: (p) => click(p, /connect a provider|add provider/i) },
  { name: 'subscriptions', path: '/providers/subscriptions' },
  { name: 'routing', path: '/routing/plans' },
  { name: 'plan', path: (ids) => `/routing/plans/${ids.plan}`, fullPage: true },
  { name: 'plan-versions', path: (ids) => `/routing/plans/${ids.plan}?tab=versions` },
  { name: 'plan-simulator', path: (ids) => `/routing/plans/${ids.plan}?tab=test`, fullPage: true },
  { name: 'fallback-groups', path: '/routing/pools' },
  { name: 'privacy', path: '/privacy' },
  { name: 'privacy-policy', path: '/privacy', setup: (p) => click(p, /add masking policy|add policy|new policy/i) },
  { name: 'capture-rules', path: '/privacy/trace-policies' },
  { name: 'captured', path: '/traces' },
  { name: 'alerts', path: '/notifications' },
  { name: 'alert-channel', path: '/notifications', setup: (p) => click(p, /add channel/i) },
  { name: 'alert-rule', path: '/notifications/rules', setup: (p) => click(p, /add rule/i) },
  { name: 'alert-history', path: '/notifications/history' },
  { name: 'relays', path: '/events/destinations' },
  { name: 'event-stream', path: '/events' },
  { name: 'people', path: '/settings/users' },
  { name: 'api-tokens', path: '/settings/service-principals' },
  { name: 'sso', path: '/settings/auth' },
  { name: 'audit', path: '/settings/audit' },
  { name: 'diagnostics', path: '/compatibility' },
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

const browser = await chromium.launch()
const failures = []
let ids

async function capture({ theme, width, height, suffix, onlyMobile }) {
  const ctx = await browser.newContext({ viewport: { width, height }, colorScheme: theme, deviceScaleFactor: 2 })
  const page = await ctx.newPage()
  await signIn(page)
  // Demo traffic starts shortly after boot; wait until all of it has been
  // recorded (it includes a failover) so every page shows the full dataset.
  for (let i = 0; i < 60 && !(ids?.failover && ids.total >= 18); i++) {
    ids = await demoIds(page)
    if (!(ids.failover && ids.total >= 18)) await page.waitForTimeout(1000)
  }
  for (const s of shots) {
    if (only.length && !only.includes(s.name)) continue
    if (onlyMobile && !s.mobile) continue
    const file = `${out}/${s.name}${suffix}.png`
    try {
      const path = typeof s.path === 'function' ? s.path(ids) : s.path
      await page.goto(base + path)
      await settle(page)
      if (s.setup) await s.setup(page)
      await settle(page)
      await page.screenshot({ path: file, fullPage: !!s.fullPage && !onlyMobile })
      console.log('ok  ', file)
    } catch (e) {
      failures.push(`${s.name}${suffix}: ${e.message.split('\n')[0]}`)
      console.log('FAIL', file, e.message.split('\n')[0])
    }
  }
  // Signed-out screen.
  if (!only.length || only.includes('login')) {
    if (!onlyMobile) {
      const anon = await browser.newContext({ viewport: { width, height }, colorScheme: theme, deviceScaleFactor: 2 })
      const p = await anon.newPage()
      await p.goto(base + '/login')
      await settle(p)
      await p.screenshot({ path: `${out}/login${suffix}.png` })
      await anon.close()
    }
  }
  await ctx.close()
}

await capture({ theme: 'dark', width: 1440, height: 900, suffix: '-dark' })
await capture({ theme: 'light', width: 1440, height: 900, suffix: '-light' })
await capture({ theme: 'dark', width: 390, height: 844, suffix: '-mobile', onlyMobile: true })
await browser.close()

if (failures.length) {
  console.error(`\n${failures.length} shot(s) failed:\n` + failures.join('\n'))
  process.exit(1)
}
