import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ActionIcon, Group, NumberInput, Select, SimpleGrid, Stack, Text } from '@mantine/core'
import { DatePickerInput } from '@mantine/dates'
import { Plus, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui'
import { ConfirmButton, Dialog, MutationError, NameField, NumberField, SelectField, TextField, nameError, slugify, useFieldErrors } from '@/components/form'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { ApiError } from '@/lib/api/client'
import { resourceListQuery } from '@/lib/api/queries'
import { useDeleteResource, useSaveResource } from '@/lib/api/resources'
import type { AccountRes, EntitlementRes, EntitlementSpec, WindowSpec } from '@/lib/api/types'
import { freeName } from '@/features/providers/labels'

/** Plain names for window units. */
export const windowUnitLabels: Record<WindowSpec['unit'], string> = {
  percent: 'Percent of the allowance',
  tokens: 'Tokens',
  requests: 'Requests',
  credits: 'Credits',
  unknown: 'Not published',
}

const HOUR = 3600
const DAY = 24 * HOUR

/** Common window lengths, with the name the window gets. */
export const windowPresets = [
  { value: String(5 * HOUR), label: '5-hour session', name: 'five_hour' },
  { value: String(DAY), label: 'Daily', name: 'daily' },
  { value: String(7 * DAY), label: 'Weekly', name: 'seven_day' },
  { value: String(30 * DAY), label: 'Monthly (30 days)', name: 'monthly' },
] as const

/** A window length in words, e.g. "5 hours" or "7 days". */
export function windowLength(seconds: number): string {
  if (seconds > 0 && seconds % DAY === 0) return seconds === DAY ? '1 day' : `${seconds / DAY} days`
  if (seconds > 0 && seconds % HOUR === 0) return seconds === HOUR ? '1 hour' : `${seconds / HOUR} hours`
  return `${Math.round(seconds / 60)} minutes`
}

const currencies = ['USD', 'EUR', 'GBP', 'CHF', 'CAD', 'AUD', 'JPY', 'SEK', 'NOK', 'DKK', 'PLN']
const today = () => new Date().toISOString().slice(0, 10)

function WindowRow({ w, onChange, onRemove, index }: { w: WindowSpec; onChange: (p: Partial<WindowSpec>) => void; onRemove: () => void; index: number }) {
  const preset = windowPresets.find((p) => Number(p.value) === w.seconds)
  const [custom, setCustom] = useState(!preset)
  const hours = w.seconds / HOUR
  return (
    <Group gap="sm" align="flex-end" wrap="wrap" pb="xs" style={{ borderBottom: '1px solid var(--border)' }}>
      <Select
        label="Length"
        w={180}
        value={custom ? 'custom' : String(w.seconds)}
        data={[...windowPresets.map((p) => ({ value: p.value, label: p.label })), { value: 'custom', label: 'Other length' }]}
        allowDeselect={false}
        onChange={(v) => {
          if (v === 'custom') return setCustom(true)
          const p = windowPresets.find((x) => x.value === v)
          setCustom(false)
          if (p) onChange({ seconds: Number(p.value), name: !w.name || windowPresets.some((x) => x.name === w.name) ? p.name : w.name })
        }}
      />
      {custom && (
        <NumberField label="Hours" unit="h" min={1} max={24 * 366} value={hours || null} onChange={(n) => onChange({ seconds: (n ?? 0) * HOUR })} w={120} />
      )}
      <SelectField label="Measured in" value={w.unit} onChange={(v) => onChange({ unit: v })}
        options={Object.entries(windowUnitLabels).map(([value, label]) => ({ value: value as WindowSpec['unit'], label }))} />
      <NumberField label="Limit" placeholder="Not published" min={1} value={w.limit ?? null} onChange={(n) => onChange({ limit: n ?? undefined })} w={140} />
      <ActionIcon variant="subtle" color="gray" mb={6} aria-label={`Remove window ${index + 1}`} onClick={onRemove}>
        <Trash2 size={14} />
      </ActionIcon>
    </Group>
  )
}

/** Create or edit a subscription: what an account's plan includes and costs. */
export function EntitlementDialog({ ent, onClose, accountID: presetAccount }: { ent: EntitlementRes | null; onClose: () => void; accountID?: string }) {
  const accounts = useQuery(resourceListQuery<AccountRes>('provider-accounts'))
  const ents = useQuery(resourceListQuery<EntitlementRes>('subscription-entitlements'))
  const init = ent?.spec
  const [accountID, setAccountID] = useState(init?.account_id ?? presetAccount ?? '')
  const [planName, setPlanName] = useState(init?.plan_name ?? '')
  const [slug, setSlug] = useState('')
  const [slugEdited, setSlugEdited] = useState(false)
  const [fee, setFee] = useState<number | string>(init?.fee ? Number(init.fee) : '')
  const [currency, setCurrency] = useState(init?.currency || 'USD')
  const [period, setPeriod] = useState<EntitlementSpec['period']>(init?.period ?? 'monthly')
  const [startsOn, setStartsOn] = useState<string | null>(init?.starts_on || today())
  const [endsOn, setEndsOn] = useState<string | null>(init?.ends_on || null)
  const [windows, setWindows] = useState<WindowSpec[]>(init?.windows ?? [])
  const save = useSaveResource<EntitlementSpec>('subscription-entitlements')
  const del = useDeleteResource('subscription-entitlements')
  const fe = useFieldErrors(save.error)
  const taken = useMemo(() => new Set((ents.data?.items ?? []).map((e) => e.name)), [ents.data])
  const account = accounts.data?.items?.find((a) => a.id === accountID)
  const name = ent
    ? ent.name
    : slugEdited
      ? slug
      : freeName(slugify(`${account?.name ?? 'subscription'} ${planName}`.trim()) || 'subscription', taken)
  const conflict = save.error instanceof ApiError && save.error.status === 409
  const feeNum = typeof fee === 'number' ? fee : Number.NaN
  const reason = !accountID
    ? 'Choose the account.'
    : !planName.trim()
      ? 'Enter the plan name.'
      : Number.isNaN(feeNum) || feeNum < 0
        ? 'Enter the fee.'
        : !startsOn
          ? 'Choose the start date.'
          : !name || nameError(name)
            ? 'Fix the short name.'
            : windows.some((w) => w.seconds <= 0)
              ? 'Give every window a length.'
              : ''

  const submit = () => {
    const spec: EntitlementSpec = {
      account_id: accountID,
      plan_name: planName.trim(),
      fee: feeNum.toFixed(2),
      currency,
      period,
      starts_on: startsOn ?? today(),
      ends_on: endsOn ?? '',
      windows: windows.map((w, i) => ({ ...w, name: w.name || `window_${i + 1}` })),
    }
    save.mutate({ existing: ent ?? undefined, body: { name, spec } }, { onSuccess: onClose })
  }

  return (
    <Dialog
      open
      wide
      title={ent ? `Edit ${ent.spec.plan_name || ent.name}` : 'Add a subscription'}
      onClose={onClose}
      footer={
        <>
          {ent && (
            <ConfirmButton
              title="Delete this subscription?"
              message="Its fee stops counting towards subscription costs and its windows towards forecasts. The account itself stays."
              confirmLabel="Delete subscription"
              busy={del.isPending}
              icon={Trash2}
              onConfirm={() => del.mutate({ id: ent.id, revision: ent.revision }, { onSuccess: onClose })}
            >
              Delete
            </ConfirmButton>
          )}
          <Text size="xs" c="dimmed" mr="auto">
            {reason}
          </Text>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" busy={save.isPending} disabled={!!reason} onClick={submit}>
            {ent ? 'Save subscription' : 'Add subscription'}
          </Button>
        </>
      }
    >
      <TerraformEditWarning r={ent} />
      {!presetAccount && (
        <SelectField label="Account" value={accountID} onChange={setAccountID} required disabled={!!ent} placeholder="Choose an account"
          options={(accounts.data?.items ?? []).map((a) => ({ value: a.id, label: a.spec.display_name || a.name }))} />
      )}
      <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
        <TextField label="Plan name" value={planName} onChange={setPlanName} placeholder="e.g. Max 20x" required autoFocus error={fe.plan_name} />
        {!ent ? (
          <NameField value={name} onChange={(v) => { setSlugEdited(v !== ''); setSlug(v) }}
            error={fe.name ?? (conflict ? `A subscription called ${name} already exists.` : undefined)} />
        ) : (
          <NameField value={name} onChange={() => {}} locked />
        )}
        <Group gap="xs" align="flex-start" wrap="nowrap">
          <NumberInput
            label="Fee"
            required
            withAsterisk
            min={0}
            decimalScale={2}
            fixedDecimalScale
            thousandSeparator=","
            hideControls
            value={fee}
            onChange={setFee}
            error={fe.fee}
            style={{ flex: 1 }}
          />
          <Select label="Currency" w={100} searchable allowDeselect={false} data={currencies.includes(currency) ? currencies : [currency, ...currencies]} value={currency}
            onChange={(v) => setCurrency(v ?? 'USD')} />
        </Group>
        <SelectField label="Billed" value={period} onChange={setPeriod} options={[{ value: 'monthly', label: 'Every month' }, { value: 'yearly', label: 'Every year' }]} />
        <DatePickerInput label="Starts on" required value={startsOn} onChange={setStartsOn} valueFormat="D MMM YYYY" error={fe.starts_on} popoverProps={{ withinPortal: true }} />
        <DatePickerInput label="Ends on" description="Leave empty if it renews." clearable value={endsOn} onChange={setEndsOn} minDate={startsOn ?? undefined}
          valueFormat="D MMM YYYY" placeholder="Renews" error={fe.ends_on} popoverProps={{ withinPortal: true }} />
      </SimpleGrid>
      <Stack gap="xs">
        <div>
          <Text size="sm" fw={500}>
            Usage windows
          </Text>
          <Text size="xs" c="dimmed">
            The limits the plan advertises, such as a 5-hour session. They are used for forecasts. Live usage comes from quota tracking on the account.
          </Text>
        </div>
        {windows.map((w, i) => (
          <WindowRow
            key={i}
            index={i}
            w={w}
            onChange={(p) => setWindows(windows.map((x, j) => (j === i ? { ...x, ...p } : x)))}
            onRemove={() => setWindows(windows.filter((_, j) => j !== i))}
          />
        ))}
        <div>
          <Button size="small" icon={Plus}
            onClick={() => {
              const next = windowPresets.find((p) => !windows.some((w) => w.seconds === Number(p.value))) ?? windowPresets[0]
              setWindows([...windows, { name: next.name, seconds: Number(next.value), unit: 'percent' }])
            }}>
            Add window
          </Button>
        </div>
      </Stack>
      <MutationError error={conflict ? null : save.error} />
      <MutationError error={del.error} />
    </Dialog>
  )
}
