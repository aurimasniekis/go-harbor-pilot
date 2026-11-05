import { expect, signIn, test, choose } from './fixtures'

// Statistics, harness setup guides and backups over the demo instance.

test('statistics break down activity by dimension', async ({ page }) => {
  test.setTimeout(60_000)
  await signIn(page)
  // The demo seeds traffic shortly after startup; wait until requests exist.
  await expect
    .poll(async () => {
      const r = await page.request.get('/api/v1/activity/requests?limit=5')
      return ((await r.json()) as { items: unknown[] | null }).items?.length ?? 0
    }, { timeout: 20_000 })
    .toBeGreaterThan(0)

  // The old address redirects to the Spend breakdown.
  await page.goto('/activity/stats')
  await expect(page).toHaveURL(/\/usage\/breakdown$/)
  await expect(page.getByRole('heading', { name: 'Spend', level: 1 })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Breakdown' })).toHaveAttribute('aria-current', 'page')
  // One caption names the range; the tiles carry plain labels.
  const tiles = page.getByRole('group', { name: /^Activity in the last 30 days/ })
  await expect(tiles.getByText('Requests', { exact: true })).toBeVisible()
  await expect(tiles.getByText(/^\d+ failed · \d+ blocked$/)).toBeVisible()
  const table = page.getByRole('region', { name: 'Breakdown by client' })
  await expect(table.locator('tbody tr').first()).toBeVisible()
  // Rows drill down into the request log.
  await expect(table.getByRole('link').first()).toHaveAttribute('href', /\/activity\?/)

  await page.locator('label').filter({ hasText: /^Cost$/ }).click()
  await expect(page).toHaveURL(/metric=cost/)
  await page.locator('label').filter({ hasText: /^Latency$/ }).click()
  await expect(page.getByRole('img', { name: 'Latency per day' }).or(page.getByText('No requests in this range'))).toBeVisible()

  // Detail columns stay behind a switch so the default table fits.
  await choose(page, 'Group by', 'intent')
  await expect(page).toHaveURL(/by=intent/)
  const byPurpose = page.getByRole('region', { name: 'Breakdown by purpose' })
  await expect(byPurpose.getByRole('columnheader', { name: 'Classifier runs' })).toHaveCount(0)
  await page.getByLabel('More columns').check({ force: true })
  await expect(byPurpose.getByRole('columnheader', { name: 'Classifier runs' })).toBeVisible()
  await choose(page, 'Group by', 'model')
  await expect(page.getByRole('region', { name: 'Breakdown by model' }).getByRole('columnheader', { name: /^Model/ })).toBeVisible()
})

test('harness setup guides fill in commands without a token', async ({ page }) => {
  await signIn(page)
  await page.goto('/clients')
  await page.getByRole('link', { name: 'Set up a tool' }).click()
  await expect(page.getByRole('heading', { name: 'Set up a tool', level: 1 })).toBeVisible()

  await page.getByRole('button', { name: 'Change' }).click()
  await page.getByLabel('Harbor Pilot address').fill('https://hp.example.test/')
  await page.locator('label', { hasText: /^Claude Code$/ }).click()
  const claude = page.getByLabel('Claude Code configuration', { exact: true })
  await expect(claude).toContainText('"ANTHROPIC_BASE_URL": "https://hp.example.test"')
  await expect(claude).toContainText('"apiKeyHelper"')

  await page.locator('label', { hasText: /^Codex$/ }).click()
  await expect(page).toHaveURL(/tool=codex/)
  await expect(page.getByText(/not yet tested with a live install/)).toBeVisible()
  await expect(page.getByLabel('Codex configuration', { exact: true })).toContainText('base_url = "https://hp.example.test/v1"')
  await page.getByRole('tab', { name: 'Use the harbor-pilot CLI' }).click()
  await expect(page.getByLabel(/^Issue a credential and apply setup/)).toContainText('| harbor-pilot harness setup codex --url https://hp.example.test --token-stdin')

  await expect(page.locator('main')).not.toContainText(/hpk_[A-Za-z0-9]/)
})

test('backups can be taken, downloaded and deleted; restore stays on the CLI', async ({ page }) => {
  await signIn(page)
  await page.goto('/settings/system')
  await expect(page.getByRole('heading', { name: 'Backups', exact: true })).toBeVisible()
  await expect(page.getByLabel('Restore commands', { exact: true })).toContainText('harbor-pilot restore --from DIR --yes')
  await expect(page.getByRole('button', { name: /^restore/i })).toHaveCount(0)

  await page.getByRole('button', { name: 'Take backup' }).click()
  const d = page.getByRole('dialog')
  await d.getByLabel('Include captured content').check()
  await expect(d.getByText(/will contain encrypted captured content/)).toBeVisible()
  await d.getByLabel('Include captured content').uncheck()
  await d.getByRole('button', { name: 'Take backup' }).click()
  await expect(page.getByRole('status').filter({ hasText: 'Backup taken' })).toBeVisible()
  const table = page.getByRole('region', { name: 'Backup list' })
  const row = table.locator('tbody tr').first()
  await expect(row).toContainText('Verified')
  await expect(row).toContainText('Configuration and data')

  const [download] = await Promise.all([page.waitForEvent('download'), row.getByRole('link', { name: /^Download backup/ }).click()])
  expect(download.suggestedFilename()).toMatch(/^harbor-pilot-backup-.+\.tar\.gz$/)

  await row.getByRole('button', { name: /^More actions for backup/ }).click()
  await page.getByRole('menuitem', { name: 'Delete…' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Delete backup' }).click()
  await expect(page.getByRole('status').filter({ hasText: 'Deleted the backup' })).toBeVisible()
  await expect(page.getByText('No backups yet')).toBeVisible()
})
