import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { KeyRound } from 'lucide-react'
import { Button } from '@/components/ui'
import { Advanced, Checkbox, Dialog, MutationError, SecretOnce, TextField, formStyles as f } from '@/components/form'
import { Text } from '@mantine/core'
import { Link } from '@tanstack/react-router'
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
      <h3 style={{ fontSize: 'var(--text-md)' }}>Or set the tool up by hand</h3>
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
    <Dialog open wide={!!issued} title={issued ? 'Access token created' : 'Create access token'} onClose={onClose}>
      {issued ? (
        <>
          <SecretOnce label="access token" value={issued.token} />
          <ConnectSnippets issued={issued} />
          <Text size="sm" c="dimmed">
            For a reversible setup that keeps the token off the screen, use{' '}
            <Link to="/clients/setup" search={{ binding: bindingID }}>
              Set up a tool
            </Link>
            .
          </Text>
          <div className={f.actions}>
            <Button variant="primary" onClick={onClose}>Done</Button>
          </div>
        </>
      ) : (
        <>
          <TextField label="Token name" value={name} onChange={setName} maxLength={80} required autoFocus placeholder="e.g. laptop" hint="Where it is used, so you know which one to revoke." />
          <TextField label="Expires after" type="number" suffix="days" value={ttlDays} onChange={setTtlDays} hint="Leave empty or 0 for a token that does not expire." />
          <Advanced label="Permissions">
            <fieldset className={c.checks} style={{ flexDirection: 'column' }}>
              <legend className={c.legend}>This token can</legend>
              {credentialScopes.map((sc) => (
                <Checkbox key={sc.value} label={sc.label} description={sc.value} checked={scopes.includes(sc.value)}
                  onChange={(on) => setScopes(toggle(scopes, sc.value, on))} />
              ))}
            </fieldset>
          </Advanced>
          <MutationError error={issue.error} />
          <div className={f.actions}>
            <Button onClick={onClose}>Cancel</Button>
            <Button variant="primary" icon={KeyRound} busy={issue.isPending} disabled={!name || scopes.length === 0} onClick={() => issue.mutate()}>
              Create token
            </Button>
          </div>
        </>
      )}
    </Dialog>
  )
}
