import { test as base, expect, type Page } from '@playwright/test'

const allowed = new WeakMap<Page, RegExp[]>()

/**
 * Permits console errors matching pattern for this page — for tests that
 * deliberately provoke an error response (the browser logs failed loads).
 */
export function allowConsoleError(page: Page, pattern: RegExp): void {
  allowed.set(page, [...(allowed.get(page) ?? []), pattern])
}

/** Fails the test on uncaught page errors or console errors. */
export const test = base.extend<{ page: Page }>({
  page: async ({ page }, provide) => {
    const errors: string[] = []
    page.on('pageerror', (e) => errors.push(`pageerror: ${e.message}`))
    page.on('console', (m) => {
      // 401 on the initial session probe is expected before sign-in.
      if (m.type() !== 'error' || m.text().includes('401')) return
      if ((allowed.get(page) ?? []).some((re) => re.test(m.text()))) return
      errors.push(`console: ${m.text()}`)
    })
    await provide(page)
    expect(errors, 'no console or page errors').toEqual([])
  },
})

export { expect }

export const DEMO_ADMIN = { email: 'admin@example.test', password: 'harbor-pilot-demo' }

/** Signs in as the synthetic demo administrator. */
export async function signIn(page: Page, who = DEMO_ADMIN): Promise<void> {
  await page.goto('/login')
  await page.getByLabel('Email').fill(who.email)
  await page.getByLabel('Password').fill(who.password)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await page.waitForURL((u) => !u.pathname.startsWith('/login'))
}

/** Pages reachable from the navigation for an administrator. */
export const ADMIN_PAGES = [
  '/', '/account', '/activity', '/activity/sessions', '/clients', '/routing', '/routing/plans', '/providers', '/providers/accounts', '/providers/subscriptions',
  '/providers/pools', '/quotas', '/quotas/monitors', '/usage', '/usage/budgets', '/privacy', '/privacy/sandbox', '/privacy/trace-policies', '/traces',
  '/compatibility', '/compatibility/watch', '/compatibility/work-items', '/workspaces', '/settings/users', '/settings/service-principals', '/settings/auth', '/settings/audit',
]
