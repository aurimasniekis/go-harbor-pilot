import { useMutation } from '@tanstack/react-query'
import { Group, Stack, Text } from '@mantine/core'
import { CheckCircle2, Send, XCircle } from 'lucide-react'
import { Button } from '@/components/ui'
import { apiSend } from '@/lib/api/client'
import { summarizeError } from '@/lib/api/errors'

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
  const failure = test.isError ? summarizeError(test.error).title : r && !r.delivered ? `${r.status ? `HTTP ${r.status}: ` : ''}${r.error ?? 'no reason given'}` : ''
  return (
    <Stack gap={4} align="flex-start">
      <Button size="small" icon={Send} busy={test.isPending} aria-label={`Send test to ${name}`} onClick={() => test.mutate()}>
        Send test
      </Button>
      <span role="status" aria-live="polite">
        {r?.delivered ? (
          <Group gap={4} wrap="nowrap" c="var(--success)">
            <CheckCircle2 size={12} aria-hidden="true" />
            <Text size="xs" c="inherit">
              Delivered
            </Text>
          </Group>
        ) : failure ? (
          <Group gap={4} wrap="nowrap" align="flex-start" c="var(--error-text)" maw={240} title={failure}>
            <XCircle size={12} aria-hidden="true" style={{ flex: 'none', marginTop: 2 }} />
            <Text size="xs" c="inherit" lineClamp={2} style={{ overflowWrap: 'anywhere' }}>
              Not delivered: {failure}
            </Text>
          </Group>
        ) : null}
      </span>
    </Stack>
  )
}
