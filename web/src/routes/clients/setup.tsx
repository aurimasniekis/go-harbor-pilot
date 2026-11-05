import { useState } from 'react'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Accordion, Anchor, Button, Code, List, SegmentedControl, Stack, Tabs, Text, Title } from '@mantine/core'
import { KeyRound } from 'lucide-react'
import { z } from 'zod'
import { SelectField, TextField } from '@/components/form'
import { metaQuery, resourceListQuery } from '@/lib/api/queries'
import type { AliasRes, BindingRes } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { IssueCredentialDialog } from '@/features/config/IssueCredentialDialog'
import { CodeBlock } from '@/features/harness/CodeBlock'
import { ManualSetup } from '@/features/harness/ManualSetup'
import { commands, guide, harnessKinds, type HarnessKind } from '@/features/harness/guides'
import { kindForTool } from '@/features/harness/tools'
import h from '@/features/harness/harness.module.css'
import { PageHeader, useCompact } from '@/ui/kit'

const kindValues = ['claude', 'codex', 'opencode', 'pi', 'sdk'] as const

export const Route = createFileRoute('/clients/setup')({
  validateSearch: z.object({ binding: z.string().optional().catch(undefined), tool: z.enum(kindValues).optional().catch(undefined) }).parse,
  component: HarnessSetup,
})

function Step({ n, title, children }: { n: number; title: string; children: React.ReactNode }) {
  return (
    <section className={h.stepBlock} aria-labelledby={`step-${n}`}>
      <span className={h.stepNum} aria-hidden="true">
        {n}
      </span>
      <Stack gap="sm" miw={0} style={{ flex: 1 }}>
        <Title order={2} id={`step-${n}`}>
          {title}
        </Title>
        {children}
      </Stack>
    </section>
  )
}

/** A URL problem, or undefined when it is an absolute http(s) URL. */
function urlProblem(u: string): string | undefined {
  try {
    const p = new URL(u)
    return p.protocol === 'http:' || p.protocol === 'https:' ? undefined : 'Use an http or https address.'
  } catch {
    return 'Enter the full address, e.g. https://harbor-pilot.example.com.'
  }
}

const shortLabel: Record<HarnessKind, string> = { claude: 'Claude Code', codex: 'Codex', opencode: 'OpenCode', pi: 'Pi', sdk: 'Other' }

