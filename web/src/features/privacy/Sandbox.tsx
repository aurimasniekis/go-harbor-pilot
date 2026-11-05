import { type ReactNode, useEffect, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Group, Paper, SimpleGrid, Stack, Text, Title } from '@mantine/core'
import { FlaskConical, ShieldCheck } from 'lucide-react'
import { Badge, Banner, Button, TableScroll } from '@/components/ui'
import { Advanced, MutationError, SelectField, TextAreaField, formStyles as f } from '@/components/form'
import { apiSend } from '@/lib/api/client'
import { resourceListQuery } from '@/lib/api/queries'
import type { PrivacyPolicyRes, PrivacyPolicySpec, SandboxResult } from '@/lib/api/types'
import { actionTone, defaultPrivacySpec, detectorLabel, hint, segments } from './privacy'
import p from './privacy.module.css'

/** Synthetic sample: credential-shaped values that are not real secrets. */
export const SYNTHETIC_SAMPLE = [
  'Deploy notes (synthetic):',
  'export ANTHROPIC_API_KEY=sk-ant-api03-EXAMPLEexampleEXAMPLE0000000000',
  'DB_PASSWORD="correct-horse-battery-staple"',
  'git clone https://deploy:hunter2hunter2@git.example.test/acme/app.git',
  'Contact ops@example.test about ticket TICKET-1234.',
].join('\n')

const actionWords: Record<string, string> = { mask: 'Masked', block: 'Blocks', warn: 'Warned', allow: 'Allowed' }

export type SandboxBody = { policy_id?: string; policy?: PrivacyPolicySpec; text: string }

export function useSandbox() {
  return useMutation({ mutationFn: (body: SandboxBody) => apiSend<SandboxResult>('POST', '/privacy/sandbox', body) })
}

function Stat({ label, value, tone }: { label: string; value: ReactNode; tone?: 'error' | 'success' }) {
  return (
    <Paper withBorder radius="sm" p="xs" miw={0}>
      <Text size="xs" c="dimmed">
        {label}
      </Text>
      <Text fw={600} size="lg" className="num" c={tone === 'error' ? 'var(--error-text)' : undefined}>
        {value}
      </Text>
    </Paper>
  )
}

