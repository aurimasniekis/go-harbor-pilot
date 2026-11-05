import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { KeyRound } from 'lucide-react'
import { Button } from '@/components/ui'
import { Checkbox, Dialog, MutationError, SecretOnce, TextField, formStyles as f } from '@/components/form'
import { apiSend } from '@/lib/api/client'
import type { IssuedCredential } from '@/lib/api/types'
import { credentialScopes, toInt, toggle } from './constants'
import c from './config.module.css'

/** Snippets that connect common harnesses with only a base URL and token. */
export function ConnectSnippets({ issued }: { issued: IssuedCredential }) {
  const u = issued.base_urls
  const tok = issued.token
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      <h3 style={{ fontSize: 'var(--text-md)' }}>Connect your harness</h3>
      <div>
        <div className={f.label}>Claude Code</div>
        <pre className={c.snippet} data-testid="snippet-claude-code">{`export ANTHROPIC_BASE_URL=${u.anthropic ?? ''}\nexport ANTHROPIC_AUTH_TOKEN=${tok}`}</pre>
      </div>
      <div>
        <div className={f.label}>Codex (~/.codex/config.toml)</div>
        <pre className={c.snippet} data-testid="snippet-codex">{`model_provider = "harbor-pilot"\n\n[model_providers.harbor-pilot]\nname = "Harbor Pilot"\nbase_url = "${u.openai ?? ''}"\nwire_api = "responses"\nenv_key = "HARBOR_PILOT_TOKEN"\n\n# then: export HARBOR_PILOT_TOKEN=${tok}`}</pre>
      </div>
      <div>
        <div className={f.label}>OpenAI-compatible clients</div>
        <pre className={c.snippet} data-testid="snippet-openai">{`export OPENAI_BASE_URL=${u.openai ?? ''}\nexport OPENAI_API_KEY=${tok}`}</pre>
      </div>
    </div>
  )
}

export function IssueCredentialDialog({ bindingID, onClose }: { bindingID: string; onClose: () => void }) {
  const qc = useQueryClient()
  const [name, setName] = useState('')
  const [scopes, setScopes] = useState<string[]>(['gateway:invoke', 'gateway:models'])
  const [ttlDays, setTtlDays] = useState('90')
  const [issued, setIssued] = useState<IssuedCredential | null>(null)
  const issue = useMutation({
    mutationFn: () =>
      apiSend<IssuedCredential>('POST', `/client-bindings/${encodeURIComponent(bindingID)}/credentials`, { name, scopes, ttl_seconds: toInt(ttlDays) * 86400 }),
    onSuccess: (r) => {
      setIssued(r)
      void qc.invalidateQueries({ queryKey: ['resources'] })
    },
  })
  return (
    <Dialog open wide title={issued ? 'Credential issued' : 'Issue gateway credential'} onClose={onClose}>
      {issued ? (
        <>
          <SecretOnce label="gateway token" value={issued.token} />
          <ConnectSnippets issued={issued} />
          <div className={f.actions}>
            <Button variant="primary" onClick={onClose}>Done</Button>
          </div>
        </>
      ) : (
        <>
          <div className={f.grid}>
            <TextField label="Credential name" value={name} onChange={setName} maxLength={80} required />
            <TextField label="Lifetime (days, 0 = no expiry)" type="number" value={ttlDays} onChange={setTtlDays} />
          </div>
          <fieldset className={c.checks}>
            <legend className={c.legend}>Scopes</legend>
            {credentialScopes.map((sc) => (
              <Checkbox key={sc.value} label={<><span className="mono">{sc.value}</span> — {sc.label}</>} checked={scopes.includes(sc.value)}
                onChange={(on) => setScopes(toggle(scopes, sc.value, on))} />
            ))}
          </fieldset>
          <MutationError error={issue.error} />
          <div className={f.actions}>
            <Button onClick={onClose}>Cancel</Button>
            <Button variant="primary" icon={KeyRound} busy={issue.isPending} disabled={!name || scopes.length === 0} onClick={() => issue.mutate()}>
              Issue credential
            </Button>
          </div>
        </>
      )}
    </Dialog>
  )
}
