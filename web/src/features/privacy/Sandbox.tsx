import { useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { FlaskConical, ShieldCheck } from 'lucide-react'
import { Badge, Banner, Button, KeyValues, Panel, TableScroll } from '@/components/ui'
import { Field, MutationError, SelectField, formStyles as f } from '@/components/form'
import { apiSend } from '@/lib/api/client'
import { resourceListQuery } from '@/lib/api/queries'
import type { PrivacyPolicyRes, PrivacyPolicySpec, SandboxResult } from '@/lib/api/types'
import { actionTone, defaultPrivacySpec, detectorLabel, segments } from './privacy'
import p from './privacy.module.css'

/** Synthetic sample: credential-shaped values that are not real secrets. */
export const SYNTHETIC_SAMPLE = [
  'Deploy notes (synthetic):',
  'export ANTHROPIC_API_KEY=sk-ant-api03-EXAMPLEexampleEXAMPLE0000000000',
  'DB_PASSWORD="correct-horse-battery-staple"',
  'git clone https://deploy:hunter2hunter2@git.example.test/acme/app.git',
  'Contact ops@example.test about ticket TICKET-1234.',
].join('\n')

type Source = { kind: 'saved'; id: string } | { kind: 'unsaved' }

/** Non-persisting rule tester. */
export function Sandbox() {
  const policies = useQuery(resourceListQuery<PrivacyPolicyRes>('privacy-policies'))
  const [source, setSource] = useState('unsaved')
  const [specText, setSpecText] = useState(() => JSON.stringify({ ...defaultPrivacySpec(), personal_data: ['email'] }, null, 2))
  const [text, setText] = useState('')
  const [specError, setSpecError] = useState<string>()
  const run = useMutation({
    mutationFn: (body: { policy_id?: string; policy?: PrivacyPolicySpec; text: string }) => apiSend<SandboxResult>('POST', '/privacy/sandbox', body),
  })
  const items = policies.data?.items ?? []
  const src: Source = source === 'unsaved' ? { kind: 'unsaved' } : { kind: 'saved', id: source }

  const submit = () => {
    if (src.kind === 'saved') {
      run.mutate({ policy_id: src.id, text })
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
    <div className={p.stack}>
      <Banner tone="info" icon={ShieldCheck}>
        Nothing you test here is stored or logged. Prefer synthetic samples over real secrets.
      </Banner>
      <div className={p.twoCol}>
        <Panel title="Policy and sample" subtitle="Test a saved policy (including its configured secret values) or an unsaved definition.">
          <div className={p.stack}>
            <SelectField label="Policy" value={source} onChange={setSource}
              options={[{ value: 'unsaved', label: 'Unsaved policy (edit below)' }, ...items.map((x) => ({ value: x.id, label: x.name }))]} />
            {src.kind === 'unsaved' && (
              <Field label="Policy definition (JSON)" error={specError} hint="The same fields as a saved privacy policy.">
                {(fp) => (
                  <textarea {...fp} className={`${f.textarea} ${p.textarea}`} rows={10} spellCheck={false} value={specText} onChange={(e) => setSpecText(e.target.value)} />
                )}
              </Field>
            )}
            <Field label="Sample text">
              {(fp) => (
                <textarea {...fp} className={`${f.textarea} ${p.textarea}`} rows={8} spellCheck={false} autoComplete="off" value={text}
                  onChange={(e) => setText(e.target.value)} placeholder="Paste a synthetic sample…" />
              )}
            </Field>
            <div className={f.actions} style={{ justifyContent: 'flex-start' }}>
              <Button onClick={() => setText(SYNTHETIC_SAMPLE)}>Use a synthetic sample</Button>
              <Button variant="primary" icon={FlaskConical} busy={run.isPending} disabled={!text} onClick={submit}>Test</Button>
            </div>
            <MutationError error={run.error} />
          </div>
        </Panel>
        <Panel title="Result" subtitle="What the policy detects and what a provider would receive.">
          {!res ? (
            <p className="secondary">Run a test to see detections.</p>
          ) : (
            <div className={p.stack}>
              <KeyValues
                items={[
                  ['Masked', <span className="num">{res.summary.masked}</span>],
                  ['Warned', <span className="num">{res.summary.warned}</span>],
                  ['Allowed', <span className="num">{res.summary.allowed}</span>],
                  ['Blocks the request', res.summary.blocked ? <Badge tone="error">Yes</Badge> : <Badge tone="success">No</Badge>],
                ]}
              />
              <div>
                <h3 style={{ fontSize: 'var(--text-sm)' }}>Detections</h3>
                <pre className={p.sample} aria-label="Sample with detections highlighted">
                  {segments(tested, res.findings).map((s, i) =>
                    s.finding ? (
                      <mark key={i} className={p.mark} data-action={s.finding.action} title={`${detectorLabel(s.finding.rule)} · ${s.finding.action}`}>
                        {s.text}
                        <span className={p.markLabel}>{s.finding.action}</span>
                      </mark>
                    ) : (
                      <span key={i}>{s.text}</span>
                    ),
                  )}
                </pre>
              </div>
              {res.findings.length > 0 && (
                <TableScroll label="Detections">
                  <table className="data-table" style={{ width: '100%' }}>
                    <thead>
                      <tr>
                        <th>Detector or rule</th>
                        <th>Action</th>
                        <th>Bytes</th>
                      </tr>
                    </thead>
                    <tbody>
                      {res.findings.map((x, i) => (
                        <tr key={i}>
                          <td>{detectorLabel(x.rule)} <span className="mono muted">{x.rule}</span></td>
                          <td><Badge tone={actionTone(x.action)}>{x.action}</Badge></td>
                          <td className="num">{x.start}–{x.end}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </TableScroll>
              )}
              <div>
                <h3 style={{ fontSize: 'var(--text-sm)' }}>As sent upstream</h3>
                <pre className={p.sample} aria-label="Masked preview">{res.masked}</pre>
              </div>
            </div>
          )}
        </Panel>
      </div>
    </div>
  )
}
