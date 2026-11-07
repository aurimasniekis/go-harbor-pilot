import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { ADMIN_PAGES, expect, signIn, test, waitForShell } from './fixtures'

/**
 * Serious or critical WCAG 2 A/AA violations on the current page. Polled
 * rather than checked once: a scan taken while a code-split route or a
 * transition is still settling sees a half-drawn page, whereas a real
 * violation persists and still fails once the poll times out.
 */
async function serious(page: Page): Promise<string[]> {
  const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
  return results.violations
    .filter((v) => v.impact === 'serious' || v.impact === 'critical')
    .map((v) => `${v.id}: ${v.help} [${v.nodes.map((n) => n.target.join(' ')).join(', ')}]`)
}

test('sign-in page has no serious accessibility violations', async ({ page }) => {
  await page.goto('/login')
  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible()
  await page.waitForLoadState('networkidle')
  await expect.poll(() => serious(page), { timeout: 15_000 }).toEqual([])
})

for (const path of ADMIN_PAGES) {
  test(`no serious accessibility violations on ${path}`, async ({ page }) => {
    await signIn(page)
    await page.goto(path)
    await waitForShell(page)
    await expect.poll(() => serious(page), { timeout: 15_000 }).toEqual([])
  })
}

test('keyboard: skip link reaches the main content', async ({ page }) => {
  await signIn(page)
  await page.goto('/')
  // Tab only once the shell is on screen: before the session probe answers
  // the document is empty and focus would stay on <body>.
  await waitForShell(page)
  const skip = page.getByRole('link', { name: 'Skip to content' })
  // It is the first stop in the tab order.
  await page.keyboard.press('Tab')
  await expect(skip).toBeFocused()
  // Focus brings it on screen.
  await expect(skip).toBeInViewport()
})
