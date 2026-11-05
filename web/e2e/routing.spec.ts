import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { expect, signIn, test } from './fixtures'

// Routing engine UI against the synthetic demo: the simulator on the seeded
// plan, and the rule / decision-table editor on a plan created here (drafts
// never affect demo traffic).

async function axeSerious(page: Page): Promise<string[]> {
  const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
  return results.violations
    .filter((v) => v.impact === 'serious' || v.impact === 'critical')
    .map((v) => `${v.id}: ${v.help} [${v.nodes.map((n) => n.target.join(' ')).join(', ')}]`)
}

test('simulator explains a synthetic request without contacting upstreams', async ({ page }) => {
  await signIn(page)
  await page.goto('/routing/plans')
  await page.getByRole('link', { name: 'team-default' }).click()

  // The decision graph shows the published version's routes.
  const graph = page.getByRole('region', { name: 'Decision graph' })
  await expect(graph.getByRole('list', { name: 'Routes' })).toBeVisible()
  await expect(graph.getByText('claude-*')).toBeVisible()
  await expect(graph.getByText('paid fallback allowed')).toBeVisible()

  const sim = page.getByRole('region', { name: 'Simulator' })
  await sim.getByLabel('Requested model').fill('claude-sonnet-4')
  await sim.getByLabel('Account health', { exact: true }).selectOption('assume_available')
  await sim.getByRole('button', { name: 'Run simulation' }).click()
  const result = sim.getByRole('region', { name: 'Simulation result' })
  await expect(result.getByText('Simulation — no upstream was contacted.')).toBeVisible()
  const cands = result.getByRole('table').first()
  const before = await cands.locator('tbody tr').count()
  expect(before).toBeGreaterThan(0)

  // Mark the first reachable account unavailable: it moves to the exclusions.
  await sim.getByText(/Account health overrides/).click()
  const firstHealth = sim.getByLabel(/^Health for /).first()
  const label = (await firstHealth.getAttribute('aria-label')) ?? ''
  await firstHealth.selectOption('unavailable')
  await sim.getByRole('button', { name: 'Run simulation' }).click()
  await expect(result.getByRole('region', { name: 'Simulated exclusions' }).getByText('simulated unavailable')).toBeVisible()
  expect(label).toMatch(/^Health for /)

  expect(await axeSerious(page)).toEqual([])
})

