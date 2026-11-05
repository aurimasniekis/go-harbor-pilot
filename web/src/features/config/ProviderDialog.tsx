import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ActionIcon, Badge, Checkbox as MCheckbox, Group, Radio, SegmentedControl, SimpleGrid, Stack, Switch, Table, Text } from '@mantine/core'
import { Pencil, Plus, Trash2 } from 'lucide-react'
import { Banner, Button } from '@/components/ui'
import {
  Advanced,
  Dialog,
  MutationError,
  NameField,
  NumberField,
  SecretRefField,
  SelectField,
  TextField,
  checkSecretRef,
  nameError,
  useFieldErrors,
} from '@/components/form'
import { KeyValueEditor } from '@/components/KeyValueEditor'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { ApiError, apiSend } from '@/lib/api/client'
import { resourceListQuery } from '@/lib/api/queries'
import type { AccountRes, AccountSpec, ModelSpec, ProviderRes, ProviderSpec } from '@/lib/api/types'
import { freeName, isBridge } from '@/features/providers/labels'
import { classifications, providerTypes, type ProviderType } from './constants'
import { defaultBilling } from './AccountDialog'
import c from './config.module.css'

const emptyModel = (): ModelSpec => ({ id: '', display_name: '', context_window: 0, max_output_tokens: 0, capabilities: ['streaming'] })

/** Plain names for model capabilities. */
export const capabilityLabels: Record<string, string> = {
  streaming: 'Streaming',
  tools: 'Tool calls',
  parallel_tools: 'Parallel tool calls',
  tool_streaming: 'Streamed tool calls',
  vision: 'Images',
  reasoning: 'Reasoning',
  structured_output: 'Structured output',
  prompt_cache: 'Prompt caching',
  pdf: 'PDF input',
}

const typeHint: Record<string, string> = {
  anthropic: 'Claude models with an API key',
  openai: 'GPT models with an API key',
  gemini: 'Gemini models with an API key',
  openai_compatible: 'OpenRouter, DeepSeek, vLLM and others',
  ollama: 'A model server on your network',
  claude_code: 'A Claude subscription through its CLI',
  codex: 'A ChatGPT subscription through its CLI',
}

const classificationName: Record<string, string> = { official: 'Cloud', partial: 'Partial compatibility', unofficial: 'Unofficial', local: 'Local' }

/** Edit one model in its own small dialog. */
function ModelDialog({ model, onSave, onClose }: { model: ModelSpec; onSave: (m: ModelSpec) => void; onClose: () => void }) {
  const [m, setM] = useState<ModelSpec>(structuredClone(model))
  const set = (p: Partial<ModelSpec>) => setM({ ...m, ...p })
  const pricing = m.pricing
  const setPrice = (p: Partial<NonNullable<ModelSpec['pricing']>>) => set({ pricing: { ...pricing!, ...p } })
  return (
    <Dialog
      open
      title={model.id ? `Model ${model.id}` : 'Add a model'}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" disabled={!m.id.trim()} onClick={() => onSave({ ...m, id: m.id.trim() })}>
            Done
          </Button>
        </>
      }
    >
      <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
        <TextField label="Model id" value={m.id} onChange={(v) => set({ id: v })} required autoFocus mono hint="Exactly as the provider names it." />
        <TextField label="Display name" value={m.display_name} onChange={(v) => set({ display_name: v })} placeholder="Optional" />
        <NumberField label="Context window" unit="tokens" placeholder="Unknown" min={1} value={m.context_window || null} onChange={(n) => set({ context_window: n ?? 0 })} w="100%" />
        <NumberField label="Max output" unit="tokens" placeholder="Unknown" min={1} value={m.max_output_tokens || null} onChange={(n) => set({ max_output_tokens: n ?? 0 })} w="100%" />
      </SimpleGrid>
      <MCheckbox.Group label="Capabilities" value={m.capabilities} onChange={(v) => set({ capabilities: v })}>
        <SimpleGrid cols={{ base: 1, sm: 3 }} spacing={6} mt={6}>
          {Object.entries(capabilityLabels).map(([v, l]) => (
            <MCheckbox key={v} value={v} label={l} />
          ))}
        </SimpleGrid>
      </MCheckbox.Group>
      <Switch label="Prices (for cost estimates)" checked={!!pricing}
        onChange={(e) => set({ pricing: e.currentTarget.checked ? { currency: 'USD', input_per_mtok: '', output_per_mtok: '', cache_read_per_mtok: '', cache_write_per_mtok: '', price_version: '' } : undefined })} />
      {pricing && (
        <SimpleGrid cols={{ base: 2, sm: 4 }} spacing="md">
          <TextField label="Input" suffix="/1M" value={pricing.input_per_mtok} onChange={(v) => setPrice({ input_per_mtok: v })} placeholder="3.00" />
          <TextField label="Output" suffix="/1M" value={pricing.output_per_mtok} onChange={(v) => setPrice({ output_per_mtok: v })} placeholder="15.00" />
          <TextField label="Cache read" suffix="/1M" value={pricing.cache_read_per_mtok ?? ''} onChange={(v) => setPrice({ cache_read_per_mtok: v })} />
          <TextField label="Cache write" suffix="/1M" value={pricing.cache_write_per_mtok ?? ''} onChange={(v) => setPrice({ cache_write_per_mtok: v })} />
          <SelectField label="Currency" value={pricing.currency} onChange={(v) => setPrice({ currency: v })} options={['USD', 'EUR', 'GBP'].map((x) => ({ value: x, label: x }))} />
          <TextField label="Price list label" value={pricing.price_version ?? ''} onChange={(v) => setPrice({ price_version: v })} placeholder="2026-11" />
        </SimpleGrid>
      )}
    </Dialog>
  )
}

