import { type ComponentPropsWithoutRef, type ReactNode, useId } from 'react'
import {
  ActionIcon,
  Alert,
  Badge as MBadge,
  Button as MButton,
  Group,
  Paper,
  Skeleton as MSkeleton,
  Stack,
  Tabs as MTabs,
  Text,
  TextInput,
  Title,
} from '@mantine/core'
import { AlertTriangle, Info, type LucideIcon, Search } from 'lucide-react'
import s from './ui.module.css'
import type { Tier } from '@/lib/api/types'
import { fmtPercent, isKnown } from '@/lib/format'
import { Empty, InfoTip as KitInfoTip, Fields } from '@/ui/kit'

/*
 * Shared primitives on Mantine. The exported API predates the Mantine
 * rebuild; screens keep using it while each page is restructured.
 */

export type Tone = 'neutral' | 'success' | 'info' | 'warning' | 'error' | 'purple' | 'accent'

/** Mantine palette name for a tone (see theme/theme.ts). */
export const toneColor: Record<Tone, string> = {
  neutral: 'gray',
  success: 'green',
  info: 'teal',
  warning: 'yellow',
  error: 'red',
  purple: 'violet',
  accent: 'blue',
}

export function cx(...parts: (string | false | null | undefined)[]): string {
  return parts.filter(Boolean).join(' ')
}

/** Badge pairs color with a text label (and optionally an icon). */
export function Badge({ tone = 'neutral', icon: Icon, children, title }: {
  tone?: Tone
  icon?: LucideIcon
  children: ReactNode
  title?: string
}) {
  return (
    <MBadge color={toneColor[tone]} title={title} size="sm" leftSection={Icon ? <Icon size={12} aria-hidden="true" /> : undefined} className={s.badge}>
      {children}
    </MBadge>
  )
}

export function TierBadge({ tier }: { tier: Tier }) {
  switch (tier) {
    case 'demo':
      return null
    case 'unofficial':
      return (
        <Badge tone="warning" icon={AlertTriangle} title="Uses an undocumented provider endpoint; may break or conflict with provider terms.">
          Unofficial
        </Badge>
      )
    case 'client_reported':
      return <Badge tone="info" icon={Info}>Reported by the tool</Badge>
    default:
      return null
  }
}

type ButtonVariant = 'default' | 'primary' | 'ghost' | 'danger'

const buttonVariant: Record<ButtonVariant, { variant: string; color?: string }> = {
  default: { variant: 'default' },
  primary: { variant: 'filled' },
  ghost: { variant: 'subtle', color: 'gray' },
  danger: { variant: 'light', color: 'red' },
}

/** Button; an icon without text renders an icon button (give it an aria-label). */
export function Button({ variant = 'default', size, busy, icon: Icon, children, className, type = 'button', ...rest }: Omit<ComponentPropsWithoutRef<'button'>, 'color'> & {
  variant?: ButtonVariant
  size?: 'small'
  busy?: boolean
  icon?: LucideIcon
}) {
  const v = buttonVariant[variant]
  if (!children && Icon) {
    return (
      <ActionIcon
        type={type}
        variant={variant === 'default' ? 'default' : v.variant}
        color={v.color}
        size={size === 'small' ? 'md' : 'lg'}
        loading={busy}
        className={className}
        {...rest}
      >
        <Icon size={size === 'small' ? 14 : 16} aria-hidden="true" />
      </ActionIcon>
    )
  }
  return (
    <MButton
      type={type}
      variant={v.variant}
      color={v.color}
      size={size === 'small' ? 'compact-sm' : 'sm'}
      loading={busy}
      leftSection={Icon ? <Icon size={14} aria-hidden="true" /> : undefined}
      className={className}
      {...rest}
    >
      {children}
    </MButton>
  )
}

/**
 * A titled block. The subtitle is help text: it goes into an info tooltip
 * next to the title instead of a paragraph.
 */
export function Panel({ title, subtitle, actions, children, className, bodyClassName, id }: {
  title?: ReactNode
  subtitle?: ReactNode
  actions?: ReactNode
  children: ReactNode
  className?: string
  bodyClassName?: string
  id?: string
}) {
  const headingId = useId()
  return (
    <Paper component="section" withBorder radius="md" className={cx(s.panel, className)} aria-labelledby={title ? headingId : undefined} id={id}>
      {(title || actions) && (
        <Group justify="space-between" gap="sm" wrap="wrap" className={s.panelHeader}>
          <Group gap={6} wrap="nowrap" miw={0}>
            {title && (
              <Title order={2} id={headingId}>
                {title}
              </Title>
            )}
            {subtitle && <KitInfoTip>{subtitle}</KitInfoTip>}
          </Group>
          {actions && <Group gap="xs">{actions}</Group>}
        </Group>
      )}
      <Stack gap="md" className={cx(s.panelBody, bodyClassName)}>
        {children}
      </Stack>
    </Paper>
  )
}

/**
 * Quota progress bar. `percent` is remaining percent (0–100) or null for
 * unknown; unknown renders a hatched track and the word "Unknown", never 0%.
 */
/**
 * Percent of a quota window still available, the one convention for every
 * quota bar (Home, Limits, account drawers): prefer the remaining fraction,
 * else derive it from used and limit. null when unknown, never 0.
 */
export function remainingPercent(b: { remaining_fraction?: number | null; used?: number | null; limit?: number | null; remaining?: number | null }): number | null {
  if (b.remaining_fraction !== null && b.remaining_fraction !== undefined) return b.remaining_fraction * 100
  if (b.limit && b.limit > 0) {
    if (b.remaining !== null && b.remaining !== undefined) return (b.remaining / b.limit) * 100
    if (b.used !== null && b.used !== undefined) return ((b.limit - b.used) / b.limit) * 100
  }
  return null
}

