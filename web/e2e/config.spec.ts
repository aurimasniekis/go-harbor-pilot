import { expect, signIn, test, choose } from './fixtures'
import type { Page } from '@playwright/test'

// End-to-end configuration flow: provider → account (stored secret) → pool →
// alias → plan (draft, publish) → client binding → credential issued once.

const SECRET = 'sk-proj-e2e-SECRET-should-never-render-0123456789'

async function dialog(page: Page) {
  return page.getByRole('dialog')
}

test('configure a provider through to an issued client credential', async ({ page }) => {
  test.setTimeout(120_000)
  page.on('dialog', (d) => void d.accept())
  const suffix = Date.now().toString(36)
  await signIn(page)

  // Provider with one model.
  await page.goto('/providers')
  await page.getByRole('button', { name: 'Add provider' }).click()
  let d = await dialog(page)
  await d.getByLabel('Name', { exact: true }).fill(`openai-${suffix}`)
  await choose(d, 'Type', 'openai')
  await d.getByRole('button', { name: 'Add model' }).click()
  await d.getByLabel('Model id').fill('gpt-5')
  await d.getByLabel('tools', { exact: true }).check()
  await d.getByRole('button', { name: 'Save provider' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.getByText(`openai-${suffix}`)).toBeVisible()

  // Account with a stored secret.
  await page.goto('/providers/accounts')
  await page.getByRole('button', { name: 'Add account' }).click()
  d = await dialog(page)
  await d.getByLabel('Name', { exact: true }).fill(`openai-main-${suffix}`)
  await choose(d, 'Provider', { label: `openai-${suffix} (openai)` })
  await d.getByLabel('Credential', { exact: true }).fill(SECRET)
  await d.getByRole('button', { name: 'Save account' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  const row = page.getByRole('row', { name: new RegExp(`openai-main-${suffix}`) })
  await expect(row.getByText('stored')).toBeVisible()
  expect(await page.content()).not.toContain(SECRET)

  // Pool.
  await page.goto('/providers/pools')
  await page.getByRole('button', { name: 'Add pool' }).click()
  d = await dialog(page)
  await d.getByLabel('Name', { exact: true }).fill(`gpt-${suffix}`)
  await d.getByRole('button', { name: 'Add member' }).click()
  await choose(d, 'Account', { label: `openai-main-${suffix}` })
  await choose(d, 'Model', 'gpt-5', { exact: true })
  await d.getByRole('button', { name: 'Save pool' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)

  // Alias.
  await page.goto('/routing')
  await page.getByRole('button', { name: 'Add alias' }).click()
  d = await dialog(page)
  await d.getByLabel('Name', { exact: true }).fill(`coding/${suffix}`)
  await choose(d, 'Target pool', { label: `gpt-${suffix}` })
  await d.getByRole('button', { name: 'Save alias' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.getByText(`coding/${suffix}`).first()).toBeVisible()

  // Plan → draft → publish.
  await page.goto('/routing/plans')
  await page.getByRole('button', { name: 'Add plan' }).click()
  d = await dialog(page)
  await d.getByLabel('Name', { exact: true }).fill(`plan-${suffix}`)
  await d.getByRole('button', { name: 'Save plan' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.getByRole('link', { name: `plan-${suffix}` }).click()
  await page.getByRole('button', { name: 'New draft' }).click()
  d = await dialog(page)
  await d.getByRole('button', { name: 'Add mapping' }).click()
  await d.getByLabel('Requested model (exact or glob)').fill('gpt-*')
  await choose(d, 'Alias', { label: `coding/${suffix}` }, { exact: true })
  await d.getByRole('button', { name: 'Save draft' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.getByRole('button', { name: 'Validate' }).first().click()
  await expect(page.getByText('Valid.')).toBeVisible()
  await page.getByRole('button', { name: 'Publish' }).click()
  await page.getByRole('dialog', { name: /Publish v1/ }).getByRole('button', { name: 'Publish' }).click()
  await expect(page.getByText('v1').first()).toBeVisible()
  await expect(page.getByRole('cell', { name: 'published' })).toBeVisible()

  // Client binding → credential.
  await page.goto('/clients')
  await page.getByRole('button', { name: 'Add client' }).click()
  d = await dialog(page)
  await d.getByLabel('Name', { exact: true }).fill(`laptop-${suffix}`)
  await choose(d, 'Routing plan', { label: `plan-${suffix}` })
  await choose(d, 'Expected harness', 'claude_code')
  await d.getByRole('button', { name: 'Save client' }).click()
  await page.waitForURL(/\/clients\/cbn_/)
  await page.getByRole('button', { name: 'Issue credential' }).first().click()
  d = await dialog(page)
  await d.getByLabel('Credential name').fill('laptop')
  await d.getByRole('button', { name: 'Issue credential' }).click()
  const token = (await page.getByTestId('secret-value').textContent()) ?? ''
  expect(token).toMatch(/^hpk_[a-z2-7]{8}_[A-Za-z0-9_-]{43}$/)
  await expect(page.getByTestId('snippet-claude-code')).toContainText('ANTHROPIC_BASE_URL=')
  await expect(page.getByTestId('snippet-claude-code')).toContainText(token)
  await page.getByRole('button', { name: 'Done' }).click()
  await expect(page.getByText(token)).toHaveCount(0)
  await expect(page.getByText(/hpk_[a-z2-7]{8}_…/)).toBeVisible()

  // The plans list shows the published plan.
  await page.goto('/routing/plans')
  await expect(page.getByRole('link', { name: `plan-${suffix}` }).first()).toBeVisible()
})
