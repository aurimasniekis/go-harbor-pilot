import { allowConsoleError, choose, expect, signIn, test } from './fixtures'

// The demo seeds no destinations, channels or rules. Every target here is
// a closed local port, so nothing leaves the machine and sends fail fast.

test('event catalog and event log', async ({ page }) => {
  await signIn(page)
  await page.goto('/events/catalog')
  await expect(page.getByRole('heading', { name: 'Settings', level: 1 })).toBeVisible()
  const kinds = page.getByRole('list', { name: 'Event kinds' })
  const exhausted = kinds.getByRole('listitem').filter({ hasText: 'quota.window.exhausted' })
  await expect(exhausted).toContainText('A monitored global quota window reached its exhaustion threshold.')
  await expect(exhausted).toContainText('Resolved by quota.window.recovered')
  await page.getByLabel('Filter event kinds').fill('breaker')
  await expect(kinds.getByRole('listitem').filter({ hasText: 'compat.breaker.opened' })).toBeVisible()
  await expect(kinds.getByRole('listitem').filter({ hasText: 'quota.window.exhausted' })).toHaveCount(0)

  await page.goto('/events')
  const log = page.getByRole('region', { name: 'Event log' }).or(page.getByText('No problems recorded'))
  await expect(log).toBeVisible()
  // All events, including configuration changes (collapsed in groups).
  await page.locator('label').filter({ hasText: /^All$/ }).click()
  const stream = page.getByRole('region', { name: 'Event log' })
  await stream.getByRole('button', { name: /configuration changes/ }).first().click()
  await stream.getByRole('button', { name: /^Show envelope/ }).first().click()
  await expect(stream.getByLabel('Event envelope')).toContainText('"schema_version"')
  // Problems hides informational events.
  await page.locator('label').filter({ hasText: /^Problems$/ }).click()
  await expect(page.locator('li[data-severity="info"]')).toHaveCount(0)
})

