import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { BookOpen, CheckCircle2, CircleAlert } from 'lucide-react'
import { Button, SearchInput } from '@/components/ui'
import { apiSend } from '@/lib/api/client'
import { routingVariablesQuery } from '@/lib/api/queries'
import type { RoutingVariable } from '@/lib/api/types'
import r from './routing.module.css'

export type ExprKind = 'bool' | 'float' | 'any'
/** Which variables an expression may use: request-stage, candidate-stage or a decision-table cell. */
export type ExprScope = 'request' | 'candidate' | 'cell'

const MAX_EXPR = 2000

/** Debounces a value by `ms`. */
export function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value)
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms)
    return () => clearTimeout(t)
  }, [value, ms])
  return v
}

/**
 * Client-side mirrors of checks the server applies to whole documents but
 * not to a single expression.
 */
export function scopeProblem(code: string, scope: ExprScope): string | undefined {
  // Same rule as the server: any mention of candidate.* in a request-stage expression.
  if (scope === 'request' && code.includes('candidate.')) return 'Request-stage expressions cannot reference candidate.*'
  const unquoted = code.replace(/"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'/g, '""')
  if (scope !== 'cell' && /\bvalue\b/.test(unquoted)) return 'value is only defined inside decision-table cells'
  return undefined
}

/** Server-side compilation of one expression against the typed environment (debounced, cached). */
export function useExprValidation(code: string, kind: ExprKind, scope: ExprScope, opts?: { optional?: boolean; any?: boolean }) {
  const debounced = useDebounced(code.trim(), 400)
  const skip = debounced === '' || (opts?.any && debounced === '-')
  const q = useQuery({
    queryKey: ['routing', 'validate', kind, debounced],
    queryFn: () => apiSend<{ valid: boolean; error?: string }>('POST', '/routing/validate-expression', { expression: debounced, kind }),
    enabled: !skip && debounced.length <= MAX_EXPR,
    staleTime: Infinity,
    retry: false,
  })
  const pending = code.trim() !== debounced || q.isFetching
  if (code.trim() === '') return { state: opts?.optional ? ('empty' as const) : ('error' as const), message: opts?.optional ? undefined : 'Required.' }
  if (opts?.any && code.trim() === '-') return { state: 'ok' as const, message: 'Matches any value.' }
  const local = scopeProblem(code, scope)
  if (local) return { state: 'error' as const, message: local }
  if (pending || !q.data) {
    if (q.isError) return { state: 'error' as const, message: 'Could not validate the expression.' }
    return { state: 'pending' as const, message: 'Checking…' }
  }
  return q.data.valid ? { state: 'ok' as const, message: undefined } : { state: 'error' as const, message: q.data.error ?? 'Invalid expression.' }
}

export function ExprStatus({ v, id, okText = 'Valid' }: { v: ReturnType<typeof useExprValidation>; id: string; okText?: string }) {
  return (
    <div id={id} aria-live="polite" className={v.state === 'ok' ? r.statusOk : v.state === 'error' ? r.statusErr : r.statusMuted}>
      {v.state === 'ok' && (
        <>
          <CheckCircle2 size={12} aria-hidden="true" /> {v.message ?? okText}
        </>
      )}
      {v.state === 'error' && (
        <>
          <CircleAlert size={12} aria-hidden="true" style={{ flexShrink: 0, marginTop: 2 }} /> {v.message}
        </>
      )}
      {v.state === 'pending' && v.message}
    </div>
  )
}

function inScope(v: RoutingVariable, scope: ExprScope): boolean {
  if (v.stage === 'table') return scope === 'cell'
  if (scope === 'request') return v.stage === 'request'
  return true
}

/** Searchable, typed reference of routing variables; clicking one inserts it. */
export function VariableReference({ scope, onInsert }: { scope: ExprScope; onInsert: (name: string) => void }) {
  const vars = useQuery(routingVariablesQuery)
  const [q, setQ] = useState('')
  const items = useMemo(() => {
    const needle = q.trim().toLowerCase()
    return (vars.data?.items ?? [])
      .filter((v) => inScope(v, scope))
      .filter((v) => !needle || v.name.toLowerCase().includes(needle) || v.description.toLowerCase().includes(needle))
  }, [vars.data, q, scope])
  const extra = [
    { name: 'known(', type: 'func(any) bool', stage: 'any', description: 'True when a value is known (not nil). Guard optional values such as candidate.health.remaining.' },
  ]
  return (
    <div className={r.reference}>
      <SearchInput value={q} onChange={setQ} label="Search variables" placeholder="Search variables" />
      {vars.isError ? (
        <p className="secondary">The variable reference could not be loaded.</p>
      ) : !vars.data ? (
        <p className="secondary">Loading variables…</p>
      ) : (
        <ul className={r.refList} aria-label="Routing variables">
          {[...items, ...extra.filter((e) => !q || e.name.includes(q.toLowerCase()))].map((v) => (
            <li key={v.name}>
              <button type="button" className={r.refItem} onClick={() => onInsert(v.name)} title={`Insert ${v.name}`}>
                <span className={r.refName}>{v.name}</span>
                <span className={r.refType}>{v.type}</span>
                <span className={r.refDesc}>{v.description}</span>
              </button>
            </li>
          ))}
          {items.length === 0 && <li className="secondary">No variable matches.</li>}
        </ul>
      )}
    </div>
  )
}

/**
 * Monospace expression editor with debounced server-side validation and a
 * click-to-insert variable reference.
 */
export function ExprEditor({ label, value, onChange, kind, scope, hint, optional, rows = 2, placeholder }: {
  label: string
  value: string
  onChange: (v: string) => void
  kind: ExprKind
  scope: ExprScope
  hint?: string
  optional?: boolean
  rows?: number
  placeholder?: string
}) {
  const id = useId()
  const ref = useRef<HTMLTextAreaElement>(null)
  const [showRef, setShowRef] = useState(false)
  const v = useExprValidation(value, kind, scope, { optional })
  const insert = (name: string) => {
    const el = ref.current
    const start = el?.selectionStart ?? value.length
    const end = el?.selectionEnd ?? value.length
    const before = value.slice(0, start)
    const pad = before && !/[\s(!]$/.test(before) ? ' ' : ''
    const next = before + pad + name + value.slice(end)
    onChange(next.slice(0, MAX_EXPR))
    requestAnimationFrame(() => {
      if (!el) return
      el.focus()
      const pos = (before + pad + name).length
      el.setSelectionRange(pos, pos)
    })
  }
  return (
    <div className={r.expr}>
      <div className={r.exprLabelRow}>
        <label htmlFor={id} className={r.exprLabel}>
          {label}
        </label>
        <Button size="small" variant="ghost" icon={BookOpen} aria-expanded={showRef} onClick={() => setShowRef((s) => !s)}>
          {showRef ? 'Hide variables' : 'Variables'}
        </Button>
      </div>
      <textarea
        id={id}
        ref={ref}
        className={r.textarea}
        rows={rows}
        spellCheck={false}
        autoCapitalize="off"
        autoComplete="off"
        maxLength={MAX_EXPR}
        value={value}
        placeholder={placeholder}
        aria-invalid={v.state === 'error' || undefined}
        aria-describedby={`${id}-status${hint ? ` ${id}-hint` : ''}`}
        onChange={(e) => onChange(e.target.value)}
      />
      {hint && (
        <span id={`${id}-hint`} className={r.hint}>
          {hint}
        </span>
      )}
      <ExprStatus v={v} id={`${id}-status`} />
      {showRef && <VariableReference scope={scope} onInsert={insert} />}
    </div>
  )
}
