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

  // The log renders; a whole row opens the request's own page.
  await page.goto('/activity')
  await expect(page.getByRole('heading', { name: 'Requests', level: 1 })).toBeVisible()
  await expect(page.getByRole('group', { name: 'Last 24 hours' })).toBeVisible()
  const rows = page.getByRole('region', { name: 'Requests' }).locator('tbody tr[tabindex]')
  await expect(rows.first()).toBeVisible()
  await rows.first().click()
  await expect(page).toHaveURL(/\/activity\/req_/)
  await page.getByRole('link', { name: 'Requests', exact: true }).first().click()
  await expect(page).toHaveURL(/\/activity$/)

  // The request page answers in one sentence, then shows the route.
  await page.goto(`/activity/${target!.id}`)
  const main = page.locator('#main')
  await expect(main.getByText(/^Served by .+ after .+ was /).first()).toBeVisible()
  for (const h of ['Route', 'Attempts', 'Candidates', 'Cost']) {
    await expect(main.getByRole('heading', { name: h, level: 2 })).toBeVisible()
  }
  await expect(main.getByText(/Fell back to another (account|provider)/).first()).toBeVisible()

  const text = await main.innerText()
  expect(text).not.toContain('NaN')
  expect(text).not.toContain('undefined')

  // Tokens the provider did not report render as an em dash, never as 0.
  const missing = page.getByLabel('Not reported by the provider')
  if ((await missing.count()) > 0) await expect(missing.first()).toHaveText('—')

  // Sessions screen renders.
  await page.goto('/activity/sessions')
  await expect(page.getByRole('link', { name: 'Sessions' })).toBeVisible()
})