/** What a policy found in a sample and what a provider would receive. */
export function SandboxResultView({ text, res }: { text: string; res: SandboxResult }) {
  const bytes = new TextEncoder().encode(text)
  const dec = new TextDecoder()
  const found = (start: number, end: number) => dec.decode(bytes.slice(start, end))
  const actions = [...new Set(res.findings.map((x) => x.action))]
  return (
    <Stack gap="md" aria-label="Test result">
      <SimpleGrid cols={{ base: 2, sm: 4 }} spacing="xs">
        <Stat label="Masked" value={res.summary.masked} />
        <Stat label="Warned" value={res.summary.warned} />
        <Stat label="Allowed" value={res.summary.allowed} />
        <Stat label="Request" value={res.summary.blocked ? 'Blocked' : 'Sent'} tone={res.summary.blocked ? 'error' : 'success'} />
      </SimpleGrid>
      <Stack gap={6}>
        <Group justify="space-between" gap="xs">
          <Title order={4}>Detections</Title>
          {actions.length > 0 && (
            <Group gap={4} aria-label="Legend">
              {actions.map((a) => (
                <span key={a} className={p.legend} data-action={a}>
                  {actionWords[a] ?? a}
                </span>
              ))}
            </Group>
          )}
        </Group>
        <pre className={p.sample} aria-label="Sample with detections highlighted">
          {segments(text, res.findings).map((s, i) =>
            s.finding ? (
              <mark key={i} className={p.mark} data-action={s.finding.action} title={`${detectorLabel(s.finding.rule)} · ${actionWords[s.finding.action] ?? s.finding.action}`}>
                {s.text}
              </mark>
            ) : (
              <span key={i}>{s.text}</span>
            ),
          )}
        </pre>
      </Stack>
      {res.findings.length === 0 ? (
        <Text size="sm" c="dimmed">
          Nothing detected; the text would be sent as it is.
        </Text>
      ) : (
        <TableScroll label="Detections">
          <table className="data-table" style={{ width: '100%' }}>
            <thead>
              <tr>
                <th>Found</th>
                <th>Detector or rule</th>
                <th>Action</th>
              </tr>
            </thead>
            <tbody>
              {res.findings.map((x, i) => (
                <tr key={i}>
                  <td className="mono">{hint(found(x.start, x.end))}</td>
                  <td>{detectorLabel(x.rule)}</td>
                  <td>
                    <Badge tone={actionTone(x.action)}>{actionWords[x.action] ?? x.action}</Badge>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableScroll>
      )}
      <Stack gap={6}>
        <Title order={4}>{res.summary.blocked ? 'Would be blocked; nothing is sent' : 'As sent to the provider'}</Title>
        {!res.summary.blocked && (
          <pre className={p.sample} aria-label="Masked preview">
            {res.masked}
          </pre>
        )}
      </Stack>
    </Stack>
  )
}

/** Non-persisting rule tester for saved policies (or a JSON definition). */
export function Sandbox({ policyId }: { policyId?: string }) {
  const policies = useQuery(resourceListQuery<PrivacyPolicyRes>('privacy-policies'))
  const items = policies.data?.items ?? []
  const [source, setSource] = useState(policyId ?? '')
  const [specText, setSpecText] = useState(() => JSON.stringify({ ...defaultPrivacySpec(), personal_data: ['email'] }, null, 2))
  const [text, setText] = useState('')
  const [specError, setSpecError] = useState<string>()
  const run = useSandbox()
  // Default to a saved policy: that is what someone wants to check.
  const firstId = items[0]?.id
  useEffect(() => {
    if (policyId) setSource(policyId)
    else if (!source && firstId) setSource(firstId)
  }, [policyId, firstId, source])

  const submit = () => {
    if (source !== 'json') {
      run.mutate({ policy_id: source, text })
      return
    }
    try {
      const spec = JSON.parse(specText) as PrivacyPolicySpec
      setSpecError(undefined)
      run.mutate({ policy: spec, text })
    } catch (e) {
      setSpecError(`Invalid JSON: ${e instanceof Error ? e.message : 'parse error'}`)
    }
  }

  const res = run.data
  // Findings refer to the text the request was made with.
  const tested = run.variables?.text ?? ''
  return (
    <Stack gap="md">
      <Banner tone="info" icon={ShieldCheck}>
        Nothing you test here is stored or logged. Prefer synthetic samples over real secrets.
      </Banner>
      <div className={p.twoCol}>
        <Stack gap="sm">
          <SelectField
            label="Policy"
            value={source}
            onChange={(v) => {
              setSource(v)
              run.reset()
            }}
            placeholder="Choose a policy"
            options={[...items.map((x) => ({ value: x.id, label: x.name })), { value: 'json', label: 'A definition written as JSON…' }]}
          />
          {source === 'json' && (
            <TextAreaField label="Policy definition (JSON)" error={specError} hint="The same fields as a saved masking policy." value={specText} onChange={setSpecText} mono minRows={8} />
          )}
          <TextAreaField label="Sample text" value={text} onChange={setText} mono minRows={6} placeholder="Paste a synthetic sample…" />
          <div className={f.actions} style={{ justifyContent: 'flex-start' }}>
            <Button onClick={() => setText(SYNTHETIC_SAMPLE)}>Use a synthetic sample</Button>
            <Button variant="primary" icon={FlaskConical} busy={run.isPending} disabled={!text || !source} onClick={submit}>
              Test
            </Button>
          </div>
          <MutationError error={run.error} />
        </Stack>
        <div>{!res ? <Text size="sm" c="dimmed">Run a test to see what the policy finds and what a provider would receive.</Text> : <SandboxResultView text={tested} res={res} />}</div>
      </div>
    </Stack>
  )
}

/** Tries the policy being edited, before it is saved. */
export function TryPolicy({ spec, hasStoredValues }: { spec: () => PrivacyPolicySpec; hasStoredValues: boolean }) {
  const [text, setText] = useState(SYNTHETIC_SAMPLE)
  const run = useSandbox()
  return (
    <Advanced label="Try this policy on sample text">
      <Stack gap="sm">
        <Text size="sm" c="dimmed">
          Tests the settings above as they are now, without saving. Nothing is stored or logged.
          {hasStoredValues && ' Stored secret values are not used here; save first, then test the policy from the list.'}
        </Text>
        <TextAreaField label="Sample text" value={text} onChange={setText} mono minRows={4} />
        <div>
          <Button icon={FlaskConical} busy={run.isPending} disabled={!text} onClick={() => run.mutate({ policy: spec(), text })}>
            Try
          </Button>
        </div>
        <MutationError error={run.error} />
        {run.data && <SandboxResultView text={run.variables?.text ?? ''} res={run.data} />}
      </Stack>
    </Advanced>
  )
}