/** Create or edit a provider. When connecting an API, its first key can be added in the same step. */
export function ProviderDialog({ provider, onClose, onCreated }: {
  provider: ProviderRes | null
  onClose: () => void
  /** After connecting: the provider and the account created with the key, if any. */
  onCreated?: (p: ProviderRes, account?: AccountRes) => void
}) {
  const qc = useQueryClient()
  const providers = useQuery(resourceListQuery<ProviderRes>('providers'))
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const init = provider?.spec
  const [type, setType] = useState<ProviderType>(init?.type ?? 'anthropic')
  const takenNames = useMemo(() => new Set((providers.data?.items ?? []).map((p) => p.name)), [providers.data])
  const [nameEdited, setNameEdited] = useState(false)
  const [nameDraft, setNameDraft] = useState('')
  const name = provider ? provider.name : nameEdited ? nameDraft : freeName(type.replace(/_/g, '-'), takenNames)
  const [displayName, setDisplayName] = useState(init?.display_name ?? '')
  const [baseURL, setBaseURL] = useState(init?.base_url ?? '')
  const [allowPrivate, setAllowPrivate] = useState(init?.allow_private_network ?? false)
  const [classification, setClassification] = useState<ProviderSpec['classification']>(init?.classification ?? '')
  const [reqTimeout, setReqTimeout] = useState<number | null>(init?.request_timeout_seconds || null)
  const [idleTimeout, setIdleTimeout] = useState<number | null>(init?.stream_idle_timeout_seconds || null)
  const [headers, setHeaders] = useState<Record<string, string>>(init?.headers ?? {})
  const [headersValid, setHeadersValid] = useState(true)
  const [models, setModels] = useState<ModelSpec[]>(init?.models ?? [])
  const [editingModel, setEditingModel] = useState<number | 'new' | null>(null)
  const [keyMode, setKeyMode] = useState<'stored' | 'reference' | 'later'>('stored')
  const [key, setKey] = useState('')
  const [keyRef, setKeyRef] = useState('')
  const meta = providerTypes.find((t) => t.value === type)
  const bridge = isBridge(type)
  const askKey = !provider && !bridge && type !== 'ollama'

  const save = useMutation({
    mutationFn: async () => {
      const spec: ProviderSpec = {
        type,
        display_name: displayName.trim() || (provider ? '' : (meta?.label ?? '')),
        base_url: bridge ? '' : baseURL.trim(),
        allow_private_network: allowPrivate,
        classification,
        headers,
        request_timeout_seconds: reqTimeout ?? 0,
        stream_idle_timeout_seconds: idleTimeout ?? 0,
        models,
      }
      const p = provider
        ? await apiSend<ProviderRes>('PUT', `/providers/${encodeURIComponent(provider.id)}`, { name, spec }, { ifMatch: provider.revision })
        : await apiSend<ProviderRes>('POST', '/providers', { name, spec })
      let account: AccountRes | undefined
      if (askKey && keyMode !== 'later' && (key.trim() || keyRef.trim())) {
        const taken = new Set((accounts.data?.items ?? []).map((a) => a.name))
        const aspec: AccountSpec = {
          provider_id: p.id,
          display_name: 'API key',
          credential_ref: keyMode === 'reference' ? keyRef.trim() : '',
          billing: defaultBilling(type),
          allowed_models: [],
          max_concurrency: 0,
          requests_per_minute: 0,
          enabled: true,
          labels: {},
        }
        account = await apiSend<AccountRes>('POST', '/provider-accounts', {
          name: freeName(`${name}-key`, taken),
          spec: aspec,
          secrets: keyMode === 'stored' ? { credential: key.trim() } : undefined,
        })
      }
      return { p, account }
    },
    onSuccess: ({ p, account }) => {
      void qc.invalidateQueries({ queryKey: ['resources'] })
      void qc.invalidateQueries({ queryKey: ['quota'] })
      if (!provider && onCreated) onCreated(p, account)
      else onClose()
    },
  })
  const fe = useFieldErrors(save.error)
  const conflict = save.error instanceof ApiError && save.error.status === 409
  const urlErr = baseURL.trim() && !/^https?:\/\/[^/\s]+/.test(baseURL.trim()) ? 'Use an http or https address.' : undefined
  const reason = !name || nameError(name)
    ? 'Fix the short name.'
    : meta?.requiresBaseURL && !baseURL.trim()
      ? 'Enter the base URL.'
      : urlErr
        ? 'Fix the base URL.'
        : !headersValid
          ? 'Fix the static headers.'
          : askKey && keyMode === 'reference' && checkSecretRef(keyRef)
            ? 'Fix the key reference.'
            : ''

  return (
    <Dialog
      open
      wide
      title={provider ? `Edit ${provider.spec.display_name || provider.name}` : 'Connect a provider'}
      onClose={onClose}
      footer={
        <>
          {reason && (
            <Text size="xs" c="dimmed" mr="auto">
              {reason}
            </Text>
          )}
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" busy={save.isPending} disabled={!!reason} onClick={() => save.mutate()}>
            {provider ? 'Save provider' : askKey && keyMode !== 'later' && (key.trim() || keyRef.trim()) ? 'Connect and test' : 'Connect provider'}
          </Button>
        </>
      }
    >
      <TerraformEditWarning r={provider} />
      {!provider && (
        <Radio.Group label="What are you connecting?" value={type} onChange={(v) => setType(v as ProviderType)}>
          <SimpleGrid cols={{ base: 1, xs: 2, sm: 3 }} spacing="xs" mt={6}>
            {providerTypes.map((t) => (
              <Radio.Card key={t.value} value={t.value} className={c.toolCard} data-selected={type === t.value || undefined}>
                <Group gap="sm" wrap="nowrap" align="flex-start">
                  <Radio.Indicator mt={2} />
                  <Stack gap={0} miw={0}>
                    <Text size="sm" fw={600}>
                      {t.label}
                    </Text>
                    <Text size="xs" c="dimmed">
                      {typeHint[t.value]}
                    </Text>
                  </Stack>
                </Group>
              </Radio.Card>
            ))}
          </SimpleGrid>
        </Radio.Group>
      )}
      <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
        <TextField label="Display name" value={displayName} onChange={setDisplayName} maxLength={120} placeholder={meta?.label ?? 'Optional'} error={fe.display_name} />
        <NameField
          value={name}
          locked={!!provider}
          onChange={(v) => {
            setNameEdited(v !== '')
            setNameDraft(v)
          }}
          error={fe.name ?? (conflict && !provider ? `A provider called ${name} already exists.` : undefined)}
        />
      </SimpleGrid>
      {bridge ? (
        <Banner tone="info">Runs the vendor’s own CLI on this server. Next you add an account with the sign-in token of each subscription seat.</Banner>
      ) : meta?.requiresBaseURL ? (
        <TextField label="Base URL" value={baseURL} onChange={setBaseURL} placeholder="https://llm.example.com/v1" required mono error={fe.base_url ?? urlErr} />
      ) : null}
      {askKey && (
        <Stack gap={6}>
          <Text size="sm" fw={500}>
            API key
          </Text>
          <SegmentedControl aria-label="API key source" size="sm" value={keyMode} onChange={(v) => setKeyMode(v as typeof keyMode)}
            data={[{ value: 'stored', label: 'Paste it' }, { value: 'reference', label: 'From the server environment' }, { value: 'later', label: 'Add later' }]} />
          {keyMode === 'stored' && (
            <TextField label="API key" type="password" value={key} onChange={setKey} autoComplete="off" hint="Stored encrypted and never shown again. It becomes this provider’s first account." />
          )}
          {keyMode === 'reference' && (
            <SecretRefField label="API key reference" value={keyRef} onChange={setKeyRef} example={`${type.toUpperCase()}_KEY`} />
          )}
        </Stack>
      )}
      {!bridge && (
        <Advanced label="Connection">
          {!meta?.requiresBaseURL && (
            <TextField label="Base URL" value={baseURL} onChange={setBaseURL} placeholder={meta?.defaultBaseURL || 'https://llm.example.com/v1'} mono
              hint="Leave empty for the provider’s standard address." error={fe.base_url ?? urlErr} />
          )}
          <Switch label="Allow a local network address" description="Needed for local model servers and plain http. Cloud metadata addresses stay blocked." checked={allowPrivate}
            onChange={(e) => setAllowPrivate(e.currentTarget.checked)} />
          <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="md">
            <NumberField label="Request timeout" unit="s" placeholder="600" min={1} max={3600} value={reqTimeout} onChange={setReqTimeout} w="100%" error={fe.request_timeout_seconds} />
            <NumberField label="Idle stream timeout" unit="s" placeholder="300" min={1} max={3600} value={idleTimeout} onChange={setIdleTimeout} w="100%" error={fe.stream_idle_timeout_seconds} />
            <SelectField label="Shown as" value={classification} onChange={setClassification}
              options={[{ value: '', label: 'Standard for this type' }, ...classifications.map((v) => ({ value: v, label: classificationName[v] ?? v }))]} />
          </SimpleGrid>
          <KeyValueEditor label="Static headers" value={headers} onChange={(v, ok) => { setHeaders(v); setHeadersValid(ok) }} error={fe.headers}
            hint="Non-secret headers sent upstream, one Name=value per line. Authentication headers are refused." />
        </Advanced>
      )}
      <Advanced label={`Models and prices${models.length ? ` (${models.length})` : ''}`} defaultOpen={models.length > 0 && !!provider}>
        {models.length === 0 ? (
          <Text size="sm" c="dimmed">
            No models listed: fallback groups can use any model id of this provider. List models to set prices for cost estimates.
          </Text>
        ) : (
          <Table verticalSpacing={6} highlightOnHover>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Model</Table.Th>
                <Table.Th>Context</Table.Th>
                <Table.Th>Price in / out per 1M</Table.Th>
                <Table.Th>
                  <span className="visually-hidden">Actions</span>
                </Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {models.map((m, i) => (
                <Table.Tr key={`${m.id}-${i}`}>
                  <Table.Td>
                    <Stack gap={2}>
                      <Text size="sm" ff="monospace">
                        {m.id || `Model ${i + 1}`}
                      </Text>
                      <Group gap={4}>
                        {m.capabilities.filter((cap) => cap !== 'streaming').map((cap) => (
                          <Badge key={cap} size="xs" variant="light" color="gray">
                            {capabilityLabels[cap] ?? cap}
                          </Badge>
                        ))}
                      </Group>
                    </Stack>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm">{m.context_window ? `${Math.round(m.context_window / 1000)}k` : '—'}</Text>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm">{m.pricing ? `${m.pricing.input_per_mtok || '?'} / ${m.pricing.output_per_mtok || '?'} ${m.pricing.currency}` : 'Not set'}</Text>
                  </Table.Td>
                  <Table.Td align="right">
                    <Group gap={2} justify="flex-end" wrap="nowrap">
                      <ActionIcon variant="subtle" color="gray" aria-label={`Edit model ${m.id || i + 1}`} onClick={() => setEditingModel(i)}>
                        <Pencil size={14} />
                      </ActionIcon>
                      <ActionIcon variant="subtle" color="gray" aria-label={`Remove model ${m.id || i + 1}`} onClick={() => setModels(models.filter((_, j) => j !== i))}>
                        <Trash2 size={14} />
                      </ActionIcon>
                    </Group>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
        {(fe['models.0.id'] || Object.keys(fe).some((k) => k.startsWith('models.'))) && (
          <Text size="xs" c="red">
            {Object.entries(fe).find(([k]) => k.startsWith('models.'))?.[1]}
          </Text>
        )}
        <div>
          <Button size="small" icon={Plus} onClick={() => setEditingModel('new')}>
            Add model
          </Button>
        </div>
      </Advanced>
      <MutationError error={conflict && !provider ? null : save.error} />
      {editingModel !== null && (
        <ModelDialog
          model={editingModel === 'new' ? emptyModel() : models[editingModel]!}
          onClose={() => setEditingModel(null)}
          onSave={(m) => {
            setModels(editingModel === 'new' ? [...models, m] : models.map((x, j) => (j === editingModel ? m : x)))
            setEditingModel(null)
          }}
        />
      )}
    </Dialog>
  )
}
