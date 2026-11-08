import type { BudgetSpec, BudgetUsage } from '@/lib/api/types'

export type Period = BudgetSpec['period']

/** "Repeats" options; a one-time budget never resets (D-044). */
export const periodOptions: { value: Period; label: string }[] = [
  { value: 'day', label: 'Daily' },
  { value: 'week', label: 'Weekly (Monday start)' },
  { value: 'month', label: 'Monthly' },
  { value: 'once', label: 'One time (never resets)' },
]

export interface BudgetForm {
  scope: BudgetSpec['scope']
  scopeID: string
  period: Period
  limit: number | null
  currency: string
  kinds: string[]
  enforcement: NonNullable<BudgetSpec['enforcement']>
  /** Percent, 1–100. */
  warnAt: number | null
  tz: string
  /** RFC 3339 or "" (one-time budgets only). */
  startsAt: string
  endsAt: string
  enabled: boolean
}

/**
 * The spec a form sends. A one-time budget has no time zone; a recurring
 * one has no starts_at or ends_at.
 */
export function buildBudgetSpec(f: BudgetForm): BudgetSpec {
  const once = f.period === 'once'
  const spec: BudgetSpec = {
    scope: f.scope,
    scope_id: f.scope === 'global' ? '' : f.scopeID,
    period: f.period,
    limit: String(f.limit ?? ''),
    currency: f.currency,
    cost_kinds: f.kinds,
    enforcement: f.enforcement,
    warn_at: (f.warnAt ?? 80) / 100,
    timezone: once || f.tz === 'UTC' ? '' : f.tz,
    enabled: f.enabled,
  }
  if (once) {
    spec.starts_at = f.startsAt
    spec.ends_at = f.endsAt
  }
  return spec
}

/**
 * The window fields after the "Repeats" select changes. Switching an
 * existing recurring budget to one time starts counting now (an empty start
 * would count everything since the budget was created); switching back to a
 * saved one-time budget restores its saved window; switching to a recurring
 * period clears both.
 */
export function windowOnPeriodChange(
  next: Period,
  cur: { startsAt: string; endsAt: string },
  saved: Pick<BudgetSpec, 'period' | 'starts_at' | 'ends_at'> | undefined,
  now: Date,
): { startsAt: string; endsAt: string } {
  if (next !== 'once') return { startsAt: '', endsAt: '' }
  if (cur.startsAt || cur.endsAt || !saved) return cur
  if (saved.period === 'once') return { startsAt: saved.starts_at ?? '', endsAt: saved.ends_at ?? '' }
  return { startsAt: now.toISOString(), endsAt: '' }
}

/** The subtitle part naming how a budget repeats: "per month" or "one time". */
export function periodLabel(period: string): string {
  return period === 'once' ? 'one time' : `per ${period}`
}

export type BudgetBadge = 'blocking' | 'over' | 'near' | 'off' | 'scheduled' | 'ended' | 'ok'

/** Which status badge a budget row shows. */
export function budgetBadge(u: Pick<BudgetUsage, 'state' | 'enforcement'>): BudgetBadge {
  switch (u.state) {
    case 'exceeded':
      return u.enforcement === 'block' ? 'blocking' : 'over'
    case 'warning':
      return 'near'
    case 'disabled':
      return 'off'
    case 'scheduled':
      return 'scheduled'
    case 'expired':
      return 'ended'
  }
  return 'ok'
}

/** What the "Resets / ends" column shows. */
export type WindowEnd = { kind: 'resets' | 'ends'; iso: string } | { kind: 'never' } | { kind: 'ended' }

export function windowEnd(u: Pick<BudgetUsage, 'period' | 'period_end' | 'state'>): WindowEnd {
  if (u.period !== 'once') return { kind: 'resets', iso: u.period_end ?? '' }
  if (u.state === 'expired') return { kind: 'ended' }
  if (!u.period_end) return { kind: 'never' }
  return { kind: 'ends', iso: u.period_end }
}

/** Whether a row offers "Start over…": one-time budgets that have not ended. */
export function canStartOver(u: Pick<BudgetUsage, 'period' | 'state'>): boolean {
  return u.period === 'once' && u.state !== 'expired'
}
