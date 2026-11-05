import { expect, signIn, test } from './fixtures'

// Every create/edit dialog keeps its actions pinned: the primary button is
// visible without scrolling, on a laptop screen and on a phone.

const DIALOGS: { page: string; open: RegExp }[] = [
  { page: '/clients', open: /^Add client$/ },
  { page: '/providers', open: /^Connect a provider$/ },
  { page: '/quotas', open: /^Add budget$/ },
  { page: '/notifications', open: /^Add channel$/ },
  { page: '/notifications/rules', open: /^Add rule$/ },
  { page: '/events/destinations', open: /^Add (event )?relay$/ },
  { page: '/settings/users', open: /^Invite/ },
  { page: '/settings/auth', open: /^Add provider$/ },
  { page: '/settings/system', open: /^Take backup$/ },
]

for (const size of [
  { name: 'desktop', width: 1440, height: 900 },
  { name: 'phone', width: 390, height: 844 },
]) {
  test.describe(`dialog actions stay visible (${size.name})`, () => {
    test.use({ viewport: { width: size.width, height: size.height } })
    for (const d of DIALOGS) {
      test(`${d.page} → ${d.open.source}`, async ({ page }) => {
        await signIn(page)
        await page.goto(d.page)
        await page.getByRole('button', { name: d.open }).first().click()
        const dialog = page.getByRole('dialog').last()
        await expect(dialog).toBeVisible()
        const actions = dialog.locator('[class*="actions"]').last()
        await expect(actions).toBeInViewport({ ratio: 1 })
        await expect(actions.getByRole('button').last()).toBeInViewport({ ratio: 1 })
      })
    }
  })
}
