import { useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Archive, Plus } from 'lucide-react'
import { Badge, Banner, Button, EmptyState, KeyValues, Panel, Skeleton, TableScroll } from '@/components/ui'
import { Accordion, Stack, Text } from '@mantine/core'
import { CopyText } from '@/ui/kit'
import { Checkbox, Dialog, MutationError, formStyles as f } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { apiSend } from '@/lib/api/client'
import { backupsQuery, metaQuery, systemStatusQuery } from '@/lib/api/queries'
import type { Backup, BackupCreated } from '@/lib/api/types'
import { fmtDateTime, fmtNumber, fmtRelative } from '@/lib/format'
import { CodeBlock } from '@/features/harness/CodeBlock'
import { BridgesPanel } from '@/features/config/BridgesPanel'

export const Route = createFileRoute('/settings/system')({
  component: SystemSettings,
})

function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`
}

function SystemSettings() {
  const meta = useQuery(metaQuery)
  const status = useQuery(systemStatusQuery)
  return (
    <>
      {status.data && !status.data.secure_cookies && (
        <Banner tone="warning">
          Sign-in uses plain HTTP, so session cookies are not protected. Set <code>public_url</code> to an https address.
        </Banner>
      )}
      <Backups />
      <Panel title="This instance">
        {status.isError ? (
          <QueryError error={status.error} retry={() => void status.refetch()} />
        ) : !status.data || !meta.data ? (
          <Skeleton height={120} />
        ) : (
          <KeyValues
            items={[
              ['Version', <CopyText value={meta.data.version} />],
              ['Public URL', <CopyText value={meta.data.public_url || '—'} />],
              ['Started', <span title={fmtDateTime(meta.data.started_at, { seconds: true })}>{fmtRelative(meta.data.started_at)}</span>],
              ['Data directory', <CopyText value={status.data.data_dir} maw={360} />],
              ['Database versions', <span className="num">{status.data.schema_version} · content {status.data.traces_schema_version}</span>],
              ['Master key', <CopyText value={status.data.master_key_id} />],
              ['Session cookies', status.data.secure_cookies ? 'Secure (https)' : <Badge tone="warning">Not secure (plain http)</Badge>],
            ]}
          />
        )}
      </Panel>
      <BridgesPanel />
    </>
  )
}

function Backups() {
  const qc = useQueryClient()
  const list = useQuery(backupsQuery)
  const [open, setOpen] = useState(false)
  const [created, setCreated] = useState<BackupCreated | null>(null)
  const items = list.data?.items ?? []
  return (
    <Panel
      title="Backups"
      subtitle="Consistent snapshots with checksums, taken while Harbor Pilot runs and written to the data directory. Copy them off the server; keep the master key separately."
      actions={
        <Button variant="primary" icon={Plus} onClick={() => setOpen(true)}>
          Take backup
        </Button>
      }
    >
      {created && (
        <Banner tone="info" role="status">
          Backup written to <span className="mono">{created.path}</span>
          {created.manifest.include_traces ? ' (including trace content).' : '.'}
        </Banner>
      )}
      {list.isError ? (
        <QueryError error={list.error} retry={() => void list.refetch()} />
      ) : !list.data ? (
        <TableSkeleton rows={3} />
      ) : items.length === 0 ? (
        <EmptyState icon={Archive} title="No backups yet">
          Take one here, or run <code>harbor-pilot backup --out DIR</code> on the server.
        </EmptyState>
      ) : (
        <BackupTable items={items} />
      )}
      <Accordion variant="separated" radius="md">
        <Accordion.Item value="restore">
          <Accordion.Control>How to restore</Accordion.Control>
          <Accordion.Panel>
            <Stack gap="sm">
              <Text size="sm">
                Restore runs on the server while Harbor Pilot is stopped. It checks the backup, checks that the configured master key can open its secrets,
                and keeps the current files aside (<code>*.pre-restore-&lt;time&gt;</code>) before installing it. There is no restore button here on purpose.
              </Text>
              <CodeBlock code={'harbor-pilot backup verify DIR\nharbor-pilot restore --from DIR --yes'} label="Restore commands" />
            </Stack>
          </Accordion.Panel>
        </Accordion.Item>
      </Accordion>
      <CreateBackupDialog
        open={open}
        onClose={() => setOpen(false)}
        onCreated={(r) => {
          setCreated(r)
          setOpen(false)
          void qc.invalidateQueries({ queryKey: backupsQuery.queryKey })
        }}
      />
    </Panel>
  )
}

function BackupTable({ items }: { items: Backup[] }) {
  return (
    <TableScroll label="Backup list">
      <table className="data-table" style={{ width: '100%' }}>
        <thead>
          <tr>
            <th>Backup</th>
            <th>Created</th>
            <th>State</th>
            <th>Contents</th>
            <th className="num">Size</th>
            <th>Server version</th>
            <th>Master key ids</th>
          </tr>
        </thead>
        <tbody>
          {items.map((b) => {
            const m = b.manifest
            const size = (m?.files ?? []).reduce((n, x) => n + x.bytes, 0)
            return (
              <tr key={b.name}>
                <td>
                  <span className="mono" title={b.path}>
                    {b.name}
                  </span>
                </td>
                <td>{m ? <span title={fmtDateTime(m.created_at, { seconds: true })}>{fmtRelative(m.created_at)}</span> : <span className="muted">—</span>}</td>
                <td>
                  {b.intact ? (
                    <Badge tone="success">Verified</Badge>
                  ) : (
                    <span>
                      <Badge tone="error">Damaged</Badge> <span className="secondary">{b.problem}</span>
                    </span>
                  )}
                </td>
                <td>
                  {m ? (
                    <span style={{ display: 'inline-flex', gap: 6, flexWrap: 'wrap' }}>
                      {(m.files ?? []).map((x) => (
                        <Badge key={x.name} title={`schema ${x.schema_version} · sha256 ${x.sha256}`}>
                          {x.name}
                        </Badge>
                      ))}
                      {m.include_traces && <Badge tone="warning">trace content</Badge>}
                    </span>
                  ) : (
                    <span className="muted">—</span>
                  )}
                </td>
                <td className="num">{m ? fmtBytes(size) : '—'}</td>
                <td className="mono">{m?.server_version || '—'}</td>
                <td className="mono">{(m?.master_key_ids ?? []).join(', ') || '—'}</td>
              </tr>
            )
          })}
        </tbody>
      </table>
      <p className="muted" style={{ fontSize: 'var(--text-sm)', marginTop: 8 }}>
        {fmtNumber(items.length)} backup{items.length === 1 ? '' : 's'} in the data directory.
      </p>
    </TableScroll>
  )
}

function CreateBackupDialog({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: (r: BackupCreated) => void }) {
  const [traces, setTraces] = useState(false)
  const create = useMutation({
    mutationFn: () => apiSend<BackupCreated>('POST', '/system/backups', { include_traces: traces }),
    onSuccess: (r) => {
      setTraces(false)
      onCreated(r)
    },
  })
  return (
    <Dialog open={open} title="Take backup" onClose={onClose}>
      <form
        style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-3)' }}
        onSubmit={(e) => {
          e.preventDefault()
          create.mutate()
        }}
      >
        <p className="secondary">
          The server keeps running. Configuration, ledger, activity, events and sealed secrets are included; secrets stay encrypted and need the
          same master key to restore.
        </p>
        <Checkbox label="Include trace content (traces.db)" checked={traces} onChange={setTraces} />
        {traces && (
          <Banner tone="warning" role="status">
            The backup will contain encrypted trace content: prompts, completions and tool data under your trace policies. Anyone holding the
            backup and the master key can read it, and expiry in the live database does not reach copies. Store it accordingly.
          </Banner>
        )}
        <MutationError error={create.error} />
        <div className={f.actions}>
          <Button type="button" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" busy={create.isPending}>
            Take backup
          </Button>
        </div>
      </form>
    </Dialog>
  )
}
