import { type ReactNode, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Pencil, Plus, Trash2 } from 'lucide-react'
import { Button, EmptyState, Panel, TableScroll } from '@/components/ui'
import { MutationError } from '@/components/form'
import { QueryError, TableSkeleton } from '@/components/states'
import { ResourceBadges } from '@/components/ResourceBadges'
import { resourceListQuery, type ResourcePlural } from '@/lib/api/queries'
import { useDeleteResource } from '@/lib/api/resources'
import type { ResourceMeta } from '@/lib/api/types'
import { useCan } from '@/lib/session'
import c from './config.module.css'

export interface Column<T> {
  header: string
  cell: (r: T) => ReactNode
}

/** Generic list panel for a managed resource kind with create/edit/delete. */
export function ResourceList<T extends ResourceMeta>({ plural, title, subtitle, noun, columns, dialog, empty, writePerm = 'config.write', nameCell, extra }: {
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
}) {
  const list = useQuery(resourceListQuery<T>(plural))
  const canWrite = useCan(writePerm)
  const canDelete = useCan('config.write')
  const [editing, setEditing] = useState<T | 'new' | null>(null)
  const del = useDeleteResource(plural)
  const items = list.data?.items ?? []

  return (
    <Panel
      title={title}
      subtitle={subtitle}
      actions={canWrite && <Button variant="primary" icon={Plus} onClick={() => setEditing('new')}>Add {noun}</Button>}
    >
      {extra}
      <MutationError error={del.error} />
      {list.isError ? (
        <QueryError error={list.error} retry={() => void list.refetch()} />
      ) : !list.data ? (
        <TableSkeleton />
      ) : items.length === 0 ? (
        <EmptyState title={`No ${noun}s yet`}>{empty}</EmptyState>
      ) : (
        <TableScroll label={title}>
          <table className="data-table" style={{ width: '100%' }}>
            <thead>
              <tr>
                <th>Name</th>
                {columns.map((col) => (
                  <th key={col.header}>{col.header}</th>
                ))}
                <th>Managed by</th>
                <th aria-label="Actions" />
              </tr>
            </thead>
            <tbody>
              {items.map((r) => (
                <tr key={r.id}>
                  <td>{nameCell ? nameCell(r) : <span className="mono">{r.name}</span>}</td>
                  {columns.map((col) => (
                    <td key={col.header}>{col.cell(r)}</td>
                  ))}
                  <td>
                    <ResourceBadges r={r} />
                  </td>
                  <td className={c.cellActions}>
                    {canWrite && <Button size="small" variant="ghost" icon={Pencil} aria-label={`Edit ${r.name}`} onClick={() => setEditing(r)} />}
                    {canDelete && (
                      <Button
                        size="small"
                        variant="ghost"
                        icon={Trash2}
                        aria-label={`Delete ${r.name}`}
                        onClick={() => window.confirm(`Delete ${noun} ${r.name}? This cannot be undone.`) && del.mutate({ id: r.id, revision: r.revision })}
                      />
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableScroll>
      )}
      {editing && dialog(editing === 'new' ? null : editing, () => setEditing(null))}
    </Panel>
  )
}
