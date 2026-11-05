import { Link } from '@tanstack/react-router'
import s from './ui.module.css'

/** Section navigation rendered as tab-styled links (each link is a route). */
export function SubNav({ label, items }: { label: string; items: { to: string; label: string; exact?: boolean }[] }) {
  return (
    <nav aria-label={label} className={`${s.tabs} ${s.subnav}`}>
      {items.map((it) => (
        <Link
          key={it.to}
          to={it.to}
          className={s.tab}
          activeOptions={{ exact: it.exact ?? false }}
          activeProps={{ 'aria-current': 'page' }}
        >
          {it.label}
        </Link>
      ))}
    </nav>
  )
}
