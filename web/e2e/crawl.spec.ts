import { ADMIN_PAGES, expect, signIn, test } from './fixtures'

// Opens every page reachable from the navigation and fails on any render
// error or failed data load.
test('every administrator page renders without errors', async ({ page }) => {
  await signIn(page)
  for (const path of ADMIN_PAGES) {
    await page.goto(path)
    await page.waitForLoadState('networkidle')
    await expect(page.getByText('Something went wrong rendering this view'), path).toHaveCount(0)
    await expect(page.getByText('Could not load data'), path).toHaveCount(0)
  }
})

test('viewer sees no administration pages', async ({ page }) => {
  await signIn(page, { email: 'viewer@example.test', password: 'harbor-pilot-demo' })
  await expect(page.getByRole('link', { name: 'Home' })).toBeVisible()
  const res = await page.request.get('/api/v1/users')
  expect(res.status()).toBe(403)
})
