import AxeBuilder from '@axe-core/playwright'
import { ADMIN_PAGES, expect, signIn, test } from './fixtures'

test('sign-in page has no serious accessibility violations', async ({ page }) => {
  await page.goto('/login')
  await page.waitForLoadState('networkidle')
  const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
  const serious = results.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')
  expect(serious.map((v) => `${v.id}: ${v.help} [${v.nodes.map((n) => n.target.join(" ")).join(", ")}]`)).toEqual([])
})

for (const path of ADMIN_PAGES) {
  test(`no serious accessibility violations on ${path}`, async ({ page }) => {
    await signIn(page)
    await page.goto(path)
    await page.waitForLoadState('networkidle')
    const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
    const serious = results.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')
    expect(serious.map((v) => `${v.id}: ${v.help} [${v.nodes.map((n) => n.target.join(" ")).join(", ")}]`)).toEqual([])
  })
}

test('keyboard: skip link reaches the main content', async ({ page }) => {
  await signIn(page)
  await page.goto('/')
  await page.keyboard.press('Tab')
  const focused = await page.evaluate(() => document.activeElement?.textContent ?? '')
  expect(focused).toContain('Skip to content')
})