test('webhook destination with replay controls', async ({ page }) => {
  await signIn(page)
  const name = `hook-${Date.now().toString(36)}`

  await page.goto('/events/destinations')
  await page.getByRole('button', { name: 'Add event relay' }).click()
  const d = page.getByRole('dialog')
  await d.getByLabel('Name', { exact: true }).fill(name)
  await d.getByLabel('Webhook URL').fill('http://127.0.0.1:9/')
  await expect(d.getByText(/Plain http needs “Allow private network”/)).toBeVisible()
  await expect(d.getByRole('button', { name: 'Save destination' })).toBeDisabled()
  await d.getByLabel(/Allow private network/).check()
  await d.getByLabel('Add event pattern').fill('quota.*')
  await d.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(d.getByRole('listitem').filter({ hasText: 'quota.*' })).toBeVisible()
  await expect(d.getByText(/Matches 3 of \d+ event kinds/)).toBeVisible()
  await d.getByLabel('Extra headers (non-secret)').fill('Authorization=Bearer nope')
  await expect(d.getByText(/Remove Authorization/)).toBeVisible()
  await d.getByLabel('Extra headers (non-secret)').fill('X-Team=platform')
  await d.getByLabel('Signing secret (write-only)', { exact: true }).fill('whsec-e2e-synthetic')
  await expect(d.getByText(/Rotation: move the old secret here/)).toBeVisible()
  await d.getByRole('button', { name: 'Save destination' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)

  await page.getByRole('row', { name: new RegExp(name) }).click()
  await expect(page).toHaveURL(/\/events\/destinations\/dst_/)
  await expect(page.getByRole('link', { name: 'Event relays' })).toBeVisible()
  await expect(page.getByText('http://127.0.0.1:9/')).toBeVisible()
  await expect(page.getByText('Signed', { exact: true })).toBeVisible()
  await expect(page.getByRole('tablist', { name: 'Delivery status' })).toBeVisible()

  // Replay asks inline and reports what was queued.
  await page.getByRole('button', { name: 'Replay dead' }).click()
  const confirm = page.getByRole('group', { name: 'Confirm replay' })
  await expect(confirm).toContainText('Queue every dead delivery')
  await confirm.getByRole('button', { name: 'Confirm replay' }).click()
  await expect(page.getByRole('status').filter({ hasText: /Nothing to replay|Queued/ })).toBeVisible()
  await page.getByRole('button', { name: 'Re-send' }).click()
  await expect(page.getByRole('group', { name: 'Confirm replay' })).toContainText('Receivers may see duplicates')
  await page.getByRole('group', { name: 'Confirm replay' }).getByRole('button', { name: 'Cancel' }).click()
  await expect(page.getByRole('group', { name: 'Confirm replay' })).toHaveCount(0)

  // The stored secret is write-only.
  await page.getByRole('button', { name: 'Edit' }).click()
  await expect(page.getByRole('dialog').getByText(/A value is stored \(fingerprint/)).toBeVisible()
  await expect(page.getByRole('dialog').getByLabel('Signing secret (write-only)', { exact: true })).toHaveValue('')
  await page.getByRole('dialog').getByRole('button', { name: 'Cancel' }).click()
})

test('slack channel, test send, rule and history', async ({ page }) => {
  await signIn(page)
  const suffix = Date.now().toString(36)
  const channel = `team-${suffix}`

  await page.goto('/notifications')
  await expect(page.getByRole('heading', { name: 'Alerts', level: 1 })).toBeVisible()
  await page.getByRole('button', { name: 'Add channel' }).click()
  let d = page.getByRole('dialog')
  await d.getByLabel('Name', { exact: true }).fill(channel)
  await choose(d, 'Type', 'slack')
  await expect(d.getByRole('button', { name: 'Save channel' })).toBeDisabled()
  await d.getByLabel('Incoming webhook URL (write-only)').fill('http://127.0.0.1:9/services/T000/B000/synthetic')
  await d.getByLabel(/Quiet hours/).check()
  await d.getByLabel('Quiet from').fill('22:00')
  await d.getByLabel('Quiet until').fill('07:00')
  await d.getByLabel('Timezone (IANA)').fill('Europe/Vilnius')
  await d.getByText('Advanced').click()
  await d.getByLabel('Allow private network').check()
  await d.getByRole('button', { name: 'Save channel' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  const row = page.getByRole('row').filter({ hasText: channel })
  await expect(row).toContainText('Quiet 22:00–07:00 Europe/Vilnius')

  // A test send to a closed port reports the failure inline.
  await row.getByRole('button', { name: `Send test to ${channel}` }).click()
  await expect(row.getByRole('status')).toContainText('Not delivered')

  // Rules refuse loops and need a channel.
  await page.getByRole('link', { name: 'Rules' }).click()
  await page.getByRole('button', { name: 'Add rule' }).click()
  d = page.getByRole('dialog')
  await d.getByLabel('Name', { exact: true }).fill(`quota-${suffix}`)
  await d.getByLabel('Add event pattern').fill('notification.*')
  await d.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(d.getByText(/cannot trigger notifications/)).toBeVisible()
  await d.getByRole('button', { name: 'Remove notification.*' }).click()
  await d.getByLabel('Add event pattern').fill('quota.*')
  await d.getByLabel('Add event pattern').press('Enter')
  await expect(d.getByRole('button', { name: 'Save rule' })).toBeDisabled()
  await d.getByRole('group', { name: 'Channels' }).getByLabel(new RegExp(channel)).check()
  await expect(d.getByRole('list', { name: 'Template variables' }).or(d.getByLabel('Template variables'))).toContainText('{{.Data.reason}}')
  allowConsoleError(page, /422/)
  await d.getByLabel('Body template').fill('{{.Data.label')
  await d.getByRole('button', { name: 'Save rule' }).click()
  await expect(d.getByText(/^invalid template/)).toBeVisible()
  await d.getByLabel('Body template').fill('{{.Data.label | upper}} is {{.Severity}}')
  await d.getByRole('button', { name: 'Save rule' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.getByRole('row').filter({ hasText: `quota-${suffix}` })).toContainText(channel)

  // The failed test is in the history. Tests are one-shot: no replay is
  // offered (send a new test instead).
  await page.getByRole('link', { name: 'History' }).click()
  await choose(page, 'Status', 'dead')
  const failed = page.getByRole('row').filter({ hasText: 'Harbor Pilot test notification' }).filter({ hasText: channel })
  await expect(failed).toContainText('Failed')
  await expect(failed.getByRole('button', { name: /Replay notification/ })).toHaveCount(0)
})
