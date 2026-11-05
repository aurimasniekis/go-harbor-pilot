import { expect, signIn, test } from './fixtures'

// The demo seeds synthetic gateway traffic in the background shortly after
// startup, including scripted failovers. This test opens one request that
// needed more than one attempt and checks the "Why this route?" view.

interface Item {
  id: string
  attempts: number
}

test('activity explains a request that failed over', async ({ page }) => {
  test.setTimeout(60_000)
  await signIn(page)

  let target: Item | undefined
  const deadline = Date.now() + 15_000
  while (!target && Date.now() < deadline) {
    const res = await page.request.get('/api/v1/activity/requests?limit=100')
    expect(res.ok()).toBeTruthy()
    const body = (await res.json()) as { items: Item[] | null }
    target = (body.items ?? []).find((r) => r.attempts > 1)
    if (!target) await page.waitForTimeout(500)
  }
  expect(target, 'demo traffic produced a request with a failover').toBeDefined()

  // The list renders and links to the request.
  await page.goto('/activity')
  await expect(page.getByRole('heading', { name: 'Activity', level: 1 })).toBeVisible()
  await expect(page.locator('table.data-table tbody tr').first()).toBeVisible()

  await page.goto(`/activity/${target!.id}`)
  await expect(page.getByRole('heading', { name: 'Candidates' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Attempts' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Accounting' })).toBeVisible()
  await expect(page.getByText(/alternate_account|alternate_provider/).first()).toBeVisible()

  const text = await page.locator('main').innerText()
  expect(text).not.toContain('NaN')
  expect(text).not.toContain('undefined')

  // Tokens the upstream did not report render as an em dash, never as 0.
  const missing = page.getByLabel('not reported by upstream')
  if ((await missing.count()) > 0) await expect(missing.first()).toHaveText('—')

  // Sessions screen renders.
  await page.goto('/activity/sessions')
  await expect(page.getByRole('link', { name: 'Sessions' })).toBeVisible()
})