function HarnessSetup() {
  const search = Route.useSearch()
  const navigate = useNavigate()
  const compact = useCompact()
  const meta = useQuery(metaQuery)
  const bindings = useQuery(resourceListQuery<BindingRes>('client-bindings')).data?.items ?? []
  const aliases = useQuery(resourceListQuery<AliasRes>('model-aliases')).data?.items ?? []
  const canTokens = useCan('tokens.manage')
  const [chosen, setChosen] = useState<string | undefined>(search.binding)
  const bindingId = chosen ?? bindings[0]?.id ?? ''
  const binding = bindings.find((b) => b.id === bindingId)
  // The tool follows the client unless the URL picks one.
  const kind: HarnessKind = search.tool ?? kindForTool(binding?.spec.expected_harness)
  const setKind = (k: HarnessKind) => void navigate({ to: '/clients/setup', search: { binding: bindingId || undefined, tool: k }, replace: true })
  const [editURL, setEditURL] = useState(false)
  const [url, setUrl] = useState<string | null>(null)
  const [model, setModel] = useState('')
  const [issuing, setIssuing] = useState(false)
  const effectiveURL = url ?? meta.data?.public_url ?? ''
  const urlErr = url !== null ? urlProblem(url) : undefined
  const input = { url: urlErr ? (meta.data?.public_url ?? '') : effectiveURL, model, bindingId }
  const g = guide(kind, input)
  const cmds = commands(kind, input)
  const label = harnessKinds.find((k) => k.value === kind)?.label ?? kind

  return (
    <Stack gap="xl" maw={920}>
      <PageHeader crumbs={[{ label: 'Clients', to: '/clients' }, { label: 'Set up a tool' }]} title="Set up a tool" context="Point a coding tool or app at Harbor Pilot with a client’s access token." />

      <Step n={1} title="Choose the client and model">
        <div className={h.form}>
          <SelectField
            label="Client"
            value={bindingId}
            onChange={(v) => {
              setChosen(v)
              void navigate({ to: '/clients/setup', search: { binding: v || undefined }, replace: true })
            }}
            required
            placeholder={bindings.length ? 'Choose a client' : 'No clients yet'}
            error={bindings.length === 0 ? 'Add a client first.' : undefined}
            options={bindings.map((b) => ({ value: b.id, label: b.spec.display_name || b.name }))}
          />
          <SelectField
            label="Model"
            value={model}
            onChange={setModel}
            options={[{ value: '', label: 'The tool’s default' }, ...aliases.map((a) => ({ value: a.name, label: a.name }))]}
          />
        </div>
        {editURL ? (
          <TextField label="Harbor Pilot address" value={effectiveURL} onChange={setUrl} placeholder="https://harbor-pilot.example.com" error={urlErr}
            hint="The address the tool’s machine uses to reach this server." mono />
        ) : (
          <Text size="sm" c="dimmed">
            Harbor Pilot address: <Code>{effectiveURL || 'not configured'}</Code>{' '}
            <Anchor component="button" type="button" size="sm" onClick={() => setEditURL(true)}>
              Change
            </Anchor>
          </Text>
        )}
      </Step>

      <Step n={2} title="Pick the tool">
        <SegmentedControl
          aria-label="Tool"
          value={kind}
          onChange={(v) => setKind(v as HarnessKind)}
          data={harnessKinds.map((k) => ({ value: k.value, label: compact ? shortLabel[k.value] : k.label }))}
          fullWidth
          orientation={compact ? 'vertical' : 'horizontal'}
        />
        <Text size="xs" c="dimmed">
          {g.verified ? 'Tested with Claude Code 2.1.318.' : `Follows ${label}’s documentation; not yet tested with a live install.`}
        </Text>
      </Step>

      <Step n={3} title={`Connect ${label}`}>
        <Tabs defaultValue="token" keepMounted={false}>
          <Tabs.List aria-label="How to connect">
            <Tabs.Tab value="token">Paste a token</Tabs.Tab>
            {kind !== 'sdk' && <Tabs.Tab value="cli">Use the harbor-pilot CLI</Tabs.Tab>}
          </Tabs.List>
          <Tabs.Panel value="token" pt="md">
            <Stack gap="md">
              <Text size="sm">
                Create an access token for this client, then run these steps on the machine with {label}. The token goes into a file only you can read,
                never into a configuration file.
              </Text>
              <div>
                <Button leftSection={<KeyRound size={14} />} disabled={!binding || !canTokens} onClick={() => setIssuing(true)}>
                  Create an access token{binding ? ` for ${binding.spec.display_name || binding.name}` : ''}
                </Button>
                {!canTokens && (
                  <Text size="xs" c="dimmed" mt={4}>
                    Ask an operator for a token; you can’t create one.
                  </Text>
                )}
              </div>
              <ManualSetup kind={kind} input={input} />
            </Stack>
          </Tabs.Panel>
          {kind !== 'sdk' && (
            <Tabs.Panel value="cli" pt="md">
              <Stack gap="md">
                <Text size="sm">One command creates a token and writes it into {label}’s settings. The token goes through the pipe and never appears on screen; the original file is backed up first.</Text>
                <Stack gap={4}>
                  <Text size="sm" fw={600}>
                    Before you start
                  </Text>
                  <List size="sm" spacing={4}>
                    <List.Item>
                      Install the <Code>harbor-pilot</Code> binary on that machine
                      {meta.data?.version ? (
                        <>
                          {' '}
                          (the same release as this server, <Code>{meta.data.version}</Code>)
                        </>
                      ) : null}
                      .
                    </List.Item>
                    <List.Item>
                      Save an API token (not an access token) in <Code>~/.config/harbor-pilot/api.token</Code>. Create one under{' '}
                      <Link to="/settings/service-principals">Settings → API tokens</Link> for an automation account that may manage access tokens.
                    </List.Item>
                  </List>
                </Stack>
                {binding ? (
                  <CodeBlock code={cmds[1]!.cmd} label={cmds[1]!.label} />
                ) : (
                  <Text size="sm" c="dimmed">
                    Choose a client in step 1 to get the command.
                  </Text>
                )}
                <Text size="xs" c="dimmed">
                  Check the result with <Code>harbor-pilot harness status</Code>.
                </Text>
                <Accordion variant="contained" radius="md">
                  <Accordion.Item value="preview">
                    <Accordion.Control>Preview the change first</Accordion.Control>
                    <Accordion.Panel>
                      <CodeBlock code={cmds[0]!.cmd} label={cmds[0]!.label} />
                    </Accordion.Panel>
                  </Accordion.Item>
                  <Accordion.Item value="remove">
                    <Accordion.Control>Remove this setup</Accordion.Control>
                    <Accordion.Panel>
                      <Stack gap="sm">
                        {cmds.slice(3).map((c) => (
                          <Stack key={c.label} gap={4}>
                            <Text size="sm">{c.label}</Text>
                            <CodeBlock code={c.cmd} label={c.label} />
                          </Stack>
                        ))}
                      </Stack>
                    </Accordion.Panel>
                  </Accordion.Item>
                </Accordion>
              </Stack>
            </Tabs.Panel>
          )}
        </Tabs>
      </Step>
      {issuing && binding && <IssueCredentialDialog binding={binding} onClose={() => setIssuing(false)} />}
    </Stack>
  )
}
