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
  const suffix = Date.now().toString(36)
  await signIn(page)

  // Provider with one model.
  await page.goto('/providers')
  await page.getByRole('button', { name: 'Connect a provider' }).click()
  let d = await dialog(page)
  await d.getByRole('radio', { name: /^OpenAI API/ }).click()
  await d.getByLabel('Name', { exact: true }).fill(`openai-${suffix}`)
  await d.getByRole('button', { name: /Models and prices/ }).click()
  await d.getByRole('button', { name: 'Add model' }).click()
  await d.getByLabel('Model id').fill('gpt-5')
  await d.getByLabel('tools', { exact: true }).check()
  await d.getByRole('button', { name: 'Connect provider' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.getByRole('heading', { name: `openai-${suffix}` })).toBeVisible()

  // Account with a stored secret, added from its provider.
  await page.getByRole('button', { name: `Actions for openai-${suffix}` }).click()
  await page.getByRole('menuitem', { name: 'Add account' }).click()
  d = await dialog(page)
  await d.getByLabel('Name', { exact: true }).fill(`openai-main-${suffix}`)
  await d.getByLabel('Credential', { exact: true }).fill(SECRET)
  await d.getByRole('button', { name: 'Save account' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  const row = page.getByRole('row', { name: new RegExp(`openai-main-${suffix}`) })
  await row.click()
  await expect(page.getByRole('dialog', { name: new RegExp(`openai-main-${suffix}`) }).getByText('Saved (encrypted)')).toBeVisible()
  await page.keyboard.press('Escape')
  expect(await page.content()).not.toContain(SECRET)

  // Fallback group.
  await page.goto('/routing/pools')
  await page.getByRole('button', { name: 'Add fallback group' }).click()
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

  // Client → access token (the token dialog opens right after creating).
  await page.goto('/clients')
  await page.getByRole('button', { name: 'Add client' }).click()
  d = await dialog(page)
  await d.getByLabel('Name', { exact: true }).fill(`laptop-${suffix}`)
  await expect(d.getByLabel('Short name')).toHaveValue(`laptop-${suffix}`)
  await d.getByRole('radio', { name: /Claude Code/ }).click()
  await choose(d, 'Routing plan', { label: `plan-${suffix}` })
  await d.getByRole('button', { name: 'Create client and token' }).click()
  await page.waitForURL(/\/clients\/cbn_/)
  d = page.getByRole('dialog', { name: 'Create access token' })
  await d.getByLabel('Token name').fill('laptop')
  await d.getByRole('button', { name: 'Create token' }).click()
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
