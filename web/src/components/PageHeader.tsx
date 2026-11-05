import type { ReactNode } from 'react'

export function PageHeader({ title, subtitle, actions, eyebrow }: {
  title: ReactNode
  subtitle?: ReactNode
  actions?: ReactNode
  eyebrow?: ReactNode
}) {
  return (
    <header style={{ display: 'flex', justifyContent: 'space-between', gap: 16, flexWrap: 'wrap', alignItems: 'flex-end' }}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6, minWidth: 0 }}>
        {eyebrow && <div className="muted" style={{ fontSize: 'var(--text-sm)' }}>{eyebrow}</div>}
        <h1>{title}</h1>
        {subtitle && <p className="secondary" style={{ fontSize: 'var(--text-md)' }}>{subtitle}</p>}
      </div>
      {actions && <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>{actions}</div>}
    </header>
  )
}
