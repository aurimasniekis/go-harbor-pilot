import { ADMIN_PAGES, expect, signIn, test } from './fixtures'

test('no horizontal page scroll on a phone', async ({ page }) => {
  await signIn(page)
  for (const path of ADMIN_PAGES) {
    await page.goto(path)
    await page.waitForLoadState('networkidle')
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow, path).toBeLessThanOrEqual(1)
  }
})

test('request page fits a phone screen', async ({ page }) => {
  await signIn(page)
  const res = await page.request.get('/api/v1/activity/requests?limit=20')
  const items = ((await res.json()) as { items: { id: string; attempts: number }[] | null }).items ?? []
  const target = items.find((r) => r.attempts > 1) ?? items[0]
  test.skip(!target, 'no demo traffic yet')
  await page.goto(`/activity/${target!.id}`)
  await expect(page.getByRole('heading', { name: 'Route', level: 2 })).toBeVisible()
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow).toBeLessThanOrEqual(1)
})

test('detail pages fit a phone screen', async ({ page }) => {
  await signIn(page)
  const first = async (url: string) => {
    const r = await page.request.get(url)
    if (!r.ok()) return undefined
    return ((await r.json()) as { items: { id: string }[] | null }).items?.[0]?.id
  }
  const details: [string, string | undefined][] = [
    ['/clients/', await first('/api/v1/client-bindings')],
    ['/routing/plans/', await first('/api/v1/routing-plans')],
    ['/events/destinations/', await first('/api/v1/event-destinations')],
    ['/compatibility/work-items/', await first('/api/v1/protocol-watch/work-items')],
    ['/traces/', await first('/api/v1/traces?limit=1')],
  ]
  let checked = 0
  for (const [prefix, id] of details) {
    if (!id) continue
    await page.goto(prefix + id)
    await page.waitForLoadState('networkidle')
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow, prefix + id).toBeLessThanOrEqual(1)
    checked++
  }
  expect(checked, 'the demo has clients and plans').toBeGreaterThanOrEqual(2)
})
