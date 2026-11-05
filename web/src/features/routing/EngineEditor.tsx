import { useEffect, useId, useState } from 'react'
import { ArrowDown, ArrowUp, BookOpen, Columns3, Plus, Rows3, Trash2 } from 'lucide-react'
import { Badge, Banner, Button } from '@/components/ui'
import { Checkbox, SelectField, TextField } from '@/components/form'
import type { EngineSpec, FieldErrorDTO, RuleAction, RuleSpec, RuleStage, TableRow, TableSpec } from '@/lib/api/types'
import { capabilities } from '@/features/config/constants'
import { ExprEditor, ExprStatus, VariableReference, useExprValidation, type ExprScope } from './ExprEditor'
import { actionsByStage, describeLocation, hitPolicies, move, newRule, newTable, stages, structuralProblems } from './engine'
import r from './routing.module.css'

type ActionFields = Pick<RuleSpec, 'action' | 'capability' | 'intent' | 'boost' | 'reason'>

/** Numeric input that tolerates intermediate text such as "-" or "1.". */
function NumberField({ label, value, onChange, hint }: { label: string; value: number | undefined; onChange: (v: number) => void; hint?: string }) {
  const [text, setText] = useState(value === undefined ? '' : String(value))
  // Follow external changes (reordering, resets) without clobbering partial input.
  useEffect(() => {
    setText((t) => (Number(t) === (value ?? 0) && t.trim() !== '' ? t : value === undefined ? '' : String(value)))
  }, [value])
  const parsed = Number(text)
  const bad = text.trim() !== '' && !Number.isFinite(parsed)
  return (
    <TextField
      label={label}
      value={text}
      hint={hint}
      error={bad ? 'Enter a number.' : undefined}
      onChange={(v) => {
        setText(v)
        const n = Number(v)
        if (v.trim() !== '' && Number.isFinite(n)) onChange(n)
        if (v.trim() === '') onChange(0)
      }}
    />
  )
}

function ActionEditor({ stage, value, onChange, compact }: { stage: RuleStage; value: ActionFields; onChange: (v: ActionFields) => void; compact?: boolean }) {
  const set = (p: Partial<ActionFields>) => onChange({ ...value, ...p })
  return (
    <>
      <SelectField label="Action" value={value.action} onChange={(a: RuleAction) => set({ action: a })} options={actionsByStage[stage]} />
      {value.action === 'require' && (
        <SelectField label="Capability" value={value.capability ?? ''} onChange={(v) => set({ capability: v })}
          options={[{ value: '', label: 'Select a capability' }, ...capabilities.map((x) => ({ value: x, label: x }))]} />
      )}
      {value.action === 'set_intent' && (
        <TextField label="Intent" value={value.intent ?? ''} onChange={(v) => set({ intent: v })} maxLength={64} placeholder="review" />
      )}
      {value.action === 'boost' && (
        <NumberField label="Score change" value={value.boost} onChange={(n) => set({ boost: n })}
          hint={compact ? undefined : 'Positive moves the candidate earlier, negative later. Never re-admits excluded candidates.'} />
      )}
      {(value.action === 'block' || value.action === 'exclude') && (
        <TextField label="Reason" value={value.reason ?? ''} onChange={(v) => set({ reason: v })} maxLength={200}
          hint={compact ? undefined : value.action === 'block' ? 'Returned to the client and shown in activity.' : 'Shown in the routing explanation.'} />
      )}
    </>
  )
}

function ErrorList({ errors, engine }: { errors: FieldErrorDTO[]; engine: EngineSpec }) {
  if (errors.length === 0) return null
  return (
    <ul className={r.errors} role="list" aria-label="Validation errors">
      {errors.map((e, i) => (
        <li key={i}>
          <strong>{describeLocation(e.location, engine)}:</strong> {e.message}
        </li>
      ))}
    </ul>
  )
}

