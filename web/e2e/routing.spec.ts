import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { expect, signIn, test, choose } from './fixtures'

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
  await page.getByRole('row', { name: /team-default/ }).click()
  // One line covering every lane of the live version, then the flow per model rule.
  await expect(page.getByText(/claude-\* → .* · gpt-\* → /).first()).toBeVisible()
  await expect(page.getByText('Requests for').first()).toBeVisible()
  await expect(page.getByRole('navigation', { name: 'Breadcrumb' }).getByRole('link', { name: 'Plans' })).toBeVisible()

  // The detailed decision graph shows the published version's routes.
  await page.getByRole('button', { name: 'Detailed graph' }).click()
  const graph = page.getByRole('region', { name: 'Decision graph' })
  await expect(graph.getByRole('list', { name: 'Routes' })).toBeVisible()
  await expect(graph.getByText('claude-*')).toBeVisible()
  await expect(graph.getByText('paid fallback allowed')).toBeVisible()

  await page.getByRole('tab', { name: 'Test a request' }).click()
  const sim = page.getByRole('region', { name: 'Simulator' })
  await sim.getByLabel('Ask for model').fill('claude-sonnet-4')
  await sim.getByRole('button', { name: /More conditions/ }).click()
  await choose(sim, 'Account health', 'assume_available', { exact: true })
  await sim.getByRole('button', { name: 'Test', exact: true }).click()
  const result = sim.getByRole('region', { name: 'Simulation result' })
  await expect(result.getByText(/no provider was contacted/)).toBeVisible()
  await expect(result.getByText(/^Goes to /)).toBeVisible()
  await result.getByRole('button', { name: 'Why' }).click()
  await expect(result.getByRole('region', { name: 'Simulated candidates' }).locator('tbody tr').first()).toBeVisible()

  // Mark the first reachable account unavailable: it moves to the exclusions.
  const firstHealth = sim.getByLabel(/^Health for /).first()
  const label = (await firstHealth.getAttribute('aria-label')) ?? ''
  await firstHealth.click()
  await page.getByRole('listbox').locator('[role="option"][value="unavailable"]').click()
  await sim.getByRole('button', { name: 'Test', exact: true }).click()
  await expect(result.getByRole('region', { name: 'Simulated exclusions' }).getByText('simulated unavailable')).toBeVisible()
  expect(label).toMatch(/^Health for /)

  expect(await axeSerious(page)).toEqual([])
})

