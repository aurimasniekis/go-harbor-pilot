import { useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, Archive, CheckCircle2, Copy, Download, MoreHorizontal, Plus, Trash2 } from 'lucide-react'
import { ActionIcon, Button as MButton, Group, Menu, Stack, Text, Tooltip } from '@mantine/core'
import { useClipboard } from '@mantine/hooks'
import { Badge, Banner, Button, EmptyState, Panel, Skeleton } from '@/components/ui'
import { type Column, CopyText, DataTable, Fields } from '@/ui/kit'
import { plural } from '@/ui/glossary'
import { Advanced, Checkbox, ConfirmDialog, Dialog, MutationError, formStyles as f } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { API, apiSend } from '@/lib/api/client'
import { backupsQuery, metaQuery, systemStatusQuery } from '@/lib/api/queries'
import type { Backup, BackupCreated } from '@/lib/api/types'
import { fmtDateTime, fmtRelative } from '@/lib/format'
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

/** "5 Nov 2026, 18:25" from a manifest time or a 20261105T182553.407Z name. */
function backupWhen(b: Backup): string {
  if (b.manifest?.created_at) return fmtDateTime(b.manifest.created_at)
  const m = /^(\d{4})(\d{2})(\d{2})T(\d{2})(\d{2})(\d{2})/.exec(b.name)
  return m ? fmtDateTime(`${m[1]}-${m[2]}-${m[3]}T${m[4]}:${m[5]}:${m[6]}Z`) : b.name
}

