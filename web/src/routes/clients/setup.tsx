import { useState } from 'react'
import { createFileRoute, Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Accordion, Anchor, Badge, Group, Stack, Text, Title } from '@mantine/core'
import { z } from 'zod'
import { Banner, Tabs } from '@/components/ui'
import { SelectField, TextField } from '@/components/form'
import { metaQuery, resourceListQuery } from '@/lib/api/queries'
import type { AliasRes, BindingRes } from '@/lib/api/types'
import { CodeBlock } from '@/features/harness/CodeBlock'
import { commands, guide, harnessKinds, type HarnessKind } from '@/features/harness/guides'
import h from '@/features/harness/harness.module.css'

export const Route = createFileRoute('/clients/setup')({
  validateSearch: z.object({ binding: z.string().optional().catch(undefined) }).parse,
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

function HarnessSetup() {
  const { binding } = Route.useSearch()
  const meta = useQuery(metaQuery)
  const bindings = useQuery(resourceListQuery<BindingRes>('client-bindings')).data?.items ?? []
  const aliases = useQuery(resourceListQuery<AliasRes>('model-aliases')).data?.items ?? []
  const [kind, setKind] = useState<HarnessKind>('claude')
  const [url, setUrl] = useState<string | null>(null)
  const [bindingId, setBindingId] = useState(binding ?? '')
  const [model, setModel] = useState('')
  const input = { url: url ?? meta.data?.public_url ?? '', model, bindingId }
  const g = guide(kind, input)
  const cmds = commands(kind, input)
  const main = cmds[1]!
  const remove = cmds.slice(3)

  return (
    <Stack gap="xl" maw={920}>
      <Stack gap={4}>
        <Text size="sm">
          <Anchor component={Link} to="/clients" c="dimmed">
            Clients
          </Anchor>{' '}
          <Text span c="dimmed">
            /
          </Text>
        </Text>
        <Title order={1}>Harness setup</Title>
        <Text c="dimmed" size="sm">
          Point a coding tool at Harbor Pilot with a client’s access token.
        </Text>
      </Stack>

      <Step n={1} title="Choose the client">
        <div className={h.form}>
          <SelectField
            label="Client"
            value={bindingId}
            onChange={setBindingId}
            options={[{ value: '', label: 'Choose a client…' }, ...bindings.map((b) => ({ value: b.id, label: b.spec.display_name || b.name }))]}
          />
          <TextField label="Harbor Pilot URL" value={input.url} onChange={setUrl} placeholder="https://harbor-pilot.example.com" />
          <SelectField
            label="Model"
            hint="Optional; otherwise the tool’s default."
            value={model}
            onChange={setModel}
            options={[{ value: '', label: 'Tool default' }, ...aliases.map((a) => ({ value: a.name, label: a.name }))]}
          />
        </div>
      </Step>

      <Step n={2} title="Pick the tool">
        <Tabs label="Harness" value={kind} onChange={setKind} items={harnessKinds} />
        {g.verified ? (
          <Group gap={6}>
            <Badge color="green" variant="light">
              Checked against Claude Code 2.1.318
            </Badge>
          </Group>
        ) : (
          <Banner tone="info" role="status">
            This follows {g.label}’s documentation and has not yet been checked against a live {g.label} install.
          </Banner>
        )}
      </Step>

      <Step n={3} title="Run this on the machine with the tool">
        <Text size="sm" c="dimmed">
          It creates an access token and writes it into {g.label}’s settings. The token goes through the pipe and never appears on screen; the original file is
          backed up first.
        </Text>
        <CodeBlock code={main.cmd} label={main.label} />
        <Text size="xs" c="dimmed">
          Needs an API token saved in <code>~/.config/harbor-pilot/api.token</code> (Settings → API tokens). Check the result with{' '}
          <code>harbor-pilot harness status</code>.
        </Text>
        <Accordion variant="separated" radius="md">
          <Accordion.Item value="preview">
            <Accordion.Control>Preview the change first</Accordion.Control>
            <Accordion.Panel>
              <CodeBlock code={cmds[0]!.cmd} label={cmds[0]!.label} />
            </Accordion.Panel>
          </Accordion.Item>
          <Accordion.Item value="manual">
            <Accordion.Control>Prefer to edit the configuration by hand?</Accordion.Control>
            <Accordion.Panel>
              <ol className={h.steps}>
                <li className={h.step}>
                  <span className={h.stepLabel}>Store the access token in a file only you can read</span>
                  <CodeBlock
                    code={`mkdir -p ~/.config/harbor-pilot/tokens\numask 077 && pbpaste > ~/.config/harbor-pilot/tokens/${kind}.token`}
                    label="Store the token file"
                  />
                </li>
                <li className={h.step}>
                  <span className={h.stepLabel}>
                    Add this to <code>{g.file}</code>
                  </span>
                  <CodeBlock code={g.snippet} label={`${g.label} configuration`} />
                  <Text size="sm" c="dimmed">
                    {g.token}
                  </Text>
                </li>
                {g.after.map((a) => (
                  <li key={a} className={h.step}>
                    <span className={h.stepLabel}>Then</span>
                    {a.startsWith('export ') ? <CodeBlock code={a} label="Shell step" /> : <span>{a}</span>}
                  </li>
                ))}
              </ol>
            </Accordion.Panel>
          </Accordion.Item>
          <Accordion.Item value="remove">
            <Accordion.Control>Remove this setup</Accordion.Control>
            <Accordion.Panel>
              <Stack gap="sm">
                {remove.map((c) => (
                  <Stack key={c.label} gap={4}>
                    <Text size="sm">{c.label}</Text>
                    <CodeBlock code={c.cmd} label={c.label} />
                  </Stack>
                ))}
              </Stack>
            </Accordion.Panel>
          </Accordion.Item>
          <Accordion.Item value="sdk">
            <Accordion.Control>Another API client or SDK</Accordion.Control>
            <Accordion.Panel>
              <Stack gap={6}>
                <Text size="sm">
                  OpenAI SDKs: <code>OPENAI_BASE_URL={input.url || 'https://harbor-pilot.example.com'}/v1</code> and <code>OPENAI_API_KEY</code> set to the
                  access token.
                </Text>
                <Text size="sm">
                  Anthropic SDKs: <code>ANTHROPIC_BASE_URL={input.url || 'https://harbor-pilot.example.com'}</code> and <code>ANTHROPIC_AUTH_TOKEN</code>.
                </Text>
                <Text size="sm">
                  Gemini clients: base URL <code>{input.url || 'https://harbor-pilot.example.com'}</code> with the token in the <code>x-goog-api-key</code>{' '}
                  header.
                </Text>
              </Stack>
            </Accordion.Panel>
          </Accordion.Item>
        </Accordion>
      </Step>
    </Stack>
  )
}
