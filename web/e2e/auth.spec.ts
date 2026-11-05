import { expect, signIn, test } from './fixtures'

test('wrong password is rejected without revealing account existence', async ({ page }) => {
  await page.goto('/login')
  await page.getByLabel('Email').fill('nobody@example.test')
  await page.getByLabel('Password', { exact: true }).fill('not-the-password')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('alert')).toContainText('Email or password is incorrect.')
})

test('invite a user and show the invitation link once', async ({ page }) => {
  await signIn(page)
  await page.goto('/settings/users')
  await page.getByRole('button', { name: 'Invite user' }).click()
  const email = `e2e-${Date.now()}@example.test`
  await page.getByLabel('Email').fill(email)
  await page.getByRole('button', { name: 'Create invitation' }).click()
  await expect(page.getByTestId('secret-value')).toContainText('/accept?purpose=invite&token=')
  await page.getByRole('button', { name: "I've copied it" }).click()
  const row = page.getByRole('row').filter({ hasText: email })
  await expect(row).toContainText('Invitation pending')
})

test('issue an API token, never show it again, and revoke it after confirming', async ({ page }) => {
  await signIn(page)
  await page.goto('/settings/service-principals')
  await page.getByRole('button', { name: /^API tokens of / }).first().click()
  await page.getByRole('button', { name: 'Issue token' }).click()
  await expect(page.getByText('Name the token after where it is used.')).toBeVisible()
  const name = `e2e-${Date.now().toString(36)}`
  await page.getByLabel('Token name').fill(name)
  await page.getByRole('button', { name: 'Issue token' }).click()
  const secret = await page.getByTestId('secret-value').textContent()
  expect(secret).toMatch(/^hpa_[a-z2-7]{8}_[A-Za-z0-9_-]{43}$/)
  await page.getByRole('button', { name: "I've copied it" }).click()
  await expect(page.getByTestId('secret-value')).toHaveCount(0)
  await expect(page.getByText(secret ?? 'x')).toHaveCount(0)
  // Revoking asks first.
  const tokens = page.getByRole('dialog').filter({ hasText: 'Token name' })
  await tokens.getByRole('row').filter({ hasText: name }).getByRole('button', { name: 'Revoke' }).click()
  await page.getByRole('dialog').filter({ hasText: `Revoke ${name}?` }).getByRole('button', { name: 'Revoke' }).click()
  await expect(tokens.getByRole('row').filter({ hasText: name })).toContainText(/Revoked/i)
})

test('sign out ends the session', async ({ page }) => {
  await signIn(page)
  await page.getByRole('button', { name: 'Account menu' }).click()
  await page.getByRole('menuitem', { name: 'Sign out' }).click()
  await page.goto('/settings/users')
  await expect(page).toHaveURL(/\/login/)
})
