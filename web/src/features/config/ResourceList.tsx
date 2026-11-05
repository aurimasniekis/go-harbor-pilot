import { type ReactNode, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ActionIcon, Group, Menu, Stack, Switch, Text } from '@mantine/core'
import { AlertTriangle, MoreHorizontal, Pencil, Plus, Trash2 } from 'lucide-react'
import { Button, EmptyState } from '@/components/ui'
import { ConfirmDialog, MutationError } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { ResourceBadges } from '@/components/ResourceBadges'
import { resourceListQuery, type ResourcePlural } from '@/lib/api/queries'
import { useDeleteResource, useSaveResource } from '@/lib/api/resources'
import type { ResourceMeta } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import { type Column as KitColumn, DataTable, Section } from '@/ui/kit'

export interface Column<T> {
  header: string
  cell: (r: T) => ReactNode
  /** Placement in the phone card (see DataTable); defaults to the meta line. */
  mobile?: KitColumn<T>['mobile']
  align?: 'left' | 'right'
}

/** Name cell: display name over a muted slug (or just the slug). */
export function NameCell({ name, display, extra }: { name: string; display?: string; extra?: ReactNode }) {
  return (
    <Stack gap={0} miw={0}>
      <Group gap={6} wrap="nowrap" miw={0}>
        <Text size="sm" fw={500} truncate>
          {display || name}
        </Text>
        {extra}
      </Group>
      {display && display !== name && (
        <Text size="xs" c="dimmed" ff="monospace" truncate>
          {name}
        </Text>
      )}
    </Stack>
  )
}

/** Row actions in a "⋯" menu so they stay reachable on phones. */
export function RowMenu({ name, onEdit, onDelete, extra }: { name: string; onEdit?: () => void; onDelete?: () => void; extra?: ReactNode }) {
  if (!onEdit && !onDelete && !extra) return null
  return (
    <Menu position="bottom-end" withinPortal>
      <Menu.Target>
        <ActionIcon variant="subtle" color="gray" aria-label={`Actions for ${name}`} onClick={(e) => e.stopPropagation()}>
          <MoreHorizontal size={16} />
        </ActionIcon>
      </Menu.Target>
      <Menu.Dropdown onClick={(e) => e.stopPropagation()}>
        {onEdit && (
          <Menu.Item leftSection={<Pencil size={14} />} onClick={onEdit} aria-label={`Edit ${name}`}>
            Edit
          </Menu.Item>
        )}
        {extra}
        {onDelete && (
          <Menu.Item color="red" leftSection={<Trash2 size={14} />} onClick={onDelete} aria-label={`Delete ${name}`}>
            Delete
          </Menu.Item>
        )}
      </Menu.Dropdown>
    </Menu>
  )
}

/** Generic list of a managed resource kind with create, edit and delete. */
export function ResourceList<T extends ResourceMeta>({ plural, title, subtitle, noun, columns, dialog, empty, writePerm = 'config.write', nameCell, extra, onRow }: {
  plural: ResourcePlural
  title: string
  subtitle?: ReactNode
  noun: string
  columns: Column<T>[]
  dialog: (r: T | null, close: () => void) => ReactNode
  empty: ReactNode
  writePerm?: string
  nameCell?: (r: T) => ReactNode
  extra?: ReactNode
  /** Row click; defaults to editing when the user can write. */
  onRow?: (r: T) => void
}) {
  const list = useQuery(resourceListQuery<T>(plural))
  const canWrite = useCan(writePerm)
  const canDelete = useCan('config.write')
  const [editing, setEditing] = useState<T | 'new' | null>(null)
  const [deleting, setDeleting] = useState<T | null>(null)
  const del = useDeleteResource(plural)
  const items = list.data?.items ?? []

  const cols: KitColumn<T>[] = [
    {
      id: 'name',
      header: 'Name',
      mobile: 'title',
      cell: (r) => (
        <Group gap={6} wrap="nowrap" miw={0}>
          {nameCell ? nameCell(r) : <NameCell name={r.name} />}
          <ResourceBadges r={r} />
        </Group>
      ),
    },
    ...columns.map((c) => ({ id: c.header, header: c.header, cell: c.cell, mobile: c.mobile, align: c.align })),
    {
      id: 'actions',
      header: <span className="visually-hidden">Actions</span>,
      align: 'right',
      width: 48,
      mobile: 'aside',
      cell: (r) => (
        <RowMenu
          name={r.name}
          onEdit={canWrite ? () => setEditing(r) : undefined}
          onDelete={canDelete ? () => setDeleting(r) : undefined}
        />
      ),
    },
  ]

  return (
    <Section
      title={title}
      info={subtitle}
      actions={canWrite && <Button variant="primary" icon={Plus} onClick={() => setEditing('new')}>Add {noun}</Button>}
    >
      {extra}
      <MutationError error={del.error} />
      {list.isError ? (
        <QueryError error={list.error} retry={() => void list.refetch()} />
      ) : !list.data ? (
        <TableSkeleton />
      ) : items.length === 0 ? (
        <EmptyState title={`No ${noun}s yet`} action={canWrite && <Button variant="primary" icon={Plus} onClick={() => setEditing('new')}>Add {noun}</Button>}>
          {empty}
        </EmptyState>
      ) : (
        <DataTable
          label={`${title} list`}
          rows={items}
          columns={cols}
          rowKey={(r) => r.id}
          onRow={onRow ?? (canWrite ? (r) => setEditing(r) : undefined)}
        />
      )}
      {editing && dialog(editing === 'new' ? null : editing, () => setEditing(null))}
      <ConfirmDialog
        open={!!deleting}
        title={`Delete ${noun} ${deleting?.name ?? ''}?`}
        message="This cannot be undone."
        confirmLabel="Delete"
        onCancel={() => setDeleting(null)}
        onConfirm={() => {
          if (deleting) del.mutate({ id: deleting.id, revision: deleting.revision })
          setDeleting(null)
        }}
      />
    </Section>
  )
}

/**
 * On/off state of a resource with an inline switch. "Off" reads as a
 * warning ("Off — not sending") so a silent channel or rule is noticed.
 */
export function EnabledSwitch<S extends { enabled: boolean }>({ plural, r, offLabel = 'Off — not sending' }: {
  plural: ResourcePlural
  r: ResourceMeta & { spec: S }
  offLabel?: string
}) {
  const canWrite = useCan('config.write')
  const save = useSaveResource<S>(plural)
  return (
    <Group gap={8} wrap="nowrap" onClick={(e) => e.stopPropagation()}>
      {canWrite && (
        <Switch
          size="sm"
          aria-label={`${r.spec.enabled ? 'Turn off' : 'Turn on'} ${r.name}`}
          checked={r.spec.enabled}
          disabled={save.isPending}
          onChange={(e) => save.mutate({ existing: r, body: { name: r.name, spec: { ...r.spec, enabled: e.currentTarget.checked } } })}
        />
      )}
      {r.spec.enabled ? (
        <Text size="xs" c="dimmed">
          On
        </Text>
      ) : (
        <Group gap={4} wrap="nowrap" c="var(--warning)">
          <AlertTriangle size={12} aria-hidden="true" />
          <Text size="xs" c="inherit" fw={500}>
            {offLabel}
          </Text>
        </Group>
      )}
    </Group>
  )
}
