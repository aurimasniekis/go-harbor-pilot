import { type ReactNode, useId } from 'react'
import { Link, useRouterState } from '@tanstack/react-router'
import {
  ActionIcon,
  Box,
  Group,
  Paper,
  SimpleGrid,
  Skeleton,
  Stack,
  Table,
  Text,
  Title,
  Tooltip,
  UnstyledButton,
} from '@mantine/core'
import { useClipboard, useMediaQuery } from '@mantine/hooks'
import { Check, Info } from 'lucide-react'
import s from './kit.module.css'

/** True below the tablet breakpoint (48em): lists render as cards. */
export function useCompact(): boolean {
  return useMediaQuery('(max-width: 48em)', false, { getInitialValueInEffect: false }) ?? false
}

/* ---------- page structure ---------- */

/**
 * The one header row of a page: title, an optional short context line, and
 * actions (time range, primary button) on the right.
 */
export interface Crumb {
  label?: ReactNode
  to?: string
  params?: Record<string, string>
}

/**
 * Breadcrumb trail. Items without a label (an object still loading) are
 * left out, so the trail never ends in a bare separator.
 */
export function Breadcrumbs({ items }: { items: Crumb[] }) {
  const shown = items.filter((c) => c.label !== undefined && c.label !== null && c.label !== '')
  if (shown.length === 0) return null
  return (
    <nav aria-label="Breadcrumb" className={s.crumbs}>
      <ol>
        {shown.map((c, i) => {
          const last = i === shown.length - 1
          return (
            <li key={i}>
              {c.to && !last ? (
                <Link to={c.to} params={c.params as never}>
                  {c.label}
                </Link>
              ) : (
                <span aria-current={last ? 'page' : undefined}>{c.label}</span>
              )}
            </li>
          )
        })}
      </ol>
    </nav>
  )
}

export function PageHeader({ title, context, actions, info, breadcrumb, crumbs }: {
  title: ReactNode
  context?: ReactNode
  actions?: ReactNode
  /** Help text shown in a tooltip beside the title. */
  info?: ReactNode
  /** Small line above the title (prefer `crumbs`). */
  breadcrumb?: ReactNode
  /** Breadcrumb trail above the title; the current page is usually omitted (it is the title). */
  crumbs?: Crumb[]
}) {
  return (
    <div className={s.pageHeader}>
      <div className={s.pageTitle}>
        {crumbs && <Breadcrumbs items={crumbs} />}
        {breadcrumb && !crumbs && (
          <Text size="sm" c="dimmed" className={s.context}>
            {breadcrumb}
          </Text>
        )}
        <Group gap={6} wrap="nowrap" miw={0}>
          <Title order={1}>{title}</Title>
          {info && <InfoTip>{info}</InfoTip>}
        </Group>
        {context && (
          <Text size="sm" c="dimmed" className={s.context}>
            {context}
          </Text>
        )}
      </div>
      {actions && <div className={s.pageActions}>{actions}</div>}
    </div>
  )
}

/** Route tabs under a page header. Each tab is a link (deep-linkable). */
export function LinkTabs({ label, items }: { label: string; items: { to: string; label: string; exact?: boolean; also?: (path: string) => boolean }[] }) {
  const path = useRouterState({ select: (r) => r.location.pathname.replace(/\/$/, '') || '/' })
  return (
    <nav aria-label={label} className={s.tabs}>
      {items.map((it) => {
        const active = it.also?.(path) || (it.exact ? path === it.to : path === it.to || path.startsWith(`${it.to}/`))
        return (
          <Link key={it.to} to={it.to} className={s.tab} data-active={active || undefined} aria-current={active ? 'page' : undefined}>
            {it.label}
          </Link>
        )
      })}
    </nav>
  )
}

