import { expect, signIn, test } from './fixtures'

test('unknown pages have a heading and a way back', async ({ page }) => {
  await signIn(page)
  await page.goto('/does-not-exist')
  await expect(page.getByRole('heading', { name: 'Page not found', level: 1 })).toBeVisible()
  await expect(page).toHaveTitle(/Page not found/)
  await page.getByRole('link', { name: 'Go to Home' }).click()
  await expect(page).toHaveURL(/\/$/)
})

test('the command palette reaches pages and actions by plain words', async ({ page }) => {
  await signIn(page)
  const open = async () => {
    await page.getByRole('button', { name: 'Search pages and actions' }).click()
    return page.getByPlaceholder('Search pages and actions')
  }
  const cases: [string, string, RegExp][] = [
    ['statistics', 'Spend breakdown', /\/usage\/breakdown/],
    ['account', 'My account', /\/account$/],
    ['tracker', 'Quota tracking', /\/quotas\/monitors/],
    // Last: it opens the budget dialog over the page.
    ['add budget', 'Add a budget', /\/quotas/],
  ]
  for (const [query, label, url] of cases) {
    const input = await open()
    await input.fill(query)
    const option = page.getByRole('button', { name: new RegExp(`^${label}`) }).or(page.getByRole('option', { name: new RegExp(`^${label}`) })).first()
    await expect(option, `"${query}" finds ${label}`).toBeVisible()
    await option.click()
    await expect(page).toHaveURL(url)
  }
})

test.describe('phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })
  test('the palette shows its actions and the drawer adds only the remaining pages', async ({ page }) => {
    await signIn(page)
    await page.getByRole('button', { name: 'Search', exact: true }).click()
    await page.getByPlaceholder('Search pages and actions').fill('sign out')
    await expect(page.getByRole('button', { name: /^Sign out/ }).or(page.getByRole('option', { name: /^Sign out/ })).first()).toBeInViewport()
    await page.keyboard.press('Escape')
    await page.getByRole('button', { name: 'More pages' }).click()
    const drawer = page.getByRole('navigation', { name: 'Main navigation' })
    await expect(drawer.getByRole('link', { name: 'Clients' })).toBeVisible()
    await expect(drawer.getByRole('link', { name: 'Requests' })).toHaveCount(0)
  })
})
