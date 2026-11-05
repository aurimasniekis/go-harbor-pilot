import { useState } from 'react'
import { ArrowDown, Plus, Trash2 } from 'lucide-react'
import { Banner, Button } from '@/components/ui'
import { Checkbox, Dialog, MutationError, SelectField, TextField, formStyles as f } from '@/components/form'
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
    <Dialog open wide title={provider ? `Edit provider ${provider.name}` : 'Add provider'} onClose={onClose}>
      <TerraformEditWarning r={provider} />
      <div className={f.grid}>
        <TextField label="Name" value={name} onChange={setName} disabled={!!provider} required
          hint="Lowercase letters, digits, dot, dash and underscore. Cannot be changed later." />
        <SelectField label="Type" value={type} onChange={setType} options={providerTypes.map((t) => ({ value: t.value, label: t.label }))} />
        <TextField label="Display name" value={displayName} onChange={setDisplayName} maxLength={120} />
      </div>
      {bridge ? (
        <Banner tone="info">
          CLI bridge providers run the vendor's own binary on this host with each account's isolated login. They have no base URL.
        </Banner>
      ) : (
        <>
          <TextField label="Base URL" value={baseURL} onChange={setBaseURL}
            placeholder={meta?.defaultBaseURL || 'https://llm.example.com/v1'}
            hint={meta?.requiresBaseURL ? 'Required for this provider type.' : `Empty = type default (${meta?.defaultBaseURL}).`} />
          <Checkbox label="Allow a private or loopback destination (local models). Cloud metadata addresses stay blocked." checked={allowPrivate} onChange={setAllowPrivate} />
        </>
      )}
      <div className={f.grid}>
        <SelectField label="Classification" value={classification} onChange={setClassification}
          options={[{ value: '', label: 'Type default' }, ...classifications.map((v) => ({ value: v, label: v }))]}
          hint="Shown wherever this provider is used." />
        <TextField label="Request timeout (seconds, 0 = 600)" type="number" value={reqTimeout} onChange={setReqTimeout} />
        <TextField label="Stream idle timeout (seconds, 0 = 300)" type="number" value={idleTimeout} onChange={setIdleTimeout} />
      </div>
      <KeyValueEditor label="Static headers" value={headers} onChange={(v, ok) => { setHeaders(v); setHeadersValid(ok) }}
        hint="Non-secret headers sent upstream, one Name=value per line. Authentication headers are refused." />

      <fieldset className={c.checks} style={{ flexDirection: 'column' }}>
        <legend className={c.legend}>Models</legend>
        <div className={c.rows}>
          {models.length === 0 && <p className="secondary">No models declared. Pools can then reference any model id at this provider.</p>}
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
                <TextField label="Context window (tokens)" type="number" value={String(m.context_window)} onChange={(v) => setModel(i, { context_window: toInt(v) })} />
                <TextField label="Max output tokens" type="number" value={String(m.max_output_tokens)} onChange={(v) => setModel(i, { max_output_tokens: toInt(v) })} />
              </div>
              <fieldset className={c.checks}>
                <legend className={c.legend}>Capabilities</legend>
                {capabilities.map((cap) => (
                  <Checkbox key={cap} label={<span className="mono">{cap}</span>} checked={m.capabilities.includes(cap)}
                    onChange={(on) => setModel(i, { capabilities: toggle(m.capabilities, cap, on) })} />
                ))}
              </fieldset>
              <Checkbox label="List prices (used for cost estimates)" checked={!!m.pricing}
                onChange={(on) => setModel(i, {
                  pricing: on ? { currency: 'USD', input_per_mtok: '', output_per_mtok: '', cache_read_per_mtok: '', cache_write_per_mtok: '', price_version: '' } : undefined,
                })} />
              {m.pricing && (
                <div className={f.grid}>
                  <TextField label="Currency" value={m.pricing.currency} onChange={(v) => setModel(i, { pricing: { ...m.pricing!, currency: v.toUpperCase() } })} maxLength={3} />
                  <TextField label="Input per 1M tokens" value={m.pricing.input_per_mtok} onChange={(v) => setModel(i, { pricing: { ...m.pricing!, input_per_mtok: v } })} />
                  <TextField label="Output per 1M tokens" value={m.pricing.output_per_mtok} onChange={(v) => setModel(i, { pricing: { ...m.pricing!, output_per_mtok: v } })} />
                  <TextField label="Cache read per 1M" value={m.pricing.cache_read_per_mtok ?? ''} onChange={(v) => setModel(i, { pricing: { ...m.pricing!, cache_read_per_mtok: v } })} />
                  <TextField label="Cache write per 1M" value={m.pricing.cache_write_per_mtok ?? ''} onChange={(v) => setModel(i, { pricing: { ...m.pricing!, cache_write_per_mtok: v } })} />
                  <TextField label="Price version" value={m.pricing.price_version ?? ''} onChange={(v) => setModel(i, { pricing: { ...m.pricing!, price_version: v } })} hint="Recorded on every estimate." />
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
      </fieldset>
      <MutationError error={save.error} />
      <div className={f.actions}>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} disabled={!name || !headersValid} onClick={submit}>
          Save provider
        </Button>
      </div>
    </Dialog>
  )
}