/** A titled block of a page. Help text lives in the info tooltip. */
export function Section({
  title,
  info,
  actions,
  children,
  id,
}: {
  title: ReactNode
  info?: ReactNode
  actions?: ReactNode
  children: ReactNode
  id?: string
}) {
  const auto = useId()
  const headingId = id ? `${id}-title` : auto
  return (
    <section className={s.section} aria-labelledby={headingId} id={id}>
      <Group justify="space-between" gap="sm" wrap="wrap" className={s.sectionHead}>
        <Group gap={6} wrap="nowrap">
          <Title order={2} id={headingId}>
            {title}
          </Title>
          {info && <InfoTip>{info}</InfoTip>}
        </Group>
        {actions && <Group gap="xs">{actions}</Group>}
      </Group>
      {children}
    </section>
  )
}

/** A small info icon whose tooltip carries the explanation. */
export function InfoTip({ children, label = 'More information' }: { children: ReactNode; label?: string }) {
  return (
    <Tooltip label={children} events={{ hover: true, focus: true, touch: true }}>
      <ActionIcon variant="transparent" color="gray" size="xs" aria-label={label} className={s.infoTip}>
        <Info size={14} />
      </ActionIcon>
    </Tooltip>
  )
}

/* ---------- headline numbers ---------- */

export interface StatProps {
  label: ReactNode
  value: ReactNode
  /** Small line under the value: a delta, a unit or a qualifier. */
  hint?: ReactNode
  tone?: 'default' | 'success' | 'warning' | 'error'
  spark?: number[]
  info?: ReactNode
  to?: string
}

export function Stat({ label, value, hint, tone = 'default', spark, info, to }: StatProps) {
  const body = (
    <>
      <Group gap={4} wrap="nowrap" className={s.statLabel}>
        <Text size="xs" c="dimmed" fw={500} truncate>
          {label}
        </Text>
        {info && <InfoTip>{info}</InfoTip>}
      </Group>
      <div className={s.statValue} data-tone={tone}>
        {value}
      </div>
      <Group justify="space-between" align="flex-end" gap="xs" wrap="nowrap" mih={20}>
        <Text size="xs" c="dimmed" truncate>
          {hint}
        </Text>
        {spark && spark.filter((v) => v > 0).length > 1 && <Sparkline values={spark} />}
      </Group>
    </>
  )
  if (to) {
    return (
      <Paper component={Link} to={to} withBorder p="sm" radius="md" className={`${s.stat} ${s.statLink}`}>
        {body}
      </Paper>
    )
  }
  return (
    <Paper withBorder p="sm" radius="md" className={s.stat}>
      {body}
    </Paper>
  )
}

export function StatGrid({ children, cols = 5, label }: { children: ReactNode; cols?: number; label?: string }) {
  return (
    <SimpleGrid cols={{ base: 2, sm: 3, md: cols }} spacing="sm" aria-label={label} role={label ? 'group' : undefined} className={s.statGrid}>
      {children}
    </SimpleGrid>
  )
}

export function StatSkeleton({ count = 5 }: { count?: number }) {
  return (
    <StatGrid cols={count}>
      {Array.from({ length: count }, (_, i) => (
        <Skeleton key={i} height={88} radius="md" />
      ))}
    </StatGrid>
  )
}

