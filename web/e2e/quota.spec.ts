import { choose, deleteRow, expect, rowAction, signIn, test } from './fixtures'

// The demo seeds quota monitors backed by the synthetic quota simulator
// (with backfilled history), two budgets and synthetic gateway traffic.

test('limits show account status, trackers and a forecast', async ({ page }) => {
  await signIn(page)
  await page.goto('/quotas')
  await expect(page.getByRole('heading', { name: 'Limits', level: 1 })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Accounts' })).toContainText('anthropic-team-1')
  // A used-up window is never reported as healthy.
  const weekly = page.getByRole('region', { name: 'Not linked to an account' }).getByRole('row', { name: /weekly-exhausted/ })
  await expect(weekly).toContainText('Limit reached')
  await expect(page.getByRole('region', { name: 'Not linked to an account' }).getByRole('row', { name: /expired-credential/ })).toContainText('Sign-in failed')

  await page.getByRole('region', { name: 'Accounts' }).getByRole('row', { name: /anthropic-team-1/ }).click()
  const drawer = page.getByRole('dialog', { name: /anthropic-team-1/ })
  await drawer.getByRole('button', { name: /History and forecast for/ }).first().click()
  const d = page.getByRole('dialog').filter({ has: page.getByRole('heading', { name: /Forecast/ }) })
  await expect(d.getByText('Estimate', { exact: true })).toBeVisible()
  await expect(d.getByRole('img', { name: /Remaining .* over time/ })).toBeVisible()
  await d.getByRole('tab', { name: '24 hours' }).click()
  await d.getByRole('button', { name: 'Close dialog' }).click()
  await expect(d).toHaveCount(0)

  const text = await page.locator('main').innerText()
  expect(text).not.toContain('NaN')
  expect(text).not.toContain('undefined')
})

test('quota trackers: unofficial adapters need acknowledgement; sign-in starts', async ({ page }) => {
  await signIn(page)
  await page.goto('/quotas/monitors')
  await page.getByRole('button', { name: 'Add quota tracker' }).click()
  const d = page.getByRole('dialog')
  const name = `sub-${Date.now().toString(36)}`
  await d.getByLabel('Name', { exact: true }).fill(name)
  await choose(d, 'Adapter', 'claude_subscription', { exact: true })
  await expect(d.getByText('Unofficial adapter.')).toBeVisible()
  await d.getByRole('button', { name: 'Advanced' }).click()
  const opts = d.getByLabel('Adapter options (JSON object)')
  await opts.fill('{bad')
  await expect(d.getByText(/Invalid JSON/)).toBeVisible()
  await opts.fill('{"token_source": "login"}')
  const save = d.getByRole('button', { name: 'Start tracking' })
  await expect(save).toBeDisabled()
  await d.getByLabel('I understand and accept these risks for this monitor').check()
  await save.click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.getByRole('region', { name: 'Quota tracking list' }).getByRole('row', { name: new RegExp(name) })).toBeVisible()

  const signin = page.getByRole('region', { name: 'In-app sign-in' })
  await expect(signin).toContainText(name)
  await signin.getByRole('row', { name: new RegExp(name) }).getByRole('button', { name: 'Sign in' }).click()
  const sd = page.getByRole('dialog')
  await expect(sd.getByRole('link', { name: 'Open the provider sign-in page' })).toHaveAttribute('href', /code_challenge_method=S256/)
  await expect(sd.getByRole('button', { name: 'Complete sign-in' })).toBeDisabled()
  await sd.getByRole('button', { name: 'Cancel' }).click()

  await deleteRow(page, name)
  await expect(page.getByRole('region', { name: 'Quota tracking list' }).getByRole('row', { name: new RegExp(name) })).toHaveCount(0)
})

test('usage keeps cost categories apart and budgets show status', async ({ page }) => {
  await signIn(page)
  await page.goto('/usage')
  // Each cost category is its own headline figure; they are never summed.
  const totals = page.getByRole('group', { name: /^Spend for the/ })
  await expect(totals.getByText('Estimated cost')).toBeVisible()
  await expect(totals.getByText('Billed by providers')).toBeVisible()
  // The demo's traffic sits in one day, so the chart explains it has too
  // little data instead of drawing one bar.
  await expect(page.getByRole('img', { name: 'Cost per day' }).or(page.getByText(/Not enough data for a trend yet/))).toBeVisible()
  await page.locator('label').filter({ hasText: /^Model$/ }).click()
  await expect(page.getByRole('region', { name: 'Usage by group' })).toContainText('demo-')
  await page.locator('label').filter({ hasText: /^90d$/ }).click()
  await expect(page).toHaveURL(/range=90/)
  await expect(page.getByRole('heading', { name: 'Cost per week' })).toBeVisible()
  await page.getByRole('button', { name: /Provider vs gateway/ }).click()
  await expect(page.getByRole('region', { name: 'Provider vs gateway', exact: true })).toBeVisible()

  // Budgets moved to Limits; the old address still works.
  await page.goto('/usage/budgets')
  await expect(page).toHaveURL(/\/quotas/)
  const budgets = page.getByRole('region', { name: 'Budgets' })
  await expect(budgets.getByRole('row', { name: /acme-monthly/ })).toBeVisible()
  await expect(budgets.getByRole('row', { name: /ci-daily/ })).toContainText('Blocks at limit')
  await expect(page.getByRole('meter', { name: 'ci-daily spend' })).toBeVisible()

  const name = `proj-${Date.now().toString(36)}`
  await page.getByRole('button', { name: 'Add budget' }).first().click()
  const d = page.getByRole('dialog')
  await d.getByLabel('Name', { exact: true }).fill(name)
  await choose(d, 'Scope', 'project')
  await choose(d, 'Project', { label: 'web-app' }, { exact: true })
  await d.getByLabel('Limit', { exact: true }).fill('25')
  await choose(d, 'Enforcement', 'warn')
  await d.getByRole('button', { name: 'Advanced' }).click()
  await d.getByLabel('Subscription usage at list prices').check()
  await d.getByRole('button', { name: 'Save budget' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(budgets.getByRole('row', { name: new RegExp(name) })).toContainText('Warns only')

  await rowAction(page, name, 'Edit')
  await page.getByRole('dialog').getByLabel('Limit', { exact: true }).fill('30')
  await page.getByRole('dialog').getByRole('button', { name: 'Save budget' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(budgets.getByRole('row', { name: new RegExp(name) })).toContainText('$30.00')
  await deleteRow(page, name)
  await expect(budgets.getByRole('row', { name: new RegExp(name) })).toHaveCount(0)

  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Closest to a limit' })).toBeVisible()
})