/**
 * A quota window bar. `percent` is what is LEFT (see remainingPercent); the
 * fill shrinks and turns amber then red as the window runs out.
 */
export function QuotaBar({ label, percent, stale, meta, srLabel }: {
  label: ReactNode
  percent: number | null | undefined
  stale?: boolean
  meta?: ReactNode
  srLabel?: string
}) {
  const known = isKnown(percent)
  const v = known ? Math.max(0, Math.min(100, percent)) : 0
  const level = !known ? undefined : v <= 0 ? 'empty' : v < 20 ? 'low' : v >= 60 ? 'ok' : 'mid'
  return (
    <div className={s.bar}>
      <div className={s.barLabel}>
        <span>{label}</span>
        <span className={s.barValue}>
          {known ? (v <= 0 ? 'Used up' : `${fmtPercent(v, 0)} left`) : 'Unknown'}
          {stale && known ? ' · stale' : ''}
        </span>
      </div>
      <div
        className={s.barTrack}
        data-stale={stale ? 'true' : undefined}
        data-unknown={known ? undefined : 'true'}
        {...(known
          ? {
              role: 'meter',
              'aria-label': srLabel ?? (typeof label === 'string' ? label : 'Quota remaining'),
              'aria-valuemin': 0,
              'aria-valuemax': 100,
              'aria-valuenow': Math.round(v),
              'aria-valuetext': `${v.toFixed(0)} percent remaining${stale ? ', stale' : ''}`,
            }
          : { role: 'img', 'aria-label': `${srLabel ?? (typeof label === 'string' ? label : 'Quota remaining')}: unknown` })}
      >
        {known && <div className={s.barFill} data-level={level} style={{ width: `${v}%` }} />}
      </div>
      {meta && <div className={s.barMeta}>{meta}</div>}
    </div>
  )
}

/** Horizontally scrollable, keyboard-focusable container for wide tables. */
export function TableScroll({ label, children, style }: { label: string; children: ReactNode; style?: React.CSSProperties }) {
  return (
    <div className="table-scroll" tabIndex={0} role="region" aria-label={label} style={style}>
      {children}
    </div>
  )
}

export function Skeleton({ width = '100%', height = 14, style }: { width?: number | string; height?: number | string; style?: React.CSSProperties }) {
  return <MSkeleton width={width} height={height} style={style} radius="sm" />
}

export function EmptyState({ icon: Icon = Info, title, children, action }: {
  icon?: LucideIcon
  title: string
  children?: ReactNode
  action?: ReactNode
}) {
  return (
    <Empty icon={<Icon size={24} aria-hidden="true" />} title={title} action={action}>
      {children}
    </Empty>
  )
}

const bannerColor = { info: 'teal', success: 'green', warning: 'yellow', error: 'red', purple: 'violet' } as const

export function Banner({ tone = 'info', icon: Icon = Info, children, role }: {
  tone?: 'info' | 'success' | 'warning' | 'error' | 'purple'
  icon?: LucideIcon
  children: ReactNode
  role?: 'status' | 'alert'
}) {
  return (
    <Alert color={bannerColor[tone]} variant="light" icon={<Icon size={16} aria-hidden="true" />} role={role ?? "note"} radius="md" className={s.banner}>
      <Text size="sm" component="div">
        {children}
      </Text>
    </Alert>
  )
}

/** Tabs (underlined) or a segmented pill row, with arrow-key navigation. */
export function Tabs<T extends string>({ value, onChange, items, label, variant = 'tabs' }: {
  value: T
  onChange: (v: T) => void
  items: { value: T; label: ReactNode; count?: number; disabled?: boolean }[]
  label: string
  variant?: 'tabs' | 'segmented'
}) {
  // Mantine tabs need a non-empty value; "" (often "all") travels as a sentinel.
  const enc = (v: string) => (v === '' ? '\u0000all' : v)
  return (
    <MTabs
      value={enc(value)}
      onChange={(v) => v !== null && onChange((v === '\u0000all' ? '' : v) as T)}
      variant={variant === 'tabs' ? 'default' : 'pills'}
      radius="md"
      activateTabWithKeyboard
      classNames={variant === 'segmented' ? { tab: s.segmentTab } : undefined}
    >
      <MTabs.List aria-label={label} className={variant === 'segmented' ? s.segmented : undefined}>
        {items.map((it) => (
          <MTabs.Tab
            key={it.value}
            value={enc(it.value)}
            disabled={it.disabled}
            rightSection={it.count !== undefined ? <span className={s.tabCount}>{it.count}</span> : undefined}
          >
            {it.label}
          </MTabs.Tab>
        ))}
      </MTabs.List>
    </MTabs>
  )
}

export function SearchInput({ value, onChange, placeholder, label }: {
  value: string
  onChange: (v: string) => void
  placeholder?: string
  label: string
}) {
  return (
    <TextInput
      type="search"
      aria-label={label}
      leftSection={<Search size={14} aria-hidden="true" />}
      value={value}
      placeholder={placeholder}
      onChange={(e) => onChange(e.currentTarget.value)}
      w={260}
      maw="100%"
    />
  )
}

export function InfoTip({ text }: { text: string }) {
  return <KitInfoTip>{text}</KitInfoTip>
}

/** Label/value pairs, stacked on phones. */
export function KeyValues({ items }: { items: [ReactNode, ReactNode][] }) {
  return <Fields items={items} cols={1} />
}

export const uiStyles = s
