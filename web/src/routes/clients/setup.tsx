import { useState } from 'react'
import { createFileRoute, Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { PageHeader } from '@/components/PageHeader'
import { Badge, Banner, Panel, Tabs } from '@/components/ui'
import { SelectField, TextField } from '@/components/form'
import { metaQuery, resourceListQuery } from '@/lib/api/queries'
import type { AliasRes, BindingRes } from '@/lib/api/types'
import { CodeBlock } from '@/features/harness/CodeBlock'
import { commands, guide, harnessKinds, type HarnessKind } from '@/features/harness/guides'
import h from '@/features/harness/harness.module.css'

export const Route = createFileRoute('/clients/setup')({
  component: HarnessSetup,
})

function HarnessSetup() {
  const meta = useQuery(metaQuery)
  const bindings = useQuery(resourceListQuery<BindingRes>('client-bindings')).data?.items ?? []
  const aliases = useQuery(resourceListQuery<AliasRes>('model-aliases')).data?.items ?? []
  const [kind, setKind] = useState<HarnessKind>('claude')
  const [url, setUrl] = useState<string | null>(null)
  const [bindingId, setBindingId] = useState('')
  const [model, setModel] = useState('')
  const input = { url: url ?? meta.data?.public_url ?? '', model, bindingId }
  const g = guide(kind, input)
  return (
    <>
      <p>
        <Link to="/clients">← Clients</Link>
      </p>
      <PageHeader
        title="Harness setup"
        subtitle="Point a coding harness at Harbor Pilot. Every harness needs only the base URL and a gateway credential from a client binding."
      />
      <Panel title="Connection" subtitle="These values fill in the commands and snippets below. Nothing here is saved.">
        <div className={h.form}>
          <TextField label="Harbor Pilot URL" value={input.url} onChange={setUrl} placeholder="https://harbor-pilot.example.com" />
          <SelectField
            label="Client binding"
            hint="The credential is issued for this client."
            value={bindingId}
            onChange={setBindingId}
            options={[{ value: '', label: 'Choose a client…' }, ...bindings.map((b) => ({ value: b.id, label: b.name }))]}
          />
          <SelectField
            label="Model or alias"
            hint="Optional; otherwise the harness default."
            value={model}
            onChange={setModel}
            options={[{ value: '', label: 'Harness default' }, ...aliases.map((a) => ({ value: a.name, label: a.name }))]}
          />
        </div>
      </Panel>
      <Tabs label="Harness" value={kind} onChange={setKind} items={harnessKinds} />
      {!g.verified && (
        <Banner tone="info" role="status">
          This configuration follows {g.label}’s documentation and has not yet been checked against a live {g.label} install.
        </Banner>
      )}
      <div className={h.grid}>
        <Panel
          title="Automatic setup"
          subtitle="harbor-pilot edits the harness config reversibly: it shows a diff first, backs up the original, writes atomically and refuses to overwrite a file that changed since the preview."
        >
          <ol className={h.steps}>
            {commands(kind, input).map((c) => (
              <li key={c.label} className={h.step}>
                <span className={h.stepLabel}>{c.label}</span>
                <CodeBlock code={c.cmd} label={c.label} />
              </li>
            ))}
          </ol>
          <p className="muted" style={{ fontSize: 'var(--text-sm)', marginTop: 12 }}>
            <code>token issue</code> needs a service-principal API token (Settings → Service principals). You can also issue a credential on the
            client’s page and pipe it from the clipboard, for example <code>pbpaste | harbor-pilot harness setup {kind} … --token-stdin --yes</code>.
            Run <code>harbor-pilot doctor</code> to check the result.
          </p>
        </Panel>
        <Panel
          title="Manual setup"
          subtitle={
            <>
              Add this to <code>{g.file}</code>.{' '}
              {g.verified ? <Badge tone="success">Checked against Claude Code 2.1.318</Badge> : <Badge>Unverified</Badge>}
            </>
          }
        >
          <ol className={h.steps}>
            <li className={h.step}>
              <span className={h.stepLabel}>Store the credential in a file only you can read</span>
              <CodeBlock
                code={`mkdir -p ~/.config/harbor-pilot/tokens\numask 077 && pbpaste > ~/.config/harbor-pilot/tokens/${kind}.token`}
                label="Store the token file"
              />
            </li>
            <li className={h.step}>
              <span className={h.stepLabel}>
                {g.label} configuration ({g.lang.toUpperCase()})
              </span>
              <CodeBlock code={g.snippet} label={`${g.label} configuration`} />
              <span className="muted" style={{ fontSize: 'var(--text-sm)' }}>
                {g.token}
              </span>
            </li>
            {g.after.map((a) => (
              <li key={a} className={h.step}>
                <span className={h.stepLabel}>Then</span>
                {a.startsWith('export ') ? <CodeBlock code={a} label="Shell step" /> : <span>{a}</span>}
              </li>
            ))}
          </ol>
        </Panel>
      </div>
      <Panel title="Other API clients" subtitle="SDKs and tools that speak a provider protocol need no setup tooling.">
        <ul style={{ margin: 0, paddingLeft: 18, display: 'flex', flexDirection: 'column', gap: 6 }}>
          <li>
            OpenAI SDKs: <code>OPENAI_BASE_URL={input.url || 'https://harbor-pilot.example.com'}/v1</code> and <code>OPENAI_API_KEY</code> set to the
            credential.
          </li>
          <li>
            Anthropic SDKs: <code>ANTHROPIC_BASE_URL={input.url || 'https://harbor-pilot.example.com'}</code> and <code>ANTHROPIC_AUTH_TOKEN</code> set to the
            credential.
          </li>
          <li>
            Gemini clients: base URL <code>{input.url || 'https://harbor-pilot.example.com'}</code> with the credential in the <code>x-goog-api-key</code>{' '}
            header.
          </li>
        </ul>
      </Panel>
    </>
  )
}
