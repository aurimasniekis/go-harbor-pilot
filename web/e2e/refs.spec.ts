import { expect, signIn, test } from './fixtures'
import type { Page } from '@playwright/test'

// The demo's orchestrator client makes requests with two run tokens, each
// bound to its own spaceman.run reference: run_synthetic_1 (two requests)
// and run_synthetic_2 (one request).

async function waitForRuns(page: Page): Promise<void> {
  const deadline = Date.now() + 20_000
  for (;;) {
    const res = await page.request.get('/api/v1/activity/requests?ref=spaceman.run:run_synthetic_2&limit=1')
    expect(res.ok()).toBeTruthy()
    const body = (await res.json()) as { items: unknown[] | null }
    if ((body.items ?? []).length > 0) return
    expect(Date.now(), 'demo traffic made requests with run tokens').toBeLessThan(deadline)
    await page.waitForTimeout(500)
  }
}

test('requests filter by a run reference', async ({ page }) => {
  test.setTimeout(60_000)
  await signIn(page)
  await waitForRuns(page)

  await page.goto('/activity')
  await page.getByRole('button', { name: 'More filters' }).click()
  await expect(page.getByLabel('Reference', { exact: true }).and(page.locator('input'))).toHaveValue('spaceman.run')
  await page.getByLabel('Value', { exact: true }).fill('run_synthetic_1')
  await page.getByRole('button', { name: 'Add filter' }).click()

  await expect(page).toHaveURL(/[?&]ref=/)
  await expect(page.getByText('Run: run_synthetic_1', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Filters (1)' })).toBeVisible()
  const rows = page.getByRole('region', { name: 'Requests' }).locator('tbody tr[tabindex]')
  await expect(rows).toHaveCount(2)
  await expect(page.getByText('2 requests', { exact: false })).toBeVisible()

  // The link survives a reload.
  await page.reload()
  await expect(rows).toHaveCount(2)

  // A request shows its run; the link leads back to everything in that run.
  await rows.first().click()
  await expect(page).toHaveURL(/\/activity\/req_/)
  await page.getByRole('button', { name: 'Verified context' }).click()
  await expect(page.getByText('spaceman.run', { exact: true })).toBeVisible()
  await page.getByRole('link', { name: 'All requests with this run' }).click()
  await expect(page).toHaveURL(/\/activity\?.*ref=/)
  await expect(page.getByText('Run: run_synthetic_1', { exact: true })).toBeVisible()
  await expect(rows).toHaveCount(2)

  // Removing the filter shows every request again.
  await page.getByRole('button', { name: 'Remove Run: run_synthetic_1' }).click()
  await expect(page).not.toHaveURL(/[?&]ref=/)
})

test('spend groups by run with separate totals', async ({ page }) => {
  test.setTimeout(60_000)
  await signIn(page)
  await waitForRuns(page)

  await page.goto('/usage')
  await page.getByRole('radiogroup', { name: 'Group by' }).locator('label').filter({ hasText: /^Run$/ }).click()
  await expect(page).toHaveURL(/group=ref%3Aspaceman\.run|group=ref:spaceman\.run/)
  const table = page.getByRole('region', { name: 'Usage by group' })
  const run1 = table.getByRole('row').filter({ hasText: 'run_synthetic_1' })
  const run2 = table.getByRole('row').filter({ hasText: 'run_synthetic_2' })
  await expect(run1).toHaveCount(1)
  await expect(run2).toHaveCount(1)
  await expect(table.getByRole('columnheader', { name: 'Run' })).toBeVisible()
  // Each run has its own request count and cost.
  await expect(run1).toContainText('2')
  await expect(run2).toContainText('1')
  const cost1 = (await run1.innerText()).match(/\$[\d.,]+/)?.[0]
  const cost2 = (await run2.innerText()).match(/\$[\d.,]+/)?.[0]
  expect(cost1, 'run 1 has a cost').toBeTruthy()
  expect(cost2, 'run 2 has a cost').toBeTruthy()
  expect(cost1).not.toEqual(cost2)
  // Usage without a run is its own row.
  await expect(table.getByText('No run', { exact: true })).toBeVisible()

  // Narrow the page to one run: totals and the chart follow, unfilterable parts hide.
  await page.getByRole('button', { name: 'Filter by reference' }).click()
  await page.getByLabel('Value', { exact: true }).fill('run_synthetic_2')
  await page.getByRole('button', { name: 'Add filter' }).click()
  await expect(page.getByText('Run: run_synthetic_2', { exact: true }).first()).toBeVisible()
  const totals = page.getByRole('group', { name: /^Spend for the/ })
  await expect(totals.getByText('Requests', { exact: true })).toBeVisible()
  await expect(totals).toContainText(cost2!)
  // The chart over time follows the filter.
  await expect(page.getByRole('heading', { name: /Daily cost/ })).toBeVisible()
  await expect(table.getByRole('row').filter({ hasText: 'run_synthetic_1' })).toHaveCount(0)

  // A run's row opens its requests.
  await run2.click()
  await expect(page).toHaveURL(/\/activity\?.*ref=/)
  await expect(page.getByRole('region', { name: 'Requests' }).locator('tbody tr[tabindex]')).toHaveCount(1)
})
