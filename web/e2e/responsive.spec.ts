import { ADMIN_PAGES, expect, signIn, test } from './fixtures'

test('no horizontal page scroll on a phone', async ({ page }) => {
  await signIn(page)
  for (const path of ADMIN_PAGES) {
    await page.goto(path)
    await page.waitForLoadState('networkidle')
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow, path).toBeLessThanOrEqual(1)
  }
})
