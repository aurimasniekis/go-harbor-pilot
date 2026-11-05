import {
  type ButtonHTMLAttributes,
  type KeyboardEvent,
  type ReactNode,
  useId,
  useRef,
} from 'react'
import {
  AlertTriangle,
  CheckCircle2,
  CircleDashed,
  CircleSlash,
  Clock3,
  HelpCircle,
  Info,
  KeyRound,
  Loader2,
  type LucideIcon,
  OctagonAlert,
  PauseCircle,
  Search,
  XCircle,
} from 'lucide-react'
import s from './ui.module.css'
import type { Availability, Health, Tier } from '@/lib/api/types'
import { fmtPercent, isKnown } from '@/lib/format'

export type Tone = 'neutral' | 'success' | 'info' | 'warning' | 'error' | 'purple' | 'accent'

const toneClass: Record<Tone, string | undefined> = {
  neutral: s['tone-neutral'],
  success: s['tone-success'],
  info: s['tone-info'],
  warning: s['tone-warning'],
  error: s['tone-error'],
  purple: s['tone-purple'],
  accent: s['tone-accent'],
}

export function cx(...parts: (string | false | null | undefined)[]): string {
  return parts.filter(Boolean).join(' ')
}

/** Badge always pairs color with an icon and a text label. */
export function Badge({ tone = 'neutral', icon: Icon, children, title }: {
  tone?: Tone
  icon?: LucideIcon
  children: ReactNode
  title?: string
}) {
  return (
    <span className={cx(s.badge, toneClass[tone])} title={title}>
      {Icon && <Icon size={12} aria-hidden="true" />}
      {children}
    </span>
  )
}

const healthMeta: Record<Health, { tone: Tone; icon: LucideIcon; label: string }> = {
  healthy: { tone: 'success', icon: CheckCircle2, label: 'Healthy' },
  degraded: { tone: 'warning', icon: AlertTriangle, label: 'Degraded' },
  auth_failed: { tone: 'error', icon: KeyRound, label: 'Auth failed' },
  stale: { tone: 'warning', icon: Clock3, label: 'Stale' },
  error: { tone: 'error', icon: XCircle, label: 'Error' },
  disabled: { tone: 'neutral', icon: PauseCircle, label: 'Disabled' },
  pending: { tone: 'info', icon: CircleDashed, label: 'Pending' },
  unsupported: { tone: 'neutral', icon: CircleSlash, label: 'Unsupported' },
}

export function HealthBadge({ health, reason }: { health: Health; reason?: string }) {
  const m = healthMeta[health] ?? { tone: 'neutral' as Tone, icon: HelpCircle, label: health }
  return (
    <Badge tone={m.tone} icon={m.icon} title={reason || undefined}>
      {m.label}
    </Badge>
  )
}

const availMeta: Record<Availability, { tone: Tone; icon: LucideIcon; label: string }> = {
  usable: { tone: 'success', icon: CheckCircle2, label: 'Usable' },
  exhausted: { tone: 'error', icon: OctagonAlert, label: 'Exhausted' },
  model_limited: { tone: 'warning', icon: AlertTriangle, label: 'Model limited' },
  unknown: { tone: 'neutral', icon: HelpCircle, label: 'Unknown' },
}

export function AvailabilityBadge({ availability, reason }: { availability: Availability; reason?: string }) {
  const m = availMeta[availability]
  return (
    <Badge tone={m.tone} icon={m.icon} title={reason || undefined}>
      {m.label}
    </Badge>
  )
}

export function TierBadge({ tier }: { tier: Tier }) {
  switch (tier) {
    case 'demo':
      return <Badge tone="purple" icon={Info}>Demo</Badge>
    case 'unofficial':
      return (
        <Badge tone="warning" icon={AlertTriangle} title="Uses an undocumented provider endpoint; may break or conflict with provider terms.">
          Unofficial
        </Badge>
      )
    case 'client_reported':
      return <Badge tone="info" icon={Info}>Client reported</Badge>
    default:
      return <Badge tone="neutral" icon={CheckCircle2}>Official</Badge>
  }
}

type ButtonVariant = 'default' | 'primary' | 'ghost' | 'danger'

export function Button({ variant = 'default', size, busy, icon: Icon, children, className, ...rest }: ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: ButtonVariant
  size?: 'small'
  busy?: boolean
  icon?: LucideIcon
}) {
  return (
    <button
      type="button"
      {...rest}
      aria-busy={busy || undefined}
      className={cx(
        s.button,
        variant === 'primary' && s.primary,
        variant === 'ghost' && s.ghost,
        variant === 'danger' && s.danger,
        size === 'small' && s.small,
        !children && s.iconButton,
        className,
      )}
    >
      {busy ? <Loader2 size={14} className={s.spin} aria-hidden="true" /> : Icon && <Icon size={14} aria-hidden="true" />}
      {children}
    </button>
  )
}

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
    <section className={cx(s.panel, className)} aria-labelledby={title ? headingId : undefined} id={id}>
      {(title || actions) && (
        <header className={s.panelHeader}>
          <div className={s.panelTitle}>
            {title && <h2 id={headingId}>{title}</h2>}
            {subtitle && <p>{subtitle}</p>}
          </div>
          {actions && <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>{actions}</div>}
        </header>
      )}
      <div className={cx(s.panelBody, bodyClassName)}>{children}</div>
    </section>
  )
}

