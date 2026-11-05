import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { allowConsoleError, expect, signIn, test } from './fixtures'

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
  await expect(page.getByRole('heading', { name: 'Compatibility', level: 1 })).toBeVisible()
  await expect(page.getByText('No degraded routes')).toBeVisible()
  await expect(page.getByText('No breakers yet')).toBeVisible()
  // No degradation banner while every path routes normally.
  await expect(page.getByText('Degraded routing.')).toHaveCount(0)
  expect(await axeSerious(page)).toEqual([])

  await page.getByRole('link', { name: 'Protocol Watch' }).click()
  await expect(page.getByText('No protocol changes observed')).toBeVisible()
  await expect(page.getByText(/not proof a field or event was removed/)).toBeVisible()
  await expect(page.getByText(/none dropped/)).toBeVisible()
  expect(await axeSerious(page)).toEqual([])

  await page.getByRole('link', { name: 'Work items' }).click()
  await expect(page.getByText('No work items', { exact: true })).toBeVisible()
  await page.getByLabel('Status').selectOption('resolved')
  await expect(page).toHaveURL(/status=resolved/)
  await expect(page.getByText('No resolved work items')).toBeVisible()
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
  await page.getByRole('link', { name }).click()

  await page.getByRole('button', { name: 'New draft' }).click()
  d = page.getByRole('dialog')
  await d.getByLabel('Default alias').selectOption({ label: 'coding/default' })
  await d.getByRole('tab', { name: 'Compatibility' }).click()
  await d.getByLabel('When a breaker is open').selectOption('passthrough')
  await expect(d.getByText(/Native passthrough always loses Detailed usage and Tool statistics/)).toBeVisible()
  expect(await axeSerious(page)).toEqual([])

  // The server refuses passthrough without the required losses.
  await d.getByRole('button', { name: 'Save draft' }).click()
  await expect(d.getByRole('alert').filter({ hasText: /allowed_losses: native passthrough always loses detailed_usage and tool_statistics/ }).first()).toBeVisible()

  await d.getByLabel('Detailed usage').check()
  await d.getByLabel('Tool statistics').check()
  await expect(d.getByText(/Native passthrough always loses/)).toHaveCount(0)
  await d.getByLabel('Unknown usage').selectOption('estimate')
  await d.getByRole('button', { name: 'Save draft' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.getByRole('button', { name: 'Validate' }).first().click()
  await expect(page.getByText('Valid.')).toBeVisible()

  // The saved policy round-trips through the editor.
  await page.getByRole('button', { name: 'Edit' }).click()
  d = page.getByRole('dialog')
  await d.getByRole('tab', { name: 'Compatibility' }).click()
  await expect(d.getByLabel('When a breaker is open')).toHaveValue('passthrough')
  await expect(d.getByLabel('Detailed usage')).toBeChecked()
  await expect(d.getByLabel('Unknown usage')).toHaveValue('estimate')
  await d.getByRole('button', { name: 'Cancel' }).click()
})
