import { expect, signIn, test } from './fixtures'

// The demo seeds quota monitors backed by the synthetic quota simulator
// (with backfilled history), two budgets and synthetic gateway traffic.

test('quota board shows monitor states, routing health and a forecast', async ({ page }) => {
  await signIn(page)
  await page.goto('/quotas')
  await expect(page.getByRole('heading', { name: 'Quotas', level: 1 })).toBeVisible()
  const weekly = page.getByRole('article', { name: 'weekly-exhausted' })
  await expect(weekly).toBeVisible()
  await expect(weekly.getByText('Exhausted').first()).toBeVisible()
  await expect(page.getByRole('article', { name: 'dev-balance' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Account routing health' })).toContainText('anthropic-team-1')

  const team = page.getByRole('article', { name: 'team-1-subscription' })
  await team.getByRole('button', { name: /History and forecast for/ }).first().click()
  const d = page.getByRole('dialog')
  await expect(d.getByRole('heading', { name: /Forecast/ })).toBeVisible()
  await expect(d.getByText('Estimate', { exact: true })).toBeVisible()
  await expect(d.getByRole('img', { name: /Remaining .* over time/ })).toBeVisible()
  await d.getByRole('tab', { name: '24 hours' }).click()
  await d.getByRole('button', { name: 'Close dialog' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)

  const text = await page.locator('main').innerText()
  expect(text).not.toContain('NaN')
  expect(text).not.toContain('undefined')
})

test('quota monitors: unofficial adapters need acknowledgement; sign-in starts', async ({ page }) => {
  page.on('dialog', (dl) => void dl.accept())
  await signIn(page)
  await page.goto('/quotas/monitors')
  await page.getByRole('button', { name: 'Add monitor' }).click()
  const d = page.getByRole('dialog')
  const name = `sub-${Date.now().toString(36)}`
  await d.getByLabel('Name', { exact: true }).fill(name)
  await d.getByLabel('Adapter', { exact: true }).selectOption('claude_subscription')
  await expect(d.getByText('Unofficial adapter.')).toBeVisible()
  const opts = d.getByLabel('Adapter options (JSON object)')
  await opts.fill('{bad')
  await expect(d.getByText(/Invalid JSON/)).toBeVisible()
  await opts.fill('{"token_source": "login"}')
  const save = d.getByRole('button', { name: 'Save monitor' })
  await expect(save).toBeDisabled()
  await d.getByLabel('I understand and accept these risks for this monitor').check()
  await save.click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.getByRole('cell', { name, exact: true }).first()).toBeVisible()

  const signin = page.getByRole('region', { name: 'In-app sign-in' })
  await expect(signin).toContainText(name)
  await signin.getByRole('row', { name: new RegExp(name) }).getByRole('button', { name: 'Sign in' }).click()
  const sd = page.getByRole('dialog')
  await expect(sd.getByRole('link', { name: 'Open the provider sign-in page' })).toHaveAttribute('href', /code_challenge_method=S256/)
  await expect(sd.getByRole('button', { name: 'Complete sign-in' })).toBeDisabled()
  await sd.getByRole('button', { name: 'Cancel' }).click()

  await page.getByRole('button', { name: `Delete ${name}` }).click()
  await expect(page.getByRole('cell', { name, exact: true })).toHaveCount(0)
})

test('usage keeps cost categories apart and budgets show status', async ({ page }) => {
  page.on('dialog', (dl) => void dl.accept())
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

  await page.goto('/usage/budgets')
  await expect(page.getByRole('article', { name: 'acme-monthly' })).toBeVisible()
  await expect(page.getByRole('article', { name: 'ci-daily' })).toContainText('Blocks at limit')
  await expect(page.getByRole('meter', { name: 'ci-daily spend' })).toBeVisible()

  const name = `proj-${Date.now().toString(36)}`
  await page.getByRole('button', { name: 'Add budget' }).click()
  const d = page.getByRole('dialog')
  await d.getByLabel('Name', { exact: true }).fill(name)
  await d.getByLabel('Scope').selectOption('project')
  await d.getByLabel('Project', { exact: true }).selectOption({ label: 'web-app' })
  await d.getByLabel('Limit', { exact: true }).fill('25')
  await d.getByLabel('Enforcement').selectOption('warn')
  await d.getByLabel('Subscription list-price equivalent').check()
  await d.getByRole('button', { name: 'Save budget' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.getByRole('article', { name })).toContainText('Warn only')

  await page.getByRole('button', { name: `Edit ${name}` }).click()
  await page.getByRole('dialog').getByLabel('Limit', { exact: true }).fill('30')
  await page.getByRole('dialog').getByRole('button', { name: 'Save budget' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.getByRole('cell', { name: /30 USD \/ month/ })).toBeVisible()
  await page.getByRole('button', { name: `Delete ${name}` }).click()
  await expect(page.getByRole('article', { name })).toHaveCount(0)

  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Closest to a limit' })).toBeVisible()
})
