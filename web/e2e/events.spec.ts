import { allowConsoleError, choose, expect, signIn, test } from './fixtures'

// The demo seeds no destinations, channels or rules. Every target here is
// a closed local port, so nothing leaves the machine and sends fail fast.

test('event catalog and event log', async ({ page }) => {
  await signIn(page)
  await page.goto('/events/catalog')
  await expect(page.getByRole('heading', { name: 'Settings', level: 1 })).toBeVisible()
  const kinds = page.getByRole('list', { name: 'Event kinds' })
  const exhausted = kinds.getByRole('listitem').filter({ hasText: 'quota.window.exhausted' })
  await expect(exhausted).toContainText('A monitored global quota window reached its exhaustion threshold')
  await expect(exhausted).toContainText('Recovers with quota.window.recovered')
  await page.getByLabel('Filter event kinds').fill('breaker')
  await expect(kinds.getByRole('listitem').filter({ hasText: 'compat.breaker.opened' })).toBeVisible()
  await expect(kinds.getByRole('listitem').filter({ hasText: 'quota.window.exhausted' })).toHaveCount(0)

  // The stream and the catalog are sub-pages of one section.
  await page.getByRole('navigation', { name: 'Event pages' }).getByRole('link', { name: 'Stream' }).click()
  await expect(page).toHaveURL(/\/events$/)
  await expect(page.getByRole('heading', { name: 'Event stream' })).toBeVisible()
  const log = page.getByRole('region', { name: 'Event log' }).or(page.getByText('No problems recorded'))
  await expect(log).toBeVisible()
  // All events, including configuration changes (collapsed in groups).
  await page.locator('label').filter({ hasText: /^All events$/ }).click()
  const stream = page.getByRole('region', { name: 'Event log' })
  await stream.getByRole('button', { name: /configuration changes/ }).first().click()
  // The whole row opens the details.
  await stream.getByRole('button', { name: /show details$/ }).first().click()
  await expect(stream.getByLabel('Event envelope')).toContainText('"schema_version"')
  // Problems hides informational events.
  await page.locator('label').filter({ hasText: /^Problems$/ }).click()
  await expect(page.locator('li[data-severity="info"]')).toHaveCount(0)
})

