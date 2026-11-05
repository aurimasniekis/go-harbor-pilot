import { expect, signIn, test, choose } from './fixtures'

// The demo seeds no privacy or trace policies; this test creates them
// through the UI and exercises the non-persisting rule sandbox.

test('privacy policy, rule sandbox and trace policy', async ({ page }) => {
  page.on('dialog', (dl) => void dl.accept())
  await signIn(page)
  const suffix = Date.now().toString(36)

  // Masking policy with a custom block rule and email detection.
  await page.goto('/privacy')
  await expect(page.getByRole('heading', { name: 'Privacy', level: 1 })).toBeVisible()
  await page.getByRole('button', { name: 'Add privacy policy' }).click()
  let d = page.getByRole('dialog')
  const policy = `mask-${suffix}`
  await d.getByLabel('Name', { exact: true }).fill(policy)
  await d.getByLabel('Email addresses').check()
  await d.getByRole('button', { name: 'Add rule' }).click()
  await d.getByLabel('Name', { exact: true }).nth(1).fill('prod_db')
  await d.getByLabel('Pattern', { exact: true }).fill('prod-db-[0-9]+')
  await choose(d, 'Action', 'block', { exact: true })
  await d.getByLabel('Restore originals into tool-call arguments (off by default)').check()
  await expect(d.getByText(/Restored tool arguments let the model pass your secrets/)).toBeVisible()
  await expect(d.getByRole('button', { name: 'Save policy' })).toBeDisabled()
  await d.getByLabel('Restore originals into tool-call arguments (off by default)').uncheck()
  await d.getByRole('button', { name: 'Save policy' }).click()
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
  await expect(page.getByRole('cell', { name: /Anthropic API keys/ })).toBeVisible()

  // Unsaved policy: block rule blocks.
  await choose(page, 'Policy', { label: /Unsaved policy/ }, { exact: true })
  const def = page.getByLabel('Policy definition (JSON)')
  await def.fill(JSON.stringify({ required: true, rules: [{ name: 'prod_db', pattern: 'prod-db-[0-9]+', action: 'block' }] }))
  await page.getByLabel('Sample text').fill('connect to prod-db-7 now')
  await page.getByRole('button', { name: 'Test', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Result' }).getByText('Yes')).toBeVisible()

  // Trace policy: unredacted needs a time box.
  await page.goto('/privacy/trace-policies')
  await page.getByRole('button', { name: 'Add trace policy' }).click()
  d = page.getByRole('dialog')
  const trace = `trace-${suffix}`
  await d.getByLabel('Name', { exact: true }).fill(trace)
  await choose(d, 'Content mode', 'unredacted')
  await expect(d.getByText(/Unredacted capture stores original prompts/)).toBeVisible()
  const saveTrace = d.getByRole('button', { name: 'Save trace policy' })
  await expect(saveTrace).toBeDisabled()
  await choose(d, 'Content mode', 'masked_full')
  await choose(d, 'Redaction policy', { label: policy })
  await d.getByLabel('Allow export (users with the traces.export grant can download content)').check()
  await saveTrace.click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  const row = page.getByRole('row', { name: new RegExp(trace) })
  await expect(row).toContainText('Masked full trace')
  await expect(row).toContainText(policy)

  // Traces: the demo's private plan captures masked traces, listed as
  // metadata; content search explains the grant.
  await page.goto('/traces')
  await expect(page.getByRole('heading', { name: 'Traces', level: 1 })).toBeVisible()
  await expect(page.getByText('No live traces').or(page.locator('table tbody tr').first())).toBeVisible()
  await expect(page.getByText('Searching trace content needs an explicit grant')).toBeVisible()

  // Clean up through the list (the browser confirm is accepted above).
  await page.goto('/privacy/trace-policies')
  await page.getByRole('button', { name: `Delete ${trace}` }).click()
  await expect(page.getByRole('cell', { name: trace, exact: true })).toHaveCount(0)
})

test('system settings list CLI bridge availability', async ({ page }) => {
  await signIn(page)
  await page.goto('/settings/system')
  const panel = page.getByRole('region', { name: 'CLI bridges' })
  await expect(panel).toContainText('Claude Code')
  await expect(panel).toContainText('Codex')
})