test('a new decision table starts off and a match-everything block is flagged before publishing', async ({ page }) => {
  await signIn(page)
  const suffix = Date.now().toString(36)
  await page.goto('/routing/plans')
  await page.getByRole('button', { name: 'Add plan' }).click()
  let d = page.getByRole('dialog')
  await d.getByLabel('Short name').fill(`guard-${suffix}`)
  await d.getByRole('button', { name: 'Create plan' }).click()
  await page.waitForURL(/\/routing\/plans\/pln_/)
  await page.getByRole('button', { name: 'New draft' }).click()
  d = page.getByRole('dialog')
  await choose(d, 'Anything else goes to', { label: 'coding/default' })
  await d.getByRole('tab', { name: /Rules/ }).click()
  await d.getByRole('button', { name: 'Add decision table' }).click()
  const table = d.getByRole('listitem', { name: /^Table table-1/ })
  // Off and without rows: adding it changes nothing.
  await expect(table.getByRole('checkbox', { name: 'On', exact: true })).not.toBeChecked()
  await expect(table.getByText(/never applies/)).toBeVisible()
  await table.getByRole('checkbox', { name: 'On', exact: true }).check()
  await table.getByRole('button', { name: 'Add row' }).click()
  await expect(table.getByText('Blocks every request')).toBeVisible()
  await d.getByRole('button', { name: 'Save draft' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  const bar = page.getByRole('alert', { name: 'Draft' }).or(page.locator('[aria-label="Draft"]'))
  await expect(bar.getByText(/matches every request and blocks it/)).toBeVisible()
  await bar.getByRole('button', { name: 'Check for problems' }).click()
  await expect(bar.getByText(/blocks every request: all \d+ test requests/)).toBeVisible()
  await bar.getByRole('button', { name: /^Publish v/ }).click()
  const dlg = page.getByRole('dialog', { name: /Publish v/ })
  await expect(dlg.getByText('This version blocks traffic')).toBeVisible()
  await expect(dlg.getByRole('button', { name: 'Publish' })).toBeDisabled()
  await dlg.getByLabel(/I understand/).check()
  await expect(dlg.getByRole('button', { name: 'Publish' })).toBeEnabled()
  await dlg.getByRole('button', { name: 'Cancel' }).click()
})

test('rule builder and decision table are saved, validated and simulated', async ({ page }) => {
  test.setTimeout(90_000)
  await signIn(page)
  const suffix = Date.now().toString(36)

  await page.goto('/routing/plans')
  await page.getByRole('button', { name: 'Add plan' }).click()
  let d = page.getByRole('dialog')
  await d.getByLabel('Short name').fill(`rules-${suffix}`)
  await d.getByRole('button', { name: 'Create plan' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.waitForURL(/\/routing\/plans\/pln_/)

  await page.getByRole('button', { name: 'New draft' }).click()
  d = page.getByRole('dialog')
  await choose(d, 'Anything else goes to', { label: 'coding/default' })
  await d.getByRole('tab', { name: /Rules/ }).click()

  // A rule with an invalid expression shows the compiler error inline.
  await d.getByRole('button', { name: 'Add rule' }).click()
  const rule = d.getByRole('listitem', { name: 'Rule rule-1' })
  await rule.getByLabel('Name').fill('no-codex')
  const ruleCard = d.getByRole('listitem', { name: 'Rule no-codex' })
  await ruleCard.getByRole('checkbox', { name: 'On', exact: true }).check()
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
  // The insert puts the cursor after the variable; let that settle before typing.
  await expect(when).toBeFocused()
  await when.fill("request.harness == 'codex'")
  await expect(ruleCard.getByText('Valid', { exact: true })).toBeVisible()
  await ruleCard.getByLabel('Reason').fill('codex is not allowed on this plan')

  // A decision table for each account.
  await d.getByRole('button', { name: 'Add decision table' }).click()
  const table = d.getByRole('listitem', { name: /^Table table-/ })
  await table.getByRole('checkbox', { name: 'On', exact: true }).check()
  await choose(table, 'Runs', 'candidate')
  await table.getByLabel('Input 1 expression').fill('candidate.account.billing')
  await table.getByRole('button', { name: 'Add row' }).click()
  await table.getByLabel('Row 1 condition 1').fill("value == 'metered_api'")
  await choose(table, 'Action', 'boost')
  await table.getByLabel('Score change').fill('-5')
  await expect(table.getByText('OK').first()).toBeVisible()
  await d.getByLabel('Score expression (optional)').fill('candidate.native ? 1.0 : 0.0')
  await expect(d.getByText('Valid', { exact: true }).last()).toBeVisible()

  expect(await axeSerious(page)).toEqual([])

  await d.getByRole('button', { name: 'Save draft' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.getByRole('button', { name: 'Check for problems' }).first().click()
  await expect(page.getByText(/no problems found/).first()).toBeVisible()

  // The saved draft round-trips through the editor.
  await page.getByRole('button', { name: 'Edit draft' }).first().click()
  d = page.getByRole('dialog')
  await d.getByRole('tab', { name: /Rules/ }).click()
  await expect(d.getByRole('listitem', { name: 'Rule no-codex' }).getByLabel('When')).toHaveValue("request.harness == 'codex'")
  await expect(d.getByRole('listitem', { name: /^Table table-/ }).getByLabel('Score change')).toHaveValue('-5')
  await d.getByRole('button', { name: 'Cancel' }).click()

  // The decision graph counts the rules; the simulator evaluates the draft.
  await page.getByRole('tab', { name: 'Flow' }).click()
  await page.getByRole('button', { name: 'Detailed graph' }).click()
  const graph = page.getByRole('region', { name: 'Decision graph' })
  await expect(graph.getByText('1 rule before choosing an account')).toBeVisible()
  await expect(graph.getByText('1 rule for each account')).toBeVisible()
  await page.getByRole('tab', { name: 'Test a request' }).click()
  const sim = page.getByRole('region', { name: 'Simulator' })
  await sim.getByLabel('Ask for model').fill('anything')
  await sim.getByRole('button', { name: /More conditions/ }).click()
  await choose(sim, 'Tool', 'codex', { exact: true })
  await sim.getByRole('button', { name: 'Test', exact: true }).click()
  const result = sim.getByRole('region', { name: 'Simulation result' })
  await expect(result.getByText(/no provider was contacted/)).toBeVisible()
  await expect(result.getByText('Blocked by a rule')).toBeVisible()
  await expect(result.getByText(/codex is not allowed on this plan/).first()).toBeVisible()
  await expect(result.getByRole('region', { name: 'Simulated rule evaluations' }).getByText('no-codex')).toBeVisible()

  await choose(sim, 'Tool', 'claude_code', { exact: true })
  await sim.getByRole('button', { name: 'Test', exact: true }).click()
  await expect(result.getByText(/^Goes to /)).toBeVisible()
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
  await expect(panel.getByText('Served then:')).toBeVisible()
})
