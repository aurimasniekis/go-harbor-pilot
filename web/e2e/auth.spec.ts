import { expect, signIn, test } from './fixtures'

test('wrong password is rejected without revealing account existence', async ({ page }) => {
  await page.goto('/login')
  await page.getByLabel('Email').fill('nobody@example.test')
  await page.getByLabel('Password', { exact: true }).fill('not-the-password')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('alert')).toContainText('invalid email or password')
})

test('invite a user and show the invitation link once', async ({ page }) => {
  await signIn(page)
  await page.goto('/settings/users')
  await page.getByRole('button', { name: 'Invite user' }).click()
  const email = `e2e-${Date.now()}@example.test`
  await page.getByLabel('Email').fill(email)
  await page.getByRole('button', { name: 'Create invitation' }).click()
  await expect(page.getByTestId('secret-value')).toContainText('/accept?purpose=invite&token=')
  await page.getByRole('button', { name: 'Close dialog' }).click()
  await expect(page.getByText(email)).toBeVisible()
})

test('issue a service principal token and never show it again', async ({ page }) => {
  await signIn(page)
  await page.goto('/settings/service-principals')
  await page.getByRole('button', { name: 'Tokens' }).first().click()
  await page.getByLabel('Token name').fill('e2e')
  await page.getByRole('button', { name: 'Issue token' }).click()
  const secret = await page.getByTestId('secret-value').textContent()
  expect(secret).toMatch(/^hpa_[a-z2-7]{8}_[A-Za-z0-9_-]{43}$/)
  await page.getByRole('button', { name: 'Close dialog' }).click()
  await page.getByRole('button', { name: 'Tokens' }).first().click()
  await expect(page.getByTestId('secret-value')).toHaveCount(0)
  await expect(page.getByText(secret ?? 'x')).toHaveCount(0)
})

test('sign out ends the session', async ({ page }) => {
  await signIn(page)
  await page.getByRole('button', { name: 'Account menu' }).click()
  await page.getByRole('menuitem', { name: 'Sign out' }).click()
  await page.goto('/settings/users')
  await expect(page).toHaveURL(/\/login/)
})