/** Inline SVG trend line (no axes); decorative. */
export function Sparkline({ values, width = 72, height = 22 }: { values: number[]; width?: number; height?: number }) {
  const max = Math.max(...values, 0)
  const min = Math.min(...values, 0)
  const span = max - min || 1
  const pts = values.map((v, i) => `${(i / (values.length - 1)) * width},${height - 2 - ((v - min) / span) * (height - 4)}`).join(' ')
  return (
    <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`} aria-hidden="true" className={s.spark}>
      <polyline points={pts} fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" strokeLinecap="round" />
    </svg>
  )
}

/* ---------- values ---------- */

const statusColor: Record<string, string> = {
  ok: 'var(--success)',
  healthy: 'var(--success)',
  error: 'var(--error)',
  auth_failed: 'var(--error)',
  blocked: 'var(--warning)',
  warning: 'var(--warning)',
  degraded: 'var(--warning)',
  stale: 'var(--warning)',
  exceeded: 'var(--error)',
  canceled: 'var(--text-muted)',
  disabled: 'var(--text-muted)',
  in_progress: 'var(--info)',
  pending: 'var(--info)',
}

/** Colored dot plus label, e.g. for request status. */
export function StatusDot({ status, label }: { status: string; label?: ReactNode }) {
  return (
    <span className={s.status}>
      <span className={s.dot} style={{ background: statusColor[status] ?? 'var(--text-muted)' }} aria-hidden="true" />
      <span>{label ?? status.replaceAll('_', ' ')}</span>
    </span>
  )
}

/** A value that is not known. Renders a muted dash with the reason. */
export function Unknown({ reason }: { reason: string }) {
  return (
    <Tooltip label={reason}>
      <span className={s.unknown} aria-label={reason} tabIndex={0}>
        —
      </span>
    </Tooltip>
  )
}

/** Single-line identifier: truncated with an ellipsis, click to copy. */
export function CopyText({ value, display, mono = true, maw }: { value: string; display?: ReactNode; mono?: boolean; maw?: number | string }) {
  const clip = useClipboard({ timeout: 1200 })
  return (
    <Tooltip label={clip.copied ? 'Copied' : `Copy ${value}`}>
      <UnstyledButton
        onClick={(e) => {
          e.stopPropagation()
          clip.copy(value)
        }}
        className={s.copy}
        data-mono={mono || undefined}
        style={{ maxWidth: maw }}
        aria-label={`Copy ${value}`}
      >
        <span className={s.copyText}>{display ?? value}</span>
        {clip.copied && <Check size={12} aria-hidden="true" />}
      </UnstyledButton>
    </Tooltip>
  )
}

/** Label-over-value pairs; two columns on wide screens, stacked on phones. */
export function Fields({ items, cols = 2 }: { items: [ReactNode, ReactNode][]; cols?: number }) {
  return (
    <dl className={s.fields} style={{ ['--cols' as string]: cols }}>
      {items.map(([k, v], i) => (
        <div key={i} className={s.field}>
          <dt>{k}</dt>
          <dd>{v}</dd>
        </div>
      ))}
    </dl>
  )
}

/* ---------- responsive table ---------- */

export interface Column<T> {
  id: string
  header: ReactNode
  cell: (row: T) => ReactNode
  align?: 'left' | 'right'
  width?: number | string
  /**
   * Where the column goes in the phone card: `title` (first line, left),
   * `aside` (first line, right), `line` (own line), `meta` (joined muted
   * line) or `hidden`.
   */
  mobile?: 'title' | 'aside' | 'line' | 'meta' | 'actions' | 'hidden'
  /**
   * Label shown before the value in a phone card's meta line. Defaults to
   * the header for right-aligned (numeric) columns; false hides it.
   */
  mobileLabel?: string | false
  /** Show the column only on phones (card) or only in the table. */
  only?: 'mobile' | 'desktop'
}

/**
 * True when a click inside a row came from something with its own action
 * (a button, link, input or a menu rendered in a portal), so the row must
 * not navigate.
 */
export function isInnerAction(e: React.MouseEvent<HTMLElement>): boolean {
  const target = e.target as Node
  if (!e.currentTarget.contains(target)) return true // from a portal (menu, popover)
  const el = target instanceof Element ? target : target.parentElement
  const hit = el?.closest('a, button, input, select, textarea, label, [role="menuitem"], [role="checkbox"], [role="switch"], [data-row-action]')
  return !!hit && hit !== e.currentTarget && e.currentTarget.contains(hit)
}

/**
 * A table on wide screens and a list of cards on phones, so nothing scrolls
 * sideways. Rows are clickable when onRow is set.
 */
export function DataTable<T>({
  rows,
  columns,
  rowKey,
  onRow,
  label,
  sticky = true,
  empty,
  fixed,
}: {
  rows: T[]
  columns: Column<T>[]
  rowKey: (row: T) => string
  onRow?: (row: T) => void
  label: string
  sticky?: boolean
  empty?: ReactNode
  /** Fixed column widths, so several tables of the same kind line up. */
  fixed?: boolean
}) {
  const compact = useCompact()
  if (rows.length === 0 && empty) return <>{empty}</>
  if (compact) {
    columns = columns.filter((c) => c.only !== 'desktop')
    const title = columns.filter((c) => c.mobile === 'title')
    const aside = columns.filter((c) => c.mobile === 'aside')
    const lines = columns.filter((c) => c.mobile === 'line')
    const meta = columns.filter((c) => c.mobile === 'meta' || c.mobile === undefined)
    const actions = columns.filter((c) => c.mobile === 'actions')
    const metaLabel = (c: Column<T>) =>
      c.mobileLabel === false ? undefined : (c.mobileLabel ?? (c.align === 'right' && typeof c.header === 'string' ? c.header : undefined))
    return (
      <div role="region" aria-label={label}>
        <ul className={s.cards}>
          {rows.map((r) => {
            const content = (
              <>
                <div className={s.cardTop}>
                  <div className={s.cardTitle}>{title.map((c) => <span key={c.id}>{c.cell(r)}</span>)}</div>
                  <div className={s.cardAside}>
                    {aside.map((c) => <span key={c.id}>{c.cell(r)}</span>)}
                    {actions.map((c) => (
                      <span key={c.id} data-row-action>
                        {c.cell(r)}
                      </span>
                    ))}
                  </div>
                </div>
                {lines.map((c) => (
                  <div key={c.id} className={s.cardLine}>
                    {c.cell(r)}
                  </div>
                ))}
                {meta.length > 0 && (
                  <div className={s.cardMeta}>
                    {meta.map((c) => {
                      const l = metaLabel(c)
                      return (
                        <span key={c.id}>
                          {l && <span className={s.metaLabel}>{l} </span>}
                          {c.cell(r)}
                        </span>
                      )
                    })}
                  </div>
                )}
              </>
            )
            return (
              <li key={rowKey(r)}>
                {onRow ? (
                  // A focusable card rather than a <button>, so row actions
                  // inside it stay valid, separate buttons.
                  <div
                    className={s.card}
                    data-clickable
                    role="button"
                    tabIndex={0}
                    onClick={(e) => {
                      if (!isInnerAction(e)) onRow(r)
                    }}
                    onKeyDown={(e) => {
                      if ((e.key === 'Enter' || e.key === ' ') && e.target === e.currentTarget) {
                        e.preventDefault()
                        onRow(r)
                      }
                    }}
                  >
                    {content}
                  </div>
                ) : (
                  <div className={s.card}>{content}</div>
                )}
              </li>
            )
          })}
        </ul>
      </div>
    )
  }
  columns = columns.filter((c) => c.only !== 'mobile')
  return (
    <div role="region" aria-label={label} className={s.tableWrap}>
      <Table stickyHeader={sticky} className={s.table} data-clickable={onRow ? true : undefined} layout={fixed ? 'fixed' : undefined}>
        <Table.Thead>
          <Table.Tr>
            {columns.map((c) => (
              <Table.Th key={c.id} style={{ textAlign: c.align ?? 'left', width: c.width }}>
                {c.header}
              </Table.Th>
            ))}
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {rows.map((r) => (
            <Table.Tr
              key={rowKey(r)}
              onClick={
                onRow
                  ? (e) => {
                      if (!isInnerAction(e)) onRow(r)
                    }
                  : undefined
              }
              tabIndex={onRow ? 0 : undefined}
              onKeyDown={
                onRow
                  ? (e) => {
                      if (e.key === 'Enter' && e.target === e.currentTarget) onRow(r)
                    }
                  : undefined
              }
            >
              {columns.map((c) => (
                <Table.Td key={c.id} style={{ textAlign: c.align ?? 'left' }} className={c.align === 'right' ? s.num : undefined}>
                  {c.cell(r)}
                </Table.Td>
              ))}
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </div>
  )
}

/** Empty list placeholder with a next step. */
export function Empty({ icon, title, children, action }: { icon?: ReactNode; title: ReactNode; children?: ReactNode; action?: ReactNode }) {
  return (
    <Stack align="center" gap={6} py="xl" className={s.empty}>
      {icon && <Box c="dimmed">{icon}</Box>}
      <Text fw={600}>{title}</Text>
      {children && (
        <Text size="sm" c="dimmed" ta="center" maw={420}>
          {children}
        </Text>
      )}
      {action}
    </Stack>
  )
}

/** Small toggle-able hook for disclosure state shared by callers. */
