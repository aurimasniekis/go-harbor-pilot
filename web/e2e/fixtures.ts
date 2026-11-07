import { test as base, expect, type Locator, type Page } from '@playwright/test'

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
  await page.getByLabel('Password', { exact: true }).fill(who.password)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await page.waitForURL((u) => !u.pathname.startsWith('/login'))
}

/**
 * Waits until the signed-in application shell has rendered and the page has
 * finished loading its data. The root renders nothing until the session
 * probe answers, and routes are code-split, so `networkidle` alone can fire
 * before the page is on screen: keyboard and accessibility checks must not
 * start until the skip link and main landmark exist and no loading skeleton
 * (`aria-busy`) is left.
 */
export async function waitForShell(page: Page): Promise<void> {
  await expect(page.getByRole('link', { name: 'Skip to content' })).toBeAttached()
  await expect(page.locator('#main')).toBeVisible()
  await page.waitForLoadState('networkidle')
  await expect(page.locator('[aria-busy="true"]')).toHaveCount(0)
}

/** Pages reachable from the navigation for an administrator. */
export const ADMIN_PAGES = [
  '/', '/account', '/activity', '/activity/sessions', '/clients', '/clients/setup', '/routing', '/routing/plans', '/providers', '/routing/pools', '/quotas', '/quotas/monitors', '/usage', '/usage/breakdown', '/privacy', '/privacy/sandbox', '/privacy/trace-policies', '/traces',
  '/compatibility', '/compatibility/watch', '/compatibility/work-items', '/events', '/events/catalog', '/events/destinations',
  '/notifications', '/notifications/rules', '/notifications/history', '/workspaces', '/settings/users', '/settings/service-principals', '/settings/auth', '/settings/audit', '/settings/system',
]

/**
 * Picks an option in a Mantine Select labelled `label` inside `scope`.
 * `option` is the option value, or `{ label }` to match the visible text.
 */
export async function choose(
  scope: Page | Locator,
  label: string | RegExp,
  option: string | { label: string | RegExp },
  opts: { exact?: boolean } = {},
): Promise<void> {
  const page = isPage(scope) ? scope : scope.page()
  // The label also names the (closed) listbox; target the input.
  await scope.getByLabel(label, { exact: opts.exact }).and(scope.locator('input:not([type="hidden"])')).first().click()
  const list = page.getByRole('listbox')
  const opt =
    typeof option === 'string'
      ? list.locator(`[role="option"][value="${option}"]`)
      : list.getByRole('option', { name: option.label, exact: typeof option.label === 'string' })
  await opt.first().click()
}

function isPage(x: Page | Locator): x is Page {
  return typeof (x as Page).goto === 'function'
}

/** Opens a row's "⋯" menu and runs an item ("Edit", "Delete", …). */
export async function rowAction(page: Page, name: string, item: string): Promise<void> {
  await page.getByRole('button', { name: `Actions for ${name}`, exact: true }).click()
  await page.getByRole('menuitem', { name: new RegExp(`^${item}`) }).click()
}

/** Deletes a row through its menu and confirms. */
export async function deleteRow(page: Page, name: string): Promise<void> {
  await rowAction(page, name, 'Delete')
  await page.getByRole('dialog').getByRole('button', { name: /^(Delete|Remove|Revoke|Discard)$/ }).click()
}
