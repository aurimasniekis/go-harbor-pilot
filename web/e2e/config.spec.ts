import { expect, signIn, test, choose } from './fixtures'
import type { Page } from '@playwright/test'

// End-to-end configuration flow: provider with its key (tested) → pool →
// alias → plan (draft, publish) → client with its token → token rotation.

const SECRET = 'sk-proj-e2e-SECRET-should-never-render-0123456789'

async function dialog(page: Page) {
  return page.getByRole('dialog')
}

test('configure a provider through to an issued client credential', async ({ page }) => {
  test.setTimeout(120_000)
  const suffix = Date.now().toString(36)
  await signIn(page)

  // Provider with one model and its first key, tested in the same step.
  await page.goto('/providers')
  await page.getByRole('button', { name: 'Connect a provider' }).click()
  const connect = page.getByRole('dialog', { name: 'Connect a provider' })
  await connect.getByRole('radio', { name: /^OpenAI-compatible API/ }).click()
  await connect.getByLabel('Display name').fill(`E2E ${suffix}`)
  await connect.getByLabel('Short name').fill(`openai-${suffix}`)
  await connect.getByLabel('Base URL').fill(`${new URL(page.url()).origin}/_demo/upstream/openai`)
  await connect.getByLabel('API key', { exact: true }).fill(SECRET)
  await connect.getByRole('button', { name: 'Connection', exact: true }).click()
  const privateNet = connect.getByRole('switch', { name: 'Allow a local network address' })
  await privateNet.dispatchEvent('click')
  await expect(privateNet).toBeChecked()
  await connect.getByRole('button', { name: /Models and prices/ }).click()
  await connect.getByRole('button', { name: 'Add model' }).click()
  const model = page.getByRole('dialog', { name: 'Add a model' })
  await model.getByLabel('Model id').fill('gpt-5')
  await model.getByLabel('Tool calls', { exact: true }).check()
  await model.getByRole('button', { name: 'Done' }).click()
  await expect(connect.getByText('gpt-5')).toBeVisible()
  await connect.getByRole('button', { name: 'Connect and test' }).click()

  // The new account opens with the result of a real connection check.
  const drawer = page.getByRole('dialog', { name: /API key/ })
  await expect(drawer.getByText('Connection works')).toBeVisible()
  await expect(drawer.getByText('Saved in Harbor Pilot (encrypted)')).toBeVisible()
  await expect(page).toHaveURL(/[?&]account=acc_/)
  await page.keyboard.press('Escape')
  await expect(page.getByRole('heading', { name: `E2E ${suffix}` })).toBeVisible()
  expect(await page.content()).not.toContain(SECRET)

  // A provider with accounts cannot be deleted until they are gone.
  await page.getByRole('button', { name: `Actions for E2E ${suffix}` }).click()
  await page.getByRole('menuitem', { name: 'Delete provider' }).click()
  await expect(page.getByRole('dialog', { name: `E2E ${suffix} still has accounts` })).toContainText('API key')
  await page.keyboard.press('Escape')

  // Fallback group: accounts tried in order.
  await page.goto('/routing/pools')
  await page.getByRole('button', { name: 'Add fallback group' }).click()
  let d = await dialog(page)
  await d.getByLabel('Short name').fill(`gpt-${suffix}`)
  await d.getByRole('button', { name: 'Add account' }).click()
  await choose(d, 'Row 1: account', { label: `API key (E2E ${suffix})` })
  await choose(d, 'Row 1: model', 'gpt-5', { exact: true })
  await d.getByRole('button', { name: 'Save fallback group' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)

  // Model name.
  await page.goto('/routing')
  await page.getByRole('button', { name: 'Add model name' }).click()
  d = await dialog(page)
  await d.getByLabel('Model name', { exact: true }).fill(`coding/${suffix}`)
  await choose(d, 'Fallback group', { label: `gpt-${suffix}` }, { exact: true })
  await d.getByRole('button', { name: 'Save model name' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.getByText(`coding/${suffix}`).first()).toBeVisible()

  // Plan → draft → check → publish.
  await page.goto('/routing/plans')
  await page.getByRole('button', { name: 'Add plan' }).click()
  d = await dialog(page)
  await d.getByLabel('Short name').fill(`plan-${suffix}`)
  await d.getByRole('button', { name: 'Create plan' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.waitForURL(/\/routing\/plans\/pln_/)
  await page.getByRole('button', { name: 'New draft' }).click()
  d = await dialog(page)
  await d.getByRole('button', { name: 'Add a row' }).click()
  await d.getByLabel('Row 1: requested model').fill('gpt-*')
  await choose(d, 'Row 1: model name', { label: `coding/${suffix}` }, { exact: true })
  await d.getByRole('button', { name: 'Save draft' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.getByRole('button', { name: 'Check for problems' }).first().click()
  await expect(page.getByText(/no problems found/).first()).toBeVisible()
  await page.getByRole('button', { name: 'Publish v1' }).click()
  await page.getByRole('dialog', { name: /Publish v1/ }).getByRole('button', { name: 'Publish' }).click()
  await expect(page.getByText('Live v1').first()).toBeVisible()

  // Client → access token: creating the client creates its first token.
  await page.goto('/clients')
  await page.getByRole('button', { name: 'Add client' }).click()
  d = await dialog(page)
  await d.getByLabel('Name', { exact: true }).fill(`laptop-${suffix}`)
  await expect(d.getByLabel('Short name')).toHaveValue(`laptop-${suffix}`)
  await d.getByRole('radio', { name: /Claude Code/ }).click()
  await choose(d, 'Routing plan', { label: `plan-${suffix}` })
  await d.getByRole('button', { name: 'Create client and token' }).click()
  await page.waitForURL(/\/clients\/cbn_/)
  let reveal = page.getByRole('dialog', { name: 'Access token created' })
  const token = (await reveal.getByTestId('secret-value').textContent()) ?? ''
  expect(token).toMatch(/^hpk_[a-z2-7]{8}_[A-Za-z0-9_-]{43}$/)
  await expect(reveal.getByLabel('Claude Code configuration', { exact: true })).toContainText('ANTHROPIC_BASE_URL')
  // The token goes into a file, never into the configuration snippet.
  await expect(reveal.getByLabel('Claude Code configuration', { exact: true })).not.toContainText(token)
  await page.keyboard.press('Escape')
  await expect(reveal).toBeVisible()
  await reveal.getByRole('button', { name: "I've copied it" }).click()
  await expect(page.getByText(token)).toHaveCount(0)
  await expect(page).not.toHaveURL(/token=/)
  await expect(page.getByText(/hpk_[a-z2-7]{8}_/).first()).toBeVisible()

  // Rotate: replace the token, then revoke the old one from the same dialog.
  const first = token.split('_')[1]
  await page.getByRole('button', { name: /^Actions for / }).first().click()
  await page.getByRole('menuitem', { name: /Replace with a new token/ }).click()
  await page.getByRole('dialog', { name: /^Replace / }).getByRole('button', { name: 'Create token' }).click()
  reveal = page.getByRole('dialog', { name: 'Access token created' })
  const second = (await reveal.getByTestId('secret-value').textContent()) ?? ''
  expect(second).not.toBe(token)
  await reveal.getByRole('button', { name: 'Revoke the old token now' }).click()
  await expect(reveal.getByText('Old token revoked')).toBeVisible()
  await reveal.getByRole('button', { name: "I've copied it" }).click()
  await expect(page.getByText(`hpk_${first}_`)).toHaveCount(0)

  // The plans list shows the published plan.
  await page.goto('/routing/plans')
  await expect(page.getByRole('row', { name: new RegExp(`plan-${suffix}`) }).first()).toContainText('Live v1')
})