test('webhook relay with retry controls', async ({ page }) => {
  await signIn(page)
  const name = `hook-${Date.now().toString(36)}`

  await page.goto('/events/destinations')
  await page.getByRole('button', { name: /^Add (event )?relay$/ }).first().click()
  const d = page.getByRole('dialog')
  // Problems show on the first save, next to their fields.
  await d.getByRole('button', { name: 'Add relay' }).click()
  await expect(d.getByText('Enter a short name.')).toBeVisible()
  await expect(d.getByText('Enter the URL that receives the events.')).toBeVisible()
  await d.getByLabel('Short name').fill(name)
  await d.getByLabel('Webhook URL').fill('http://127.0.0.1:9/')
  await expect(d.getByText(/Plain http works only with “Receiver is on a private network”/)).toBeVisible()
  await d.getByLabel('Receiver is on a private network').check()
  // Events come from the catalog; no hand-typed patterns needed.
  await d.getByRole('combobox', { name: 'Events' }).click()
  await page.getByRole('option', { name: 'Any quota event' }).click()
  await page.keyboard.press('Escape')
  await expect(d.getByText(/Matches 3 of \d+ event kinds/)).toBeVisible()
  await d.getByRole('button', { name: 'Retries and headers' }).click()
  await d.getByLabel('Extra headers').fill('Authorization=Bearer nope')
  await expect(d.getByText(/Remove Authorization/)).toBeVisible()
  await d.getByLabel('Extra headers').fill('X-Team=platform')
  await d.getByLabel('Signing secret', { exact: true }).fill('whsec-e2e-synthetic')
  await d.getByRole('button', { name: 'Add relay' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)

  await page.getByRole('row', { name: new RegExp(name) }).click()
  await expect(page).toHaveURL(/\/events\/destinations\/dst_/)
  await expect(page.getByRole('heading', { name, level: 1 })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Event relays' })).toBeVisible()
  await expect(page.getByText('http://127.0.0.1:9/')).toBeVisible()
  await expect(page.getByText('Signed', { exact: true })).toBeVisible()
  await expect(page.getByText('Retrying', { exact: true })).toBeVisible()

  // Re-sending asks for a window and reports what was queued.
  await page.getByRole('button', { name: 'More relay actions' }).click()
  await page.getByRole('menuitem', { name: /Re-send events since/ }).click()
  const resend = page.getByRole('dialog', { name: 'Re-send events' })
  await expect(resend).toContainText('Receivers may see duplicates')
  await resend.getByRole('button', { name: 'Re-send' }).click()
  await expect(page.getByRole('status').filter({ hasText: /Nothing to retry|Queued/ })).toBeVisible()

  // The stored secret is write-only.
  await page.getByRole('button', { name: 'Edit' }).click()
  await expect(page.getByRole('dialog').getByText(/Stored \(fingerprint/).first()).toBeVisible()
  await expect(page.getByRole('dialog').getByLabel('Signing secret', { exact: true })).toHaveValue('')
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
  // Pick the type first; the form then asks only for what Slack needs.
  await d.getByRole('listitem', { name: 'Slack' }).click()
  await d.getByLabel('Short name').fill(channel)
  await d.getByLabel('Incoming webhook URL', { exact: true }).fill('http://127.0.0.1:9/services/T000/B000/synthetic')
  await d.getByRole('button', { name: 'Add channel' }).click()
  await expect(d.getByText(/A Slack webhook URL starts with https:\/\/hooks.slack.com\/services\//)).toBeVisible()
  await d.getByRole('button', { name: /^Delivery/ }).click()
  await d.getByLabel('Webhook is on a private network').check()
  await d.getByRole('switch', { name: /^Quiet hours/ }).check()
  await d.getByLabel('Quiet from').fill('22:00')
  await d.getByLabel('Until').fill('07:00')
  await d.getByRole('combobox', { name: 'Time zone' }).fill('Vilnius')
  await page.getByRole('option', { name: 'Europe/Vilnius' }).click()
  await d.getByRole('button', { name: 'Add channel' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  const row = page.getByRole('row').filter({ hasText: channel })
  await expect(row).toContainText('Quiet 22:00–07:00 Europe/Vilnius')

  // A test send to a closed port reports the failure inline.
  await row.getByRole('button', { name: `Send test to ${channel}` }).click()
  await expect(row.getByRole('status')).toContainText('Not delivered')

  // Rules are built from catalog events, refuse loops and need a channel.
  await page.getByRole('link', { name: 'Rules' }).click()
  await page.getByRole('button', { name: 'Add rule' }).first().click()
  d = page.getByRole('dialog')
  await d.getByLabel('Short name').fill(`quota-${suffix}`)
  await d.getByRole('button', { name: 'Quota used up' }).click()
  await expect(d.getByText(/Matches 2 of \d+ event kinds/)).toBeVisible()
  await d.getByRole('button', { name: 'Add rule' }).click()
  await expect(d.getByText('Choose at least one channel.')).toBeVisible()
  await d.getByRole('group', { name: 'Channels' }).getByLabel(new RegExp(channel)).check()
  await d.getByRole('button', { name: 'Custom message and grouping' }).click()
  await expect(d.getByLabel('Template variables')).toContainText('{{.Data.reason}}')
  allowConsoleError(page, /422/)
  await d.getByRole('textbox', { name: 'Message' }).fill('{{.Data.label')
  await d.getByRole('button', { name: 'Add rule' }).click()
  await expect(d.getByText(/invalid template/i).first()).toBeVisible()
  await d.getByRole('textbox', { name: 'Message' }).fill('{{.Data.label | upper}} is {{.Severity}}')
  await d.getByRole('button', { name: 'Add rule' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.getByRole('row').filter({ hasText: `quota-${suffix}` })).toContainText(channel)

  // The failed test is in the history. Tests are one-shot: no retry is
  // offered (send a new test instead).
  await page.getByRole('link', { name: 'History' }).click()
  await choose(page, 'Status', 'dead')
  const failed = page.getByRole('row').filter({ hasText: 'Test message' }).filter({ hasText: channel })
  await expect(failed).toContainText('Failed')
  await expect(failed.getByRole('button', { name: /Retry notification/ })).toHaveCount(0)
})