function RuleCard({ rule, index, count, errors, engine, onChange, onMove, onRemove }: {
  rule: RuleSpec
  index: number
  count: number
  errors: FieldErrorDTO[]
  engine: EngineSpec
  onChange: (r: RuleSpec) => void
  onMove: (d: -1 | 1) => void
  onRemove: () => void
}) {
  const label = rule.name || `Rule ${index + 1}`
  return (
    <li className={rule.enabled ? r.card : r.cardDisabled} aria-label={`Rule ${label}`}>
      <div className={r.cardHeader}>
        <div className={r.cardTitle}>
          <span className="num secondary">{index + 1}.</span>
          <strong className="mono">{label}</strong>
          <Badge tone={rule.stage === 'request' ? 'info' : 'purple'}>{rule.stage}</Badge>
          {!rule.enabled && <Badge>disabled</Badge>}
        </div>
        <div className={r.cardActions}>
          <Checkbox label="Enabled" checked={rule.enabled} onChange={(v) => onChange({ ...rule, enabled: v })} />
          <Button size="small" variant="ghost" icon={ArrowUp} aria-label={`Move ${label} up`} disabled={index === 0} onClick={() => onMove(-1)} />
          <Button size="small" variant="ghost" icon={ArrowDown} aria-label={`Move ${label} down`} disabled={index === count - 1} onClick={() => onMove(1)} />
          <Button size="small" variant="ghost" icon={Trash2} aria-label={`Remove ${label}`} onClick={onRemove} />
        </div>
      </div>
      <div className={r.fields}>
        <TextField label="Name" value={rule.name} onChange={(v) => onChange({ ...rule, name: v })} maxLength={100} />
        <SelectField label="Stage" value={rule.stage} options={stages}
          onChange={(st: RuleStage) => onChange({ ...rule, stage: st, action: actionsByStage[st].some((a) => a.value === rule.action) ? rule.action : (actionsByStage[st][0]?.value as RuleAction) })} />
      </div>
      <ExprEditor label="When" kind="bool" scope={rule.stage} value={rule.when} onChange={(v) => onChange({ ...rule, when: v })}
        placeholder={rule.stage === 'request' ? "request.harness == 'codex'" : "candidate.account.billing == 'metered_api'"}
        hint="A boolean expression. Unknown values (such as missing quota) make the rule not apply; guard them with known(...)." />
      <div className={r.fields}>
        <ActionEditor stage={rule.stage} value={rule} onChange={(a) => onChange({ ...rule, ...a })} />
      </div>
      <ErrorList errors={errors} engine={engine} />
    </li>
  )
}

/** One expression cell in a decision table (input column or condition). */
function Cell({ label, value, onChange, scope, kind, onFocus }: {
  label: string
  value: string
  onChange: (v: string) => void
  scope: ExprScope
  kind: 'bool' | 'any'
  onFocus: () => void
}) {
  const id = useId()
  const v = useExprValidation(value, kind, scope, { any: scope === 'cell' })
  return (
    <div className={r.expr}>
      <textarea
        className={r.cellInput}
        rows={1}
        spellCheck={false}
        autoCapitalize="off"
        aria-label={label}
        aria-invalid={v.state === 'error' || undefined}
        aria-describedby={`${id}-s`}
        maxLength={2000}
        value={value}
        onFocus={onFocus}
        onChange={(e) => onChange(e.target.value)}
      />
      <ExprStatus v={v} id={`${id}-s`} okText="OK" />
    </div>
  )
}

type Focus = { kind: 'input'; col: number } | { kind: 'cell'; row: number; col: number }