test('rule builder and decision table are saved, validated and simulated', async ({ page }) => {
  test.setTimeout(90_000)
  page.on('dialog', (d) => void d.accept())
  await signIn(page)
  const suffix = Date.now().toString(36)

  await page.goto('/routing/plans')
  await page.getByRole('button', { name: 'Add plan' }).click()
  let d = page.getByRole('dialog')
  await d.getByLabel('Name', { exact: true }).fill(`rules-${suffix}`)
  await d.getByRole('button', { name: 'Save plan' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.getByRole('link', { name: `rules-${suffix}` }).click()

  await page.getByRole('button', { name: 'New draft' }).click()
  d = page.getByRole('dialog')
  await d.getByLabel('Default alias').selectOption({ label: 'coding/default' })
  await d.getByRole('tab', { name: /Rules and tables/ }).click()

  // A rule with an invalid expression shows the compiler error inline.
  await d.getByRole('button', { name: 'Add rule' }).click()
  const rule = d.getByRole('listitem', { name: 'Rule rule-1' })
  await rule.getByLabel('Name').fill('no-codex')
  const ruleCard = d.getByRole('listitem', { name: 'Rule no-codex' })
  const when = ruleCard.getByLabel('When')
  await when.fill('request.nope == 1')
  await expect(ruleCard.getByText(/has no field nope/)).toBeVisible()
  // Request-stage rules cannot reference candidates (checked while typing).
  await when.fill('candidate.native')
  await expect(ruleCard.getByText('Request-stage expressions cannot reference candidate.*')).toBeVisible()
  // Variable reference inserts at the cursor.
  await when.fill('')
  await ruleCard.getByRole('button', { name: 'Variables' }).click()
  await ruleCard.getByPlaceholder('Search variables').fill('harness')
  await ruleCard.getByRole('button', { name: /^request\.harness\b/ }).first().click()
  await expect(when).toHaveValue('request.harness')
  await when.fill("request.harness == 'codex'")
  await expect(ruleCard.getByText('Valid', { exact: true })).toBeVisible()
  await ruleCard.getByLabel('Reason').fill('codex is not allowed on this plan')

  // A candidate-stage decision table.
  await d.getByRole('button', { name: 'Add decision table' }).click()
  const table = d.getByRole('listitem', { name: /^Table table-/ })
  await table.getByLabel('Stage').selectOption('candidate')
  await table.getByLabel('Input 1 expression').fill('candidate.account.billing')
  await table.getByLabel('Row 1 condition 1').fill("value == 'metered_api'")
  await table.getByLabel('Action').selectOption('boost')
  await table.getByLabel('Score change').fill('-5')
  await expect(table.getByText('OK').first()).toBeVisible()
  await d.getByLabel('Score expression (optional)').fill('candidate.native ? 1.0 : 0.0')
  await expect(d.getByText('Valid', { exact: true }).last()).toBeVisible()

  expect(await axeSerious(page)).toEqual([])

  await d.getByRole('button', { name: 'Save draft' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.getByRole('button', { name: 'Validate' }).first().click()
  await expect(page.getByText('Valid.')).toBeVisible()

  // The saved draft round-trips through the editor.
  await page.getByRole('button', { name: 'Edit' }).click()
  d = page.getByRole('dialog')
  await d.getByRole('tab', { name: /Rules and tables/ }).click()
  await expect(d.getByRole('listitem', { name: 'Rule no-codex' }).getByLabel('When')).toHaveValue("request.harness == 'codex'")
  await expect(d.getByRole('listitem', { name: /^Table table-/ }).getByLabel('Score change')).toHaveValue('-5')
  await d.getByRole('button', { name: 'Cancel' }).click()

  // The decision graph counts the rules; the simulator evaluates the draft.
  const graph = page.getByRole('region', { name: 'Decision graph' })
  await expect(graph.getByText('1 request rule')).toBeVisible()
  await expect(graph.getByText('1 candidate rule')).toBeVisible()
  const sim = page.getByRole('region', { name: 'Simulator' })
  await expect(sim.getByLabel('Plan version')).toHaveValue(/.+/)
  await sim.getByLabel('Requested model').fill('anything')
  await sim.getByLabel('Harness').selectOption('codex')
  await sim.getByRole('button', { name: 'Run simulation' }).click()
  const result = sim.getByRole('region', { name: 'Simulation result' })
  await expect(result.getByText('Simulation — no upstream was contacted.')).toBeVisible()
  await expect(result.getByText(/would be refused/)).toBeVisible()
  await expect(result.getByText(/codex is not allowed on this plan/).first()).toBeVisible()
  await expect(result.getByRole('region', { name: 'Simulated rule evaluations' }).getByText('no-codex')).toBeVisible()

  await sim.getByLabel('Harness').selectOption('claude_code')
  await sim.getByRole('button', { name: 'Run simulation' }).click()
  await expect(result.getByText(/^First choice:/)).toBeVisible()
})

test('replay re-evaluates a recorded request', async ({ page }) => {
  await signIn(page)
  let id: string | undefined
  const deadline = Date.now() + 15_000
  while (!id && Date.now() < deadline) {
    const res = await page.request.get('/api/v1/activity/requests?limit=50&status=ok')
    const body = (await res.json()) as { items: { id: string }[] | null }
    id = body.items?.[0]?.id
    if (!id) await page.waitForTimeout(500)
  }
  expect(id, 'demo traffic produced a successful request').toBeDefined()
  await page.goto(`/activity/${id}`)
  await page.getByRole('button', { name: 'Replay with current config' }).click()
  const panel = page.getByRole('region', { name: 'Replay' })
  await panel.getByRole('button', { name: 'Replay', exact: true }).click()
  await expect(panel.getByRole('status')).toContainText(/Same routing today|Routing would change|Would now be refused/)
  await expect(panel.getByText(/^Then: /)).toBeVisible()
})
