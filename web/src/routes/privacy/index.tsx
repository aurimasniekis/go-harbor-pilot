import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Accordion, Menu, Stack, Text } from '@mantine/core'
import { FlaskConical } from 'lucide-react'
import { z } from 'zod'
import { Badge } from '@/components/ui'
import { ResourceList } from '@/features/config/ResourceList'
import { PrivacyPolicyDialog } from '@/features/privacy/PrivacyPolicyDialog'
import { Sandbox } from '@/features/privacy/Sandbox'
import { enforcementOf, enforcementOptions, masksSummary } from '@/features/privacy/privacy'
import { privacyUsers, usePlanDocuments } from '@/features/routing/usage'
import { resourceListQuery } from '@/lib/api/queries'
import type { PrivacyPolicyRes, TracePolicyRes } from '@/lib/api/types'

export const Route = createFileRoute('/privacy/')({
  validateSearch: z.object({ try: z.boolean().optional().catch(undefined), policy: z.string().optional().catch(undefined) }).parse,
  component: PrivacyPolicies,
})

function PrivacyPolicies() {
  const { try: open, policy } = Route.useSearch()
  const navigate = useNavigate()
  const plans = usePlanDocuments()
  const capture = useQuery(resourceListQuery<TracePolicyRes>('trace-policies'))
  const users = privacyUsers(plans, 'policy_id')
  // Capture rules can mask their content with a masking policy too.
  for (const t of capture.data?.items ?? []) {
    const id = t.spec.redaction_policy_id
    if (id) (users[id] ??= []).push(`capture rule ${t.name}`)
  }
  const tryPolicy = (id: string) => {
    void navigate({ to: '/privacy', search: { try: true, policy: id }, replace: true })
    requestAnimationFrame(() => document.getElementById('try-policy')?.scrollIntoView({ block: 'start', behavior: 'smooth' }))
  }
  return (
    <Stack gap="lg">
      <ResourceList<PrivacyPolicyRes>
        plural="privacy-policies"
        title="Masking policies"
        hideTitle
        explain="A routing plan that chooses a masking policy has secrets and personal data replaced with placeholders before its requests leave Harbor Pilot."
        noun="masking policy"
        empty="Masking replaces credentials and other sensitive values with placeholders before requests leave the gateway."
        usedBy={(r) => users[r.id] ?? []}
        columns={[
          {
            header: 'When masking can’t run',
            mobile: 'aside',
            cell: (r) => {
              const o = enforcementOptions.find((x) => x.value === enforcementOf(r.spec))
              return <Badge tone={o?.tone ?? 'neutral'}>{o?.label}</Badge>
            },
          },
          { header: 'Masks', mobile: 'line', cell: (r) => <Text size="sm">{masksSummary(r.spec)}</Text> },
          {
            header: 'Unmask in replies',
            mobile: 'meta',
            cell: (r) => (
              <>
                {r.spec.restore_in_responses ? 'Yes' : 'No'}
                {r.spec.tool_arg_restore?.enabled && <> · <Badge tone="warning">Tool arguments too</Badge></>}
              </>
            ),
          },
        ]}
        rowActions={(r) => (
          <Menu.Item leftSection={<FlaskConical size={14} />} onClick={() => tryPolicy(r.id)}>
            Try on sample text
          </Menu.Item>
        )}
        dialog={(r, close) => <PrivacyPolicyDialog policy={r} onClose={close} />}
      />
      <Accordion id="try-policy" variant="separated" radius="md" value={open ? 'try' : null}
        onChange={(v) => void navigate({ to: '/privacy', search: v ? { try: true, policy } : {}, replace: true })}>
        <Accordion.Item value="try">
          <Accordion.Control icon={<FlaskConical size={16} />}>Try a policy on sample text</Accordion.Control>
          <Accordion.Panel>
            <Sandbox policyId={policy} />
          </Accordion.Panel>
        </Accordion.Item>
      </Accordion>
    </Stack>
  )
}