function TableCard({ table, index, count, errors, engine, onChange, onMove, onRemove }: {
  table: TableSpec
  index: number
  count: number
  errors: FieldErrorDTO[]
  engine: EngineSpec
  onChange: (t: TableSpec) => void
  onMove: (d: -1 | 1) => void
  onRemove: () => void
}) {
  const label = table.name || `Table ${index + 1}`
  const [focus, setFocus] = useState<Focus | null>(null)
  const [showRef, setShowRef] = useState(false)
  const setRow = (i: number, row: TableRow) => onChange({ ...table, rows: table.rows.map((x, j) => (j === i ? row : x)) })
  const setInput = (col: number, v: string) => onChange({ ...table, inputs: table.inputs.map((x, j) => (j === col ? v : x)) })
  const setCond = (row: number, col: number, v: string) => {
    const r0 = table.rows[row]
    if (r0) setRow(row, { ...r0, conditions: r0.conditions.map((x, j) => (j === col ? v : x)) })
  }
  const insert = (name: string) => {
    if (!focus) return
    const add = (cur: string) => (cur.trim() === '' || cur.trim() === '-' ? name : `${cur}${/\s$/.test(cur) ? '' : ' '}${name}`)
    if (focus.kind === 'input') setInput(focus.col, add(table.inputs[focus.col] ?? ''))
    else setCond(focus.row, focus.col, add(table.rows[focus.row]?.conditions[focus.col] ?? ''))
  }
  const addColumn = () => onChange({ ...table, inputs: [...table.inputs, ''], rows: table.rows.map((x) => ({ ...x, conditions: [...x.conditions, '-'] })) })
  const removeColumn = (col: number) =>
    onChange({ ...table, inputs: table.inputs.filter((_, j) => j !== col), rows: table.rows.map((x) => ({ ...x, conditions: x.conditions.filter((_, j) => j !== col) })) })
  const firstAction = actionsByStage[table.stage][0]?.value as RuleAction
  const hp = hitPolicies.find((h) => h.value === table.hit_policy)
  return (
    <li className={table.enabled ? r.card : r.cardDisabled} aria-label={`Table ${label}`}>
      <div className={r.cardHeader}>
        <div className={r.cardTitle}>
          <span className="num secondary">{index + 1}.</span>
          <strong className="mono">{label}</strong>
          <Badge tone={table.stage === 'request' ? 'info' : 'purple'}>{table.stage}</Badge>
          <Badge>hit policy: {table.hit_policy}</Badge>
          {!table.enabled && <Badge>disabled</Badge>}
        </div>
        <div className={r.cardActions}>
          <Checkbox label="Enabled" checked={table.enabled} onChange={(v) => onChange({ ...table, enabled: v })} />
          <Button size="small" variant="ghost" icon={ArrowUp} aria-label={`Move ${label} up`} disabled={index === 0} onClick={() => onMove(-1)} />
          <Button size="small" variant="ghost" icon={ArrowDown} aria-label={`Move ${label} down`} disabled={index === count - 1} onClick={() => onMove(1)} />
          <Button size="small" variant="ghost" icon={Trash2} aria-label={`Remove ${label}`} onClick={onRemove} />
        </div>
      </div>
      <div className={r.fields}>
        <TextField label="Name" value={table.name} onChange={(v) => onChange({ ...table, name: v })} maxLength={100} />
        <SelectField label="Stage" value={table.stage} options={stages}
          onChange={(st: RuleStage) =>
            onChange({ ...table, stage: st, rows: table.rows.map((x) => (actionsByStage[st].some((a) => a.value === x.action) ? x : { ...x, action: actionsByStage[st][0]?.value as RuleAction })) })} />
        <SelectField label="Hit policy" value={table.hit_policy} hint={hp?.help} options={hitPolicies.map((h) => ({ value: h.value, label: h.label }))}
          onChange={(v) => onChange({ ...table, hit_policy: v })} />
      </div>
      <p className={r.hint}>
        Each column evaluates an input expression; each cell is a condition over <code>value</code> (the column's result), for example{' '}
        <code>value == 'codex'</code> or <code>value in ['a', 'b']</code>. <code>-</code> matches anything.
      </p>
      <div className={r.tableWrap}>
        <table className={r.grid} aria-label={`Decision table ${label}`}>
          <thead>
            <tr>
              <th scope="col">#</th>
              {table.inputs.map((inp, col) => (
                <th key={col} scope="col">
                  <div className={r.colHead}>
                    <Cell label={`Input ${col + 1} expression`} value={inp} onChange={(v) => setInput(col, v)} scope={table.stage} kind="any"
                      onFocus={() => setFocus({ kind: 'input', col })} />
                    <Button size="small" variant="ghost" icon={Trash2} aria-label={`Remove input column ${col + 1}`} disabled={table.inputs.length === 1}
                      onClick={() => removeColumn(col)} />
                  </div>
                </th>
              ))}
              <th scope="col">Then</th>
              <th scope="col">
                <span className="visually-hidden">Row actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {table.rows.map((row, i) => (
              <tr key={i}>
                <td className="num">{i + 1}</td>
                {table.inputs.map((_, col) => (
                  <td key={col}>
                    <Cell label={`Row ${i + 1} condition ${col + 1}`} value={row.conditions[col] ?? '-'} onChange={(v) => setCond(i, col, v)} scope="cell" kind="bool"
                      onFocus={() => setFocus({ kind: 'cell', row: i, col })} />
                  </td>
                ))}
                <td>
                  <div className={r.actionCell}>
                    <ActionEditor compact stage={table.stage} value={row} onChange={(a) => setRow(i, { ...row, ...a })} />
                  </div>
                </td>
                <td style={{ whiteSpace: 'nowrap' }}>
                  <Button size="small" variant="ghost" icon={ArrowUp} aria-label={`Move row ${i + 1} up`} disabled={i === 0}
                    onClick={() => onChange({ ...table, rows: move(table.rows, i, -1) })} />
                  <Button size="small" variant="ghost" icon={ArrowDown} aria-label={`Move row ${i + 1} down`} disabled={i === table.rows.length - 1}
                    onClick={() => onChange({ ...table, rows: move(table.rows, i, 1) })} />
                  <Button size="small" variant="ghost" icon={Trash2} aria-label={`Remove row ${i + 1}`}
                    onClick={() => onChange({ ...table, rows: table.rows.filter((_, j) => j !== i) })} />
                </td>
              </tr>
            ))}
            {table.rows.length === 0 && (
              <tr>
                <td colSpan={table.inputs.length + 3} className="secondary">
                  No rows. A table without rows never applies.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      <div className={r.cardActions}>
        <Button size="small" icon={Rows3}
          onClick={() => onChange({ ...table, rows: [...table.rows, { conditions: table.inputs.map(() => '-'), action: firstAction }] })}>
          Add row
        </Button>
        <Button size="small" icon={Columns3} disabled={table.inputs.length >= 10} onClick={addColumn}>
          Add input column
        </Button>
        <Button size="small" variant="ghost" icon={BookOpen} aria-expanded={showRef} onClick={() => setShowRef((s) => !s)}>
          {showRef ? 'Hide variables' : 'Variables'}
        </Button>
      </div>
      {showRef && (
        <>
          <p className={r.hint}>{focus ? 'Click a variable to insert it into the focused cell.' : 'Focus a cell first, then click a variable to insert it.'}</p>
          <VariableReference scope={focus?.kind === 'cell' ? 'cell' : table.stage} onInsert={insert} />
        </>
      )}
      <ErrorList errors={errors} engine={engine} />
    </li>
  )
}

/**
 * Editor for a plan's routing engine: ordered rules, decision tables and a
 * score expression. Hard restrictions and health always run first; these
 * rules can block, require, set intent, exclude or re-rank, never re-admit.
 */
export function EngineEditor({ value, onChange, errors = [] }: { value: EngineSpec; onChange: (e: EngineSpec) => void; errors?: FieldErrorDTO[] }) {
  const problems = structuralProblems(value)
  const errsFor = (prefix: string) => errors.filter((e) => e.location.startsWith(prefix + '.') || e.location === prefix)
  const general = errors.filter((e) => e.location.startsWith('body.document.engine') && !/engine\.(rules|tables)\[/.test(e.location))
  return (
    <div className={r.section}>
      <Banner tone="info">
        Evaluation order: request rules and tables (block, require, set intent) → hard restrictions → account health → candidate rules and
        tables (exclude, adjust score) → score expression → pool order. Scores never bring back an excluded candidate.
      </Banner>
      {errors.length > 0 && (
        <Banner tone="error" role="status">
          The last validation of this draft reported {errors.length} problem{errors.length === 1 ? '' : 's'}; they are shown next to the rule or table
          they refer to. Save and validate again after fixing them.
        </Banner>
      )}
      {general.length > 0 && <ErrorList errors={general} engine={value} />}
      {problems.length > 0 && (
        <Banner tone="warning" role="status">
          <ul style={{ margin: 0, paddingLeft: 18 }}>
            {problems.map((p) => (
              <li key={p}>{p}</li>
            ))}
          </ul>
        </Banner>
      )}

      <div className={r.sectionHeader}>
        <h3>Rules</h3>
        <span className="secondary">Evaluated in order within each stage.</span>
      </div>
      {value.rules.length === 0 ? (
        <p className="secondary">No rules. Add one to block, require capabilities, set intents, exclude or re-rank candidates.</p>
      ) : (
        <ol className={r.section} style={{ listStyle: 'none', margin: 0, padding: 0 }} aria-label="Rules">
          {value.rules.map((rule, i) => (
            <RuleCard key={i} rule={rule} index={i} count={value.rules.length} engine={value} errors={errsFor(`body.document.engine.rules[${i}]`)}
              onChange={(nr) => onChange({ ...value, rules: value.rules.map((x, j) => (j === i ? nr : x)) })}
              onMove={(d) => onChange({ ...value, rules: move(value.rules, i, d) })}
              onRemove={() => onChange({ ...value, rules: value.rules.filter((_, j) => j !== i) })} />
          ))}
        </ol>
      )}
      <div>
        <Button size="small" icon={Plus} disabled={value.rules.length >= 100} onClick={() => onChange({ ...value, rules: [...value.rules, newRule(value.rules)] })}>
          Add rule
        </Button>
      </div>

      <div className={r.sectionHeader}>
        <h3>Decision tables</h3>
        <span className="secondary">Evaluated after rules of the same stage.</span>
      </div>
      {value.tables.length === 0 ? (
        <p className="secondary">No decision tables.</p>
      ) : (
        <ol className={r.section} style={{ listStyle: 'none', margin: 0, padding: 0 }} aria-label="Decision tables">
          {value.tables.map((t, i) => (
            <TableCard key={i} table={t} index={i} count={value.tables.length} engine={value} errors={errsFor(`body.document.engine.tables[${i}]`)}
              onChange={(nt) => onChange({ ...value, tables: value.tables.map((x, j) => (j === i ? nt : x)) })}
              onMove={(d) => onChange({ ...value, tables: move(value.tables, i, d) })}
              onRemove={() => onChange({ ...value, tables: value.tables.filter((_, j) => j !== i) })} />
          ))}
        </ol>
      )}
      <div>
        <Button size="small" icon={Plus} disabled={value.tables.length >= 50}
          onClick={() => onChange({ ...value, tables: [...value.tables, newTable([...value.rules.map((x) => x.name), ...value.tables.map((x) => x.name)])] })}>
          Add decision table
        </Button>
      </div>

      <div className={r.sectionHeader}>
        <h3>Score</h3>
      </div>
      <ExprEditor label="Score expression (optional)" kind="float" scope="candidate" optional value={value.score} onChange={(v) => onChange({ ...value, score: v })}
        placeholder="candidate.native ? 10.0 : 0.0"
        hint="A number per candidate; higher ranks earlier, after exclusions and before pool order. Leave empty to keep pool order." />
    </div>
  )
}
