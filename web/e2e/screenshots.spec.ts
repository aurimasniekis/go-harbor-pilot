import { signIn, test } from './fixtures'

// Documentation screenshots (frozen demo clock). Run with HP_SCREENSHOTS=1.
test.skip(!process.env.HP_SCREENSHOTS, 'set HP_SCREENSHOTS=1 to capture docs screenshots')

for (const [name, path] of [
  ['overview', '/'],
  ['users', '/settings/users'],
  ['authentication', '/settings/auth'],
  ['audit', '/settings/audit'],
] as const) {
  test(`screenshot ${name}`, async ({ page }) => {
    await signIn(page)
    await page.goto(path)
    await page.waitForLoadState('networkidle')
    await page.screenshot({ path: `../docs/screenshots/${name}.png`, fullPage: true })
  })
}
