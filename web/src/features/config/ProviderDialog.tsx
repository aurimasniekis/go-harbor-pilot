import { useState } from 'react'
import { ArrowDown, Plus, Trash2 } from 'lucide-react'
import { Banner, Button } from '@/components/ui'
import { Advanced, Checkbox, Dialog, MutationError, SelectField, TextField, formStyles as f } from '@/components/form'
import { SimpleGrid, Switch, Text, UnstyledButton } from '@mantine/core'
import { KeyValueEditor } from '@/components/KeyValueEditor'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { useSaveResource } from '@/lib/api/resources'
import type { ModelSpec, ProviderRes, ProviderSpec } from '@/lib/api/types'
import { capabilities, classifications, providerTypes, toInt, toggle, type ProviderType } from './constants'
import c from './config.module.css'

const emptyModel = (): ModelSpec => ({ id: '', display_name: '', context_window: 0, max_output_tokens: 0, capabilities: ['streaming'] })

/** Create or edit a provider. The full spec is always sent. */
export function ProviderDialog({ provider, onClose }: { provider: ProviderRes | null; onClose: () => void }) {
  const init = provider?.spec
  const [name, setName] = useState(provider?.name ?? '')
  const [type, setType] = useState<ProviderType>(init?.type ?? 'anthropic')
  const [displayName, setDisplayName] = useState(init?.display_name ?? '')
  const [baseURL, setBaseURL] = useState(init?.base_url ?? '')
  const [allowPrivate, setAllowPrivate] = useState(init?.allow_private_network ?? false)
  const [classification, setClassification] = useState<ProviderSpec['classification']>(init?.classification ?? '')
  const [reqTimeout, setReqTimeout] = useState(String(init?.request_timeout_seconds ?? 0))
  const [idleTimeout, setIdleTimeout] = useState(String(init?.stream_idle_timeout_seconds ?? 0))
  const [headers, setHeaders] = useState<Record<string, string>>(init?.headers ?? {})
  const [headersValid, setHeadersValid] = useState(true)
  const [models, setModels] = useState<ModelSpec[]>(init?.models ?? [])
  const save = useSaveResource<ProviderSpec>('providers')
  const meta = providerTypes.find((t) => t.value === type)
  const bridge = meta?.bridge ?? false

  const setModel = (i: number, patch: Partial<ModelSpec>) => setModels(models.map((m, j) => (j === i ? { ...m, ...patch } : m)))

  const submit = () => {
    const spec: ProviderSpec = {
      type,
      display_name: displayName,
      base_url: bridge ? '' : baseURL.trim(),
      allow_private_network: allowPrivate,
      classification,
      headers,
      request_timeout_seconds: toInt(reqTimeout),
      stream_idle_timeout_seconds: toInt(idleTimeout),
      models: models.map((m) => ({ ...m, id: m.id.trim() })),
    }
    save.mutate({ existing: provider ?? undefined, body: { name, spec } }, { onSuccess: onClose })
  }

  return (
    <Dialog open wide title={provider ? `Edit provider ${provider.name}` : 'Connect a provider'} onClose={onClose}>
      <TerraformEditWarning r={provider} />
      {!provider && (
        <div role="radiogroup" aria-label="Provider type">
          <Text size="sm" fw={500} mb={6}>
            What are you connecting?
          </Text>
          <SimpleGrid cols={{ base: 1, xs: 2, sm: 3 }} spacing="xs">
            {providerTypes.map((t) => (
              <UnstyledButton key={t.value} role="radio" aria-checked={type === t.value} className={c.toolCard} data-selected={type === t.value || undefined}
                onClick={() => setType(t.value)}>
                <Text size="sm" fw={600}>
                  {t.label}
                </Text>
                <Text size="xs" c="dimmed">
                  {typeHint[t.value]}
                </Text>
              </UnstyledButton>
            ))}
          </SimpleGrid>
        </div>
      )}
      <div className={f.grid}>
        <TextField label="Name" value={name} onChange={setName} disabled={!!provider} required placeholder="e.g. anthropic" hint={provider ? undefined : 'Lowercase; cannot be changed later.'} />
        <TextField label="Display name" value={displayName} onChange={setDisplayName} maxLength={120} placeholder="Optional" />
      </div>
      {bridge ? (
        <Banner tone="info">
          Runs the vendor’s own CLI on this server with each account’s separate login. Add an account with that login next.
        </Banner>
      ) : meta?.requiresBaseURL ? (
        <TextField label="Base URL" value={baseURL} onChange={setBaseURL} placeholder="https://llm.example.com/v1" required />
      ) : null}
      {!bridge && (
        <Advanced label="Connection">
          {!meta?.requiresBaseURL && (
            <TextField label="Base URL" value={baseURL} onChange={setBaseURL} placeholder={meta?.defaultBaseURL || 'https://llm.example.com/v1'} hint="Leave empty for the provider’s standard address." />
          )}
          <Switch label="Allow a local network address" description="For local models. Cloud metadata addresses stay blocked." checked={allowPrivate}
            onChange={(e) => setAllowPrivate(e.currentTarget.checked)} />
          <div className={f.grid}>
            <TextField label="Request timeout" type="number" suffix="s" value={reqTimeout === '0' ? '' : reqTimeout} onChange={setReqTimeout} placeholder="600" />
            <TextField label="Idle stream timeout" type="number" suffix="s" value={idleTimeout === '0' ? '' : idleTimeout} onChange={setIdleTimeout} placeholder="300" />
            <SelectField label="Shown as" value={classification} onChange={setClassification}
              options={[{ value: '', label: 'Standard for this type' }, ...classifications.map((v) => ({ value: v, label: classificationName[v] ?? v }))]} />
          </div>
          <KeyValueEditor label="Static headers" value={headers} onChange={(v, ok) => { setHeaders(v); setHeadersValid(ok) }}
            hint="Non-secret headers sent upstream, one Name=value per line. Authentication headers are refused." />
        </Advanced>
      )}
      <Advanced label={`Models and prices${models.length ? ` (${models.length})` : ''}`} defaultOpen={models.length > 0 && !!provider}>
        <div className={c.rows}>
          {models.length === 0 && <Text size="sm" c="dimmed">No models listed: fallback groups can use any model id of this provider. List models to set prices for cost estimates.</Text>}
          {models.map((m, i) => (
            <div key={i} className={c.row}>
              <div className={c.rowHeader}>
                <strong className="mono">{m.id || `Model ${i + 1}`}</strong>
                <Button size="small" variant="ghost" icon={Trash2} aria-label={`Remove model ${m.id || i + 1}`}
                  onClick={() => setModels(models.filter((_, j) => j !== i))} />
              </div>
              <div className={f.grid}>
                <TextField label="Model id" value={m.id} onChange={(v) => setModel(i, { id: v })} required />
                <TextField label="Display name" value={m.display_name} onChange={(v) => setModel(i, { display_name: v })} />
                <TextField label="Context window" type="number" suffix="tokens" value={m.context_window ? String(m.context_window) : ''} onChange={(v) => setModel(i, { context_window: toInt(v) })} />
                <TextField label="Max output" type="number" suffix="tokens" value={m.max_output_tokens ? String(m.max_output_tokens) : ''} onChange={(v) => setModel(i, { max_output_tokens: toInt(v) })} />
              </div>
              <fieldset className={c.checks}>
                <legend className={c.legend}>Capabilities</legend>
                {capabilities.map((cap) => (
                  <Checkbox key={cap} label={<span className="mono">{cap}</span>} checked={m.capabilities.includes(cap)}
                    onChange={(on) => setModel(i, { capabilities: toggle(m.capabilities, cap, on) })} />
                ))}
              </fieldset>
              <Checkbox label="Prices (used for cost estimates)" checked={!!m.pricing}
                onChange={(on) => setModel(i, {
                  pricing: on ? { currency: 'USD', input_per_mtok: '', output_per_mtok: '', cache_read_per_mtok: '', cache_write_per_mtok: '', price_version: '' } : undefined,
                })} />
              {m.pricing && (
                <div className={f.grid}>
                  <TextField label="Input" suffix="per 1M" value={m.pricing.input_per_mtok} onChange={(v) => setModel(i, { pricing: { ...m.pricing!, input_per_mtok: v } })} />
                  <TextField label="Output" suffix="per 1M" value={m.pricing.output_per_mtok} onChange={(v) => setModel(i, { pricing: { ...m.pricing!, output_per_mtok: v } })} />
                  <TextField label="Cache read" suffix="per 1M" value={m.pricing.cache_read_per_mtok ?? ''} onChange={(v) => setModel(i, { pricing: { ...m.pricing!, cache_read_per_mtok: v } })} />
                  <TextField label="Cache write" suffix="per 1M" value={m.pricing.cache_write_per_mtok ?? ''} onChange={(v) => setModel(i, { pricing: { ...m.pricing!, cache_write_per_mtok: v } })} />
                  <TextField label="Currency" value={m.pricing.currency} onChange={(v) => setModel(i, { pricing: { ...m.pricing!, currency: v.toUpperCase() } })} maxLength={3} />
                  <TextField label="Price list label" value={m.pricing.price_version ?? ''} onChange={(v) => setModel(i, { pricing: { ...m.pricing!, price_version: v } })} hint="Recorded on every estimate." />
                </div>
              )}
            </div>
          ))}
        </div>
        <div>
          <Button size="small" icon={models.length ? ArrowDown : Plus} onClick={() => setModels([...models, emptyModel()])}>
            Add model
          </Button>
        </div>
      </Advanced>
      <MutationError error={save.error} />
      <div className={f.actions}>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} disabled={!name || !headersValid || (!!meta?.requiresBaseURL && !baseURL.trim())} onClick={submit}>
          {provider ? 'Save provider' : 'Connect provider'}
        </Button>
      </div>
    </Dialog>
  )
}

const typeHint: Record<string, string> = {
  anthropic: 'Paste an Anthropic API key',
  openai: 'Paste an OpenAI API key',
  gemini: 'Paste a Gemini API key',
  openai_compatible: 'OpenRouter, DeepSeek, vLLM and others',
  ollama: 'A model server on your network',
  claude_code: 'A Claude subscription through the CLI',
  codex: 'A ChatGPT subscription through the CLI',
}

const classificationName: Record<string, string> = { official: 'Cloud', partial: 'Partial compatibility', unofficial: 'Unofficial', local: 'Local' }