function SystemSettings() {
  const meta = useQuery(metaQuery)
  const status = useQuery(systemStatusQuery)
  return (
    <>
      {status.data && !status.data.secure_cookies && (
        <Banner tone="warning" icon={AlertTriangle}>
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
          <Stack gap="sm">
            <Fields
              cols={3}
              items={[
                ['Version', <CopyText value={meta.data.version} maw="100%" />],
                ['Public URL', meta.data.public_url ? <CopyText value={meta.data.public_url} maw="100%" /> : <span className="muted">Not set</span>],
                ['Started', <span title={fmtDateTime(meta.data.started_at, { seconds: true })}>{fmtRelative(meta.data.started_at)}</span>],
                ['Data directory', <CopyText value={status.data.data_dir} maw="100%" />],
                ['Master key', <CopyText value={status.data.master_key_id} maw="100%" />],
                ['Session cookies', status.data.secure_cookies ? 'Secure (https)' : 'Not secure (plain http)'],
              ]}
            />
            <Advanced label="Database schema versions">
              <Fields
                cols={2}
                items={[
                  ['Main database', <span className="mono">{status.data.schema_version}</span>],
                  ['Captured content database', <span className="mono">{status.data.traces_schema_version}</span>],
                ]}
              />
            </Advanced>
          </Stack>
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
  const [deleted, setDeleted] = useState<string | null>(null)
  const items = list.data?.items ?? []
  return (
    <Panel
      title="Backups"
      subtitle="Consistent snapshots with checksums, taken while Harbor Pilot runs and kept in the data directory. Download them to keep a copy off the server; keep the master key separately."
      actions={
        <Button variant="primary" icon={Plus} onClick={() => setOpen(true)}>
          Take backup
        </Button>
      }
    >
      {created && (
        <Banner tone="success" icon={CheckCircle2} role="status">
          Backup taken{created.manifest.include_traces ? ', including captured content' : ''}. Download it from the list below to keep a copy off the server.
        </Banner>
      )}
      {deleted && (
        <Banner tone="success" icon={CheckCircle2} role="status">
          Deleted the backup from {deleted}.
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
        <BackupTable
          items={items}
          onDeleted={(b) => {
            setCreated(null)
            setDeleted(backupWhen(b))
            void qc.invalidateQueries({ queryKey: backupsQuery.queryKey })
          }}
        />
      )}
      <Stack gap={6}>
        <Text size="sm" fw={600}>
          How to restore
        </Text>
        <Text size="sm" c="dimmed">
          Restore runs on the server while Harbor Pilot is stopped. It checks the backup and the master key, and keeps the current files aside
          (<code>*.pre-restore-&lt;time&gt;</code>) before installing it. Unpack a downloaded archive into a directory first.
        </Text>
        <CodeBlock code={'harbor-pilot backup verify DIR\nharbor-pilot restore --from DIR --yes'} label="Restore commands" />
      </Stack>
      <CreateBackupDialog
        open={open}
        onClose={() => setOpen(false)}
        onCreated={(r) => {
          setDeleted(null)
          setCreated(r)
          setOpen(false)
          void qc.invalidateQueries({ queryKey: backupsQuery.queryKey })
        }}
      />
    </Panel>
  )
}

function BackupTable({ items, onDeleted }: { items: Backup[]; onDeleted: (b: Backup) => void }) {
  const [confirm, setConfirm] = useState<Backup | null>(null)
  const clip = useClipboard({ timeout: 1500 })
  const del = useMutation({
    mutationFn: (b: Backup) => apiSend<void>('DELETE', `/system/backups/${encodeURIComponent(b.name)}`).then(() => b),
    onSuccess: onDeleted,
  })
  const cols: Column<Backup>[] = [
    {
      id: 'when',
      header: 'Taken',
      mobile: 'title',
      cell: (b) => (
        <Stack gap={0}>
          <Text size="sm" fw={500}>
            {backupWhen(b)}
          </Text>
          {b.manifest && (
            <Text size="xs" c="dimmed">
              {fmtRelative(b.manifest.created_at)}
            </Text>
          )}
        </Stack>
      ),
    },
    {
      id: 'includes',
      header: 'Includes',
      mobile: 'meta',
      mobileLabel: 'Includes',
      cell: (b) =>
        !b.manifest ? (
          <span className="muted">—</span>
        ) : b.manifest.include_traces ? (
          <Group gap={6} wrap="nowrap">
            Configuration and data <Badge tone="warning">+ captured content</Badge>
          </Group>
        ) : (
          'Configuration and data'
        ),
    },
    { id: 'size', header: 'Size', align: 'right', mobile: 'meta', mobileLabel: 'Size', cell: (b) => (b.manifest ? fmtBytes((b.manifest.files ?? []).reduce((n, x) => n + x.bytes, 0)) : '—') },
    {
      id: 'status',
      header: 'Status',
      mobile: 'aside',
      cell: (b) =>
        b.intact ? (
          <Badge tone="success">Verified</Badge>
        ) : (
          <Tooltip label={b.problem ?? 'The checksums do not match.'} multiline maw={320}>
            <span>
              <Badge tone="error">Damaged</Badge>
            </span>
          </Tooltip>
        ),
    },
    {
      id: 'actions',
      header: <span className="visually-hidden">Actions</span>,
      align: 'right',
      mobile: 'actions',
      cell: (b) => (
        <Group gap={4} wrap="nowrap" justify="flex-end">
          <MButton component="a" href={`${API}/system/backups/${encodeURIComponent(b.name)}/archive`} download variant="default" size="compact-sm"
            leftSection={<Download size={14} aria-hidden="true" />} aria-label={`Download backup from ${backupWhen(b)}`}>
            Download
          </MButton>
          <Menu position="bottom-end" withinPortal>
            <Menu.Target>
              <ActionIcon variant="subtle" color="gray" aria-label={`More actions for backup from ${backupWhen(b)}`}>
                <MoreHorizontal size={16} />
              </ActionIcon>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Item leftSection={<Copy size={14} />} onClick={() => clip.copy(b.path)}>
                {clip.copied ? 'Copied' : 'Copy path on the server'}
              </Menu.Item>
              <Menu.Item leftSection={<Trash2 size={14} />} color="red" onClick={() => { del.reset(); setConfirm(b) }}>
                Delete…
              </Menu.Item>
            </Menu.Dropdown>
          </Menu>
        </Group>
      ),
    },
  ]
  return (
    <>
      <MutationError error={del.error} />
      <DataTable label="Backup list" rows={items} columns={cols} rowKey={(b) => b.name} />
      <Text size="xs" c="dimmed">
        {plural(items.length, 'backup')} in the data directory. Server version, master key ids and checksums are in each backup's manifest.
      </Text>
      <ConfirmDialog
        open={!!confirm}
        title="Delete this backup?"
        message={confirm ? `The backup from ${backupWhen(confirm)} is removed from the server. Copies you downloaded are not affected. This cannot be undone.` : ''}
        confirmLabel="Delete backup"
        danger
        onConfirm={() => {
          if (confirm) del.mutate(confirm)
          setConfirm(null)
        }}
        onCancel={() => setConfirm(null)}
      />
    </>
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
        <Checkbox label="Include captured content" description="Prompts and completions kept by your content capture rules (traces.db)." checked={traces} onChange={setTraces} />
        {traces && (
          <Banner tone="warning" icon={AlertTriangle} role="status">
            The backup will contain encrypted captured content: prompts, completions and tool data kept by your content capture rules. Anyone holding the
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
