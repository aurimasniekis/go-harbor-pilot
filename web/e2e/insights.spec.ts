import { expect, signIn, test } from './fixtures'

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

  await page.goto('/activity/stats')
  await expect(page.getByRole('heading', { name: 'Activity', level: 1 })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Statistics' })).toHaveAttribute('aria-current', 'page')
  await expect(page.getByText('Unknown usage').first()).toBeVisible()
  await expect(page.getByText(/^\d+ ok · \d+ errors · \d+ blocked$/)).not.toHaveText('0 ok · 0 errors · 0 blocked')
  const table = page.getByRole('region', { name: 'Statistics breakdown' })
  await expect(table.locator('tbody tr').first()).toBeVisible()

  await page.getByRole('tab', { name: 'Cost' }).click()
  await page.getByRole('tab', { name: 'Latency' }).click()
  await expect(page.getByRole('img', { name: 'latency per period' }).or(page.getByText('No requests in this range'))).toBeVisible()

  await page.getByLabel('Group by').selectOption('intent')
  await expect(table.getByRole('columnheader', { name: 'Classifier runs' })).toBeVisible()
  await page.getByLabel('Group by').selectOption('model')
  await expect(table.getByRole('columnheader', { name: 'Model (served)' })).toBeVisible()
})

test('harness setup guides fill in commands without a token', async ({ page }) => {
  await signIn(page)
  await page.goto('/clients')
  await page.getByRole('link', { name: 'Harness setup' }).click()
  await expect(page.getByRole('heading', { name: 'Harness setup', level: 1 })).toBeVisible()

  await page.getByLabel('Harbor Pilot URL').fill('https://hp.example.test/')
  const claude = page.getByLabel('Claude Code configuration', { exact: true })
  await expect(claude).toContainText('"ANTHROPIC_BASE_URL": "https://hp.example.test"')
  await expect(claude).toContainText('"apiKeyHelper"')

  await page.getByRole('tab', { name: 'Codex' }).click()
  await expect(page.getByText(/not yet been checked against a live Codex install/)).toBeVisible()
  await expect(page.getByLabel('Codex configuration', { exact: true })).toContainText('base_url = "https://hp.example.test/v1"')
  await expect(page.getByLabel(/^Issue a credential and apply setup/)).toContainText('| harbor-pilot harness setup codex --url https://hp.example.test --token-stdin')

  await expect(page.locator('main')).not.toContainText(/hpk_[A-Za-z0-9]/)
})

test('backups can be taken and restore stays on the CLI', async ({ page }) => {
  await signIn(page)
  await page.goto('/settings/system')
  await expect(page.getByRole('heading', { name: 'Backups', exact: true })).toBeVisible()
  await expect(page.getByLabel('Restore commands', { exact: true })).toContainText('harbor-pilot restore --from DIR --yes')
  await expect(page.getByRole('button', { name: /^restore/i })).toHaveCount(0)

  await page.getByRole('button', { name: 'Take backup' }).click()
  const d = page.getByRole('dialog')
  await d.getByLabel('Include trace content (traces.db)').check()
  await expect(d.getByText(/will contain encrypted trace content/)).toBeVisible()
  await d.getByLabel('Include trace content (traces.db)').uncheck()
  await d.getByRole('button', { name: 'Take backup' }).click()
  await expect(page.getByText(/Backup written to/)).toBeVisible()
  const table = page.getByRole('region', { name: 'Backup list' })
  await expect(table.locator('tbody tr').first()).toContainText('Verified')
  await expect(table.locator('tbody tr').first()).toContainText('harbor.db')
})