/**
 * Quota progress bar. `percent` is remaining percent (0–100) or null for
 * unknown; unknown renders a hatched track and the word "Unknown", never 0%.
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
          {known ? `${fmtPercent(v, 0)} left` : 'Unknown'}
          {stale && known ? ' · stale' : ''}
        </span>
      </div>
      <div
        className={s.barTrack}
        data-stale={stale ? 'true' : undefined}
        data-unknown={known ? undefined : 'true'}
        role="meter"
        aria-label={srLabel ?? (typeof label === 'string' ? label : 'Quota remaining')}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={known ? Math.round(v) : undefined}
        aria-valuetext={known ? `${v.toFixed(0)} percent remaining${stale ? ', stale' : ''}` : 'Unknown'}
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
  return <span className={s.skeleton} style={{ width, height, ...style }} aria-hidden="true" />
}

export function EmptyState({ icon: Icon = Info, title, children, action }: {
  icon?: LucideIcon
  title: string
  children?: ReactNode
  action?: ReactNode
}) {
  return (
    <div className={s.state}>
      <Icon size={28} aria-hidden="true" />
      <h3>{title}</h3>
      {children && <p>{children}</p>}
      {action}
    </div>
  )
}

export function Banner({ tone = 'info', icon: Icon = Info, children, role }: {
  tone?: 'info' | 'warning' | 'error' | 'purple'
  icon?: LucideIcon
  children: ReactNode
  role?: 'status' | 'alert'
}) {
  return (
    <div className={cx(s.banner, s[`banner-${tone}`])} role={role}>
      <Icon size={16} aria-hidden="true" />
      <div>{children}</div>
    </div>
  )
}

/** Accessible tabs/segmented control with arrow-key navigation. */
export function Tabs<T extends string>({ value, onChange, items, label, variant = 'tabs' }: {
  value: T
  onChange: (v: T) => void
  items: { value: T; label: ReactNode; count?: number; disabled?: boolean }[]
  label: string
  variant?: 'tabs' | 'segmented'
}) {
  const refs = useRef<(HTMLButtonElement | null)[]>([])
  const onKey = (e: KeyboardEvent<HTMLButtonElement>, idx: number) => {
    const enabled = items.map((it, i) => (it.disabled ? -1 : i)).filter((i) => i >= 0)
    const pos = enabled.indexOf(idx)
    let next: number | undefined
    if (e.key === 'ArrowRight') next = enabled[(pos + 1) % enabled.length]
    else if (e.key === 'ArrowLeft') next = enabled[(pos - 1 + enabled.length) % enabled.length]
    else if (e.key === 'Home') next = enabled[0]
    else if (e.key === 'End') next = enabled[enabled.length - 1]
    if (next !== undefined) {
      e.preventDefault()
      const item = items[next]
      if (item) {
        onChange(item.value)
        refs.current[next]?.focus()
      }
    }
  }
  return (
    <div role="tablist" aria-label={label} className={variant === 'tabs' ? s.tabs : s.segmented}>
      {items.map((it, i) => {
        const selected = it.value === value
        return (
          <button
            key={it.value}
            ref={(el) => {
              refs.current[i] = el
            }}
            type="button"
            role="tab"
            aria-selected={selected}
            tabIndex={selected ? 0 : -1}
            disabled={it.disabled}
            className={variant === 'tabs' ? s.tab : s.segment}
            onClick={() => onChange(it.value)}
            onKeyDown={(e) => onKey(e, i)}
          >
            {it.label}
            {it.count !== undefined && <span className={s.tabCount}>{it.count}</span>}
          </button>
        )
      })}
    </div>
  )
}

export function SearchInput({ value, onChange, placeholder, label }: {
  value: string
  onChange: (v: string) => void
  placeholder?: string
  label: string
}) {
  return (
    <label className={s.searchWrap}>
      <span className="visually-hidden">{label}</span>
      <Search size={14} aria-hidden="true" />
      <input
        type="search"
        className={s.input}
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
      />
    </label>
  )
}

export function InfoTip({ text }: { text: string }) {
  return (
    <span className={s.tooltipTrigger} title={text} aria-label={text} role="img">
      <HelpCircle size={13} aria-hidden="true" />
    </span>
  )
}

export function KeyValues({ items }: { items: [ReactNode, ReactNode][] }) {
  return (
    <dl className={s.kv}>
      {items.map(([k, v], i) => (
        <div key={i} style={{ display: 'contents' }}>
          <dt>{k}</dt>
          <dd>{v}</dd>
        </div>
      ))}
    </dl>
  )
}

export const uiStyles = s
