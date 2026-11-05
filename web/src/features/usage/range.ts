import { serverNow } from '@/lib/format'

export const rangePresets = [
  { value: '1', label: '24 hours' },
  { value: '7', label: '7 days' },
  { value: '30', label: '30 days' },
  { value: '90', label: '90 days' },
] as const

export type RangePreset = (typeof rangePresets)[number]['value']

/** A [from, to) range that includes now (server clock): to is the start of the next minute. */
export function presetRange(days: number, now: number = serverNow()): { from: string; to: string } {
  const to = Math.floor(now / 60_000) * 60_000 + 60_000
  return { from: new Date(to - days * 86_400_000).toISOString(), to: new Date(to).toISOString() }
}

/** Hourly steps for ranges up to 7 days, daily beyond (the API caps hourly at 31 days). */
export function stepFor(days: number): 'hour' | 'day' {
  return days <= 7 ? 'hour' : 'day'
}
