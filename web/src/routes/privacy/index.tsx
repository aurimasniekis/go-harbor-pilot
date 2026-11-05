import { createFileRoute } from '@tanstack/react-router'
import { Accordion, Stack, Text } from '@mantine/core'
import { z } from 'zod'
import { Badge } from '@/components/ui'
import { ResourceList } from '@/features/config/ResourceList'
import { PrivacyPolicyDialog } from '@/features/privacy/PrivacyPolicyDialog'
import { Sandbox } from '@/features/privacy/Sandbox'
import { detectorLabel } from '@/features/privacy/privacy'
import type { PrivacyPolicyRes } from '@/lib/api/types'

export const Route = createFileRoute('/privacy/')({
  validateSearch: z.object({ try: z.boolean().optional().catch(undefined) }).parse,
  component: PrivacyPolicies,
})

/** "AWS keys, GitHub tokens and 12 more · email, phone" */
function masks(r: PrivacyPolicyRes): string {
  const builtin = r.spec.detectors ?? []
  const personal = r.spec.personal_data ?? []
  const parts: string[] = []
  if (builtin.length === 0) parts.push('All built-in secret types')
  else parts.push(builtin.length <= 2 ? builtin.map(detectorLabel).join(', ') : `${builtin.slice(0, 2).map(detectorLabel).join(', ')} and ${builtin.length - 2} more`)
  if (personal.length) parts.push(personal.map(detectorLabel).join(', '))
  if (r.spec.rules?.length) parts.push(`${r.spec.rules.length} custom ${r.spec.rules.length === 1 ? 'rule' : 'rules'}`)
  return parts.join(' · ')
}

function PrivacyPolicies() {
  const { try: open } = Route.useSearch()
  return (
    <Stack gap="lg">
      <ResourceList<PrivacyPolicyRes>
        plural="privacy-policies"
        title="Masking policies"
        subtitle="Choose a policy in a routing plan to mask that plan’s requests. Masking is defense in depth: tools on the user’s machine are outside the gateway."
        noun="privacy policy"
        empty="Masking replaces credentials and other sensitive values with placeholders before requests leave the gateway."
        columns={[
          {
            header: 'When masking can’t run',
            mobile: 'aside',
            cell: (r) => (r.spec.required ? (r.spec.allow_unmasked_fallback ? <Badge tone="warning">Send unmasked</Badge> : <Badge tone="success">Block the request</Badge>) : <Badge>Send anyway</Badge>),
          },
          { header: 'Masks', mobile: 'line', cell: (r) => <Text size="sm">{masks(r)}</Text> },
          {
            header: 'Unmask in replies',
            mobile: 'meta',
            cell: (r) => (
              <>
                {r.spec.restore_in_responses ? 'On' : 'Off'}
                {r.spec.tool_arg_restore?.enabled && <> · <Badge tone="warning">Tool arguments</Badge></>}
              </>
            ),
          },
        ]}
        dialog={(r, close) => <PrivacyPolicyDialog policy={r} onClose={close} />}
      />
      <Accordion variant="separated" radius="md" defaultValue={open ? 'try' : null}>
        <Accordion.Item value="try">
          <Accordion.Control>Try a policy on sample text</Accordion.Control>
          <Accordion.Panel>
            <Sandbox />
          </Accordion.Panel>
        </Accordion.Item>
      </Accordion>
    </Stack>
  )
}
