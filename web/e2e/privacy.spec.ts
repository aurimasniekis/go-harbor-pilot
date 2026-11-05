import { choose, deleteRow, expect, signIn, test } from './fixtures'

// The demo seeds no privacy or trace policies; this test creates them
// through the UI and exercises the non-persisting rule sandbox.

test('privacy policy, rule sandbox and trace policy', async ({ page }) => {
  await signIn(page)
  const suffix = Date.now().toString(36)

  // Masking policy with a custom block rule and email detection.
  await page.goto('/privacy')
  await expect(page.getByRole('heading', { name: 'Privacy', level: 1 })).toBeVisible()
  await page.getByRole('button', { name: 'Add masking policy' }).click()
  let d = page.getByRole('dialog')
  const policy = `mask-${suffix}`
  await d.getByLabel('Short name').fill(policy)
  // One choice instead of two flags; anything but blocking warns.
  await expect(d.getByRole('radio', { name: 'Block the request' })).toBeChecked()
  await d.getByRole('radio', { name: 'Send anyway' }).check()
  await expect(d.getByText(/secrets can reach providers unmasked/)).toBeVisible()
  await d.getByRole('radio', { name: 'Block the request' }).check()
  await d.getByRole('checkbox', { name: 'Email addresses' }).check()
  await d.getByRole('button', { name: 'Add rule' }).click()
  await d.getByLabel('Name', { exact: true }).fill('prod_db')
  await d.getByLabel('Pattern', { exact: true }).fill('prod-db-[0-9]+')
  await choose(d, 'Action', 'block', { exact: true })
  await d.getByRole('checkbox', { name: 'Also put them back in tool-call arguments' }).check()
  await expect(d.getByText(/Restored tool arguments let the model pass your secrets/)).toBeVisible()
  await expect(d.getByRole('button', { name: 'Create policy' })).toBeDisabled()
  await d.getByRole('checkbox', { name: 'Also put them back in tool-call arguments' }).uncheck()
  // Values too short to mask are named before saving.
  await choose(d, 'Values', 'stored', { exact: true })
  await d.getByLabel('Values (one per line)').fill('q7Zx1\nsupersecretvalue123')
  await expect(d.getByText(/Line 1 is shorter than 6 characters/)).toBeVisible()
  await expect(d.getByRole('button', { name: 'Create policy' })).toBeDisabled()
  await choose(d, 'Values', 'none', { exact: true })
  // Try the unsaved policy: a credential URL keeps its host.
  await d.getByRole('button', { name: 'Try this policy on sample text' }).click()
  await d.getByRole('button', { name: 'Try', exact: true }).click()
  const tried = d.getByLabel('Masked preview')
  await expect(tried).toContainText('@git.example.test/acme/app.git')
  await expect(tried).not.toContainText('hunter2hunter2')
  await d.getByRole('button', { name: 'Create policy' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.getByRole('cell', { name: policy, exact: true })).toBeVisible()

  // Sandbox with the saved policy and a synthetic sample.
  await page.goto('/privacy/sandbox')
  await expect(page.getByText('Nothing you test here is stored or logged.')).toBeVisible()
  await choose(page, 'Policy', { label: policy }, { exact: true })
  await page.getByRole('button', { name: 'Use a synthetic sample' }).click()
  await page.getByRole('button', { name: 'Test', exact: true }).click()
  const masked = page.getByLabel('Masked preview')
  await expect(masked).toContainText('HPSEC_')
  await expect(masked).not.toContainText('sk-ant-api03-EXAMPLE')
  await expect(page.getByLabel('Sample with detections highlighted').locator('mark').first()).toBeVisible()
  await expect(page.getByRole('cell', { name: /Anthropic API keys/ }).first()).toBeVisible()

  // A JSON definition: the block rule blocks.
  await choose(page, 'Policy', { label: /written as JSON/ }, { exact: true })
  const def = page.getByLabel('Policy definition (JSON)')
  await def.fill(JSON.stringify({ required: true, rules: [{ name: 'prod_db', pattern: 'prod-db-[0-9]+', action: 'block' }] }))
  await page.getByLabel('Sample text', { exact: true }).fill('connect to prod-db-7 now')
  await page.getByRole('button', { name: 'Test', exact: true }).click()
  await expect(page.getByText('Would be blocked; nothing is sent')).toBeVisible()

  // Capture rule: unredacted needs a time box.
  await page.goto('/privacy/trace-policies')
  await page.getByRole('button', { name: 'Add capture rule' }).click()
  d = page.getByRole('dialog')
  const trace = `trace-${suffix}`
  await d.getByLabel('Short name').fill(trace)
  await choose(d, 'What to keep', 'unredacted')
  await expect(d.getByText(/Unredacted capture stores original prompts/)).toBeVisible()
  const saveTrace = d.getByRole('button', { name: 'Create capture rule' })
  await expect(saveTrace).toBeDisabled()
  await choose(d, 'What to keep', 'masked_full')
  // Sizes are kept in range on their own field, and the quota stays valid.
  await d.getByLabel('Largest capture').fill('40')
  expect(Number(await d.getByLabel('Largest capture').inputValue())).toBeLessThanOrEqual(16)
  await expect(d.getByText('At least 1 MB.')).toHaveCount(0)
  await d.getByLabel('Largest capture').fill('1')
  await choose(d, 'Mask captured content with', { label: policy })
  await d.getByRole('checkbox', { name: 'Allow downloads' }).check()
  await saveTrace.click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  const row = page.getByRole('row', { name: new RegExp(trace) })
  await expect(row).toContainText('Masked full')
  await expect(row).toContainText(policy)

  // Traces: the demo's private plan captures masked traces, listed as
  // metadata; content search explains the grant.
  await page.getByRole('link', { name: 'Captured content' }).click()
  await expect(page).toHaveURL(/\/traces/)
  await expect(page.getByRole('heading', { name: 'Privacy', level: 1 })).toBeVisible()
  await expect(page.getByText('Nothing captured yet').or(page.getByRole('table', { name: 'Captured content' })).or(page.getByLabel('Captured content')).first()).toBeVisible()
  await expect(page.getByText(/Reading content needs an explicit grant/)).toBeVisible()

  // Clean up through the list (the browser confirm is accepted above).
  await page.goto('/privacy/trace-policies')
  await deleteRow(page, trace)
  await expect(page.getByRole('cell', { name: trace, exact: true })).toHaveCount(0)
})

test('system settings list CLI bridge availability', async ({ page }) => {
  await signIn(page)
  await page.goto('/settings/system')
  const panel = page.getByRole('region', { name: 'CLI bridges' })
  await expect(panel).toContainText('Claude Code')
  await expect(panel).toContainText('Codex')
})
