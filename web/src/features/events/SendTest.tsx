import { useMutation } from '@tanstack/react-query'
import { CheckCircle2, Send, XCircle } from 'lucide-react'
import { Button } from '@/components/ui'
import { apiSend, ApiError } from '@/lib/api/client'
import e from './events.module.css'

interface TestResult {
  delivered: boolean
  status?: number
  error?: string
}

/** Sends a test notification through a channel and shows the outcome inline. */
export function SendTest({ id, name }: { id: string; name: string }) {
  const test = useMutation({
    mutationFn: () => apiSend<TestResult>('POST', `/notification-channels/${encodeURIComponent(id)}/test`, {}),
  })
  const r = test.data
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4, alignItems: 'flex-start', minWidth: 0 }}>
      <Button size="small" icon={Send} busy={test.isPending} aria-label={`Send test to ${name}`} onClick={() => test.mutate()}>
        Send test
      </Button>
      <span role="status" aria-live="polite" className={e.result}>
        {test.isError ? (
          <span style={{ color: 'var(--error-text)' }}>
            <XCircle size={14} aria-hidden="true" style={{ verticalAlign: '-2px' }} /> {test.error instanceof ApiError ? test.error.message : 'The test request failed.'}
          </span>
        ) : r?.delivered ? (
          <span style={{ color: 'var(--success)' }}>
            <CheckCircle2 size={14} aria-hidden="true" style={{ verticalAlign: '-2px' }} /> Delivered{r.status ? ` (HTTP ${r.status})` : ''}
          </span>
        ) : r ? (
          <span style={{ color: 'var(--error-text)', overflowWrap: 'anywhere' }}>
            <XCircle size={14} aria-hidden="true" style={{ verticalAlign: '-2px' }} /> Not delivered{r.status ? ` (HTTP ${r.status})` : ''}{r.error ? `: ${r.error}` : ''}
          </span>
        ) : null}
      </span>
    </div>
  )
}
