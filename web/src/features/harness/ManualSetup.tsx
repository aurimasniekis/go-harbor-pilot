import { Code, Stack, Text } from '@mantine/core'
import { CodeBlock } from './CodeBlock'
import { guide, saveTokenCommand, type GuideInput, type HarnessKind } from './guides'
import h from './harness.module.css'

/**
 * Hand setup for one tool: save the token to a private file, then point the
 * tool at Harbor Pilot. The token never appears in a configuration file.
 */
export function ManualSetup({ kind, input }: { kind: HarnessKind; input: GuideInput }) {
  const g = guide(kind, input)
  return (
    <ol className={h.steps}>
      <li className={h.step}>
        <span className={h.stepLabel}>1. Save the access token to a file only you can read (paste it when asked)</span>
        <CodeBlock code={saveTokenCommand(kind)} label="Save the token file" />
      </li>
      <li className={h.step}>
        <span className={h.stepLabel}>
          2. {kind === 'sdk' ? 'Set these in ' : 'Add this to '}
          <Code>{g.file}</Code>
        </span>
        <CodeBlock code={g.snippet} label={`${g.label} configuration`} />
        <Text size="xs" c="dimmed">
          {g.token}
        </Text>
      </li>
      {g.after.map((a, i) => (
        <li key={a} className={h.step}>
          <span className={h.stepLabel}>{i + 3}. Then</span>
          {a.startsWith('export ') ? <CodeBlock code={a} label="Shell step" /> : <Text size="sm">{a}</Text>}
        </li>
      ))}
    </ol>
  )
}

/** The token file path for a tool, for short references in text. */
export function TokenFileNote({ kind }: { kind: HarnessKind }) {
  return (
    <Stack gap={2}>
      <Text size="xs" c="dimmed">
        Token file: <Code>~/.config/harbor-pilot/tokens/{kind}.token</Code>
      </Text>
    </Stack>
  )
}
