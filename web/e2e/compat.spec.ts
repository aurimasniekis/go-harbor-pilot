import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { allowConsoleError, expect, signIn, test, choose } from './fixtures'

// The demo has no breakers or protocol changes, so this covers the empty
// states, accessibility, and the plan editor's compatibility section
// (including the server's passthrough validation).

async function axeSerious(page: Page): Promise<string[]> {
  const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
  return results.violations
    .filter((v) => v.impact === 'serious' || v.impact === 'critical')
    .map((v) => `${v.id}: ${v.help} [${v.nodes.map((n) => n.target.join(' ')).join(', ')}]`)
}

test('compatibility and Protocol Watch empty states', async ({ page }) => {
  await signIn(page)
  // Diagnostics live under Settings; the page is reached directly or from Home when something is wrong.
  await page.goto('/compatibility')
  await expect(page.getByRole('heading', { name: 'Settings', level: 1 })).toBeVisible()
  await expect(page.getByText('No degraded routes')).toBeVisible()
  await expect(page.getByText('No breakers yet')).toBeVisible()
  // No degradation banner while every path routes normally.
  await expect(page.getByText('Degraded routing.')).toHaveCount(0)
  expect(await axeSerious(page)).toEqual([])

  await page.getByRole('link', { name: 'Protocol changes' }).click()
  await expect(page.getByText('No protocol changes observed')).toBeVisible()
  // The caveats about recency only show once there is something to qualify.
  await expect(page.getByText(/not proof a field or event was removed/)).toHaveCount(0)
  await expect(page.getByText(/none dropped/)).toBeVisible()
  expect(await axeSerious(page)).toEqual([])

  await page.getByRole('link', { name: 'Follow-ups' }).click()
  await expect(page.getByText('No follow-ups', { exact: true })).toBeVisible()
  await choose(page, 'Status', 'resolved')
  await expect(page).toHaveURL(/status=resolved/)
  await expect(page.getByText('No resolved follow-ups')).toBeVisible()
  expect(await axeSerious(page)).toEqual([])
})

test('plan compatibility section validates passthrough losses', async ({ page }) => {
  test.setTimeout(60_000)
  allowConsoleError(page, /422/)
  await signIn(page)
  const name = `compat-${Date.now().toString(36)}`
  await page.goto('/routing/plans')
  await page.getByRole('button', { name: 'Add plan' }).click()
  let d = page.getByRole('dialog')
  await d.getByLabel('Name', { exact: true }).fill(name)
  await d.getByRole('button', { name: 'Save plan' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.waitForURL(/\/routing\/plans\/pln_/)

  await page.getByRole('button', { name: 'New draft' }).click()
  d = page.getByRole('dialog')
  await choose(d, 'Default alias', { label: 'coding/default' })
  await d.getByRole('tab', { name: 'Compatibility' }).click()
  await choose(d, 'When a breaker is open', 'passthrough')
  await expect(d.getByText(/Native passthrough always loses Detailed usage and Tool statistics/)).toBeVisible()
  expect(await axeSerious(page)).toEqual([])

  // The server refuses passthrough without the required losses.
  await d.getByRole('button', { name: 'Save draft' }).click()
  await expect(d.getByRole('alert').filter({ hasText: /native passthrough always loses detailed_usage and tool_statistics/i }).first()).toBeVisible()

  await d.getByLabel('Detailed usage').check()
  await d.getByLabel('Tool statistics').check()
  await expect(d.getByText(/Native passthrough always loses Detailed usage/)).toHaveCount(0)
  await choose(d, 'Unknown usage', 'estimate')
  await d.getByRole('button', { name: 'Save draft' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.getByRole('tab', { name: 'Versions' }).click()
  await page.getByRole('button', { name: 'Validate' }).first().click()
  await expect(page.getByText('Valid.')).toBeVisible()

  // The saved policy round-trips through the editor.
  await page.getByRole('button', { name: 'Edit draft' }).click()
  d = page.getByRole('dialog')
  await d.getByRole('tab', { name: 'Compatibility' }).click()
  await expect(d.getByLabel('When a breaker is open').and(d.locator('input'))).toHaveValue(/passthrough/i)
  await expect(d.getByLabel('Detailed usage')).toBeChecked()
  await expect(d.getByLabel('Unknown usage').and(d.locator('input'))).toHaveValue(/estimat/i)
  await d.getByRole('button', { name: 'Cancel' }).click()
})
