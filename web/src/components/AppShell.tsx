import { type ReactNode, useEffect, useState } from 'react'
import { Link, useRouterState } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { useStore } from '@tanstack/react-store'
import {
  Activity,
  Building2,
  Coins,
  Gauge,
  Laptop,
  LayoutDashboard,
  LogOut,
  type LucideIcon,
  Menu,
  Moon,
  PanelLeftClose,
  PanelLeftOpen,
  Route as RouteIcon,
  Server,
  Settings,
  ShieldAlert,
  ShipWheel,
  Sun,
  UserRound,
} from 'lucide-react'
import s from './AppShell.module.css'
import { Banner, Button } from './ui'
import { metaQuery, sessionQuery } from '@/lib/api/queries'
import { uiStore, updatePrefs } from '@/lib/store'
import { useLogout } from '@/lib/session'

interface NavItem {
  to: string
  label: string
  icon: LucideIcon
  exact?: boolean
  /** Permission required to show the item (any authenticated user when absent). */
  perm?: string
}

/** Primary navigation. Items appear only for screens the caller can use. */
export const nav: NavItem[] = [
  { to: '/', label: 'Overview', icon: LayoutDashboard, exact: true },
  { to: '/activity', label: 'Activity', icon: Activity, perm: 'activity.read' },
  { to: '/quotas', label: 'Quotas', icon: Gauge, perm: 'usage.read' },
  { to: '/usage', label: 'Usage & cost', icon: Coins, perm: 'usage.read' },
  { to: '/clients', label: 'Clients', icon: Laptop, perm: 'config.read' },
  { to: '/routing', label: 'Routing', icon: RouteIcon, perm: 'config.read' },
  { to: '/providers', label: 'Providers', icon: Server, perm: 'config.read' },
  { to: '/workspaces', label: 'Workspaces', icon: Building2, perm: 'config.read' },
  { to: '/settings', label: 'Settings', icon: Settings, perm: 'config.read' },
]

export function AppShell({ children }: { children: ReactNode }) {
  const collapsed = useStore(uiStore, (st) => st.sidebarCollapsed)
  const theme = useStore(uiStore, (st) => st.theme)
  const meta = useQuery(metaQuery)
  const session = useQuery(sessionQuery)
  const logout = useLogout()
  const [open, setOpen] = useState(false)
  const path = useRouterState({ select: (r) => r.location.pathname })
  useEffect(() => setOpen(false), [path])
  const perms = new Set(session.data?.permissions ?? [])
  const user = session.data?.user

  return (
    <div className={s.shell} data-collapsed={collapsed}>
      <a href="#main" className="skip-link">
        Skip to content
      </a>
      {open && <button className={s.scrim} aria-label="Close navigation" onClick={() => setOpen(false)} />}
      <aside className={s.sidebar} data-open={open} aria-label="Primary">
        <Link to="/" className={s.brand}>
          <span className={s.logo} aria-hidden="true">
            <ShipWheel size={17} />
          </span>
          <span className={s.label}>Harbor Pilot</span>
        </Link>
        <nav className={s.nav} aria-label="Main navigation">
          {nav
            .filter((item) => !item.perm || perms.has(item.perm))
            .map((item) => {
              const Icon = item.icon
              return (
                <Link
                  key={item.to}
                  to={item.to}
                  className={s.navLink}
                  activeOptions={{ exact: item.exact ?? false }}
                  title={collapsed ? item.label : undefined}
                >
                  <Icon size={17} aria-hidden="true" />
                  <span className={s.label}>{item.label}</span>
                </Link>
              )
            })}
        </nav>
        <div className={s.footer}>
          {user && (
            <Link to="/account" className={s.navLink} title={collapsed ? 'My account' : undefined}>
              <UserRound size={15} aria-hidden="true" />
              <span className={s.label}>{user.display_name}</span>
            </Link>
          )}
          <div className={s.footerRow}>
            <span className={s.label}>
              {meta.data ? `v${meta.data.version}` : ''}
              {meta.data?.demo ? ' · demo' : ''}
            </span>
            <span style={{ display: 'inline-flex', gap: 2 }}>
              <Button
                variant="ghost"
                size="small"
                icon={theme === 'dark' ? Sun : Moon}
                aria-label={theme === 'dark' ? 'Use light theme' : 'Use dark theme'}
                onClick={() => updatePrefs({ theme: theme === 'dark' ? 'light' : 'dark' })}
              />
              <Button
                variant="ghost"
                size="small"
                icon={collapsed ? PanelLeftOpen : PanelLeftClose}
                aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
                onClick={() => updatePrefs({ sidebarCollapsed: !collapsed })}
              />
            </span>
          </div>
          {session.data?.authenticated && (
            <div className={s.footerRow}>
              <Button variant="ghost" size="small" icon={LogOut} onClick={() => logout.mutate()}>
                <span className={s.label}>Sign out</span>
              </Button>
            </div>
          )}
        </div>
      </aside>
      <div className={s.main}>
        <div className={s.topbar}>
          <Button variant="ghost" icon={Menu} aria-label="Open navigation" onClick={() => setOpen(true)} />
          <span style={{ fontWeight: 650 }}>Harbor Pilot</span>
        </div>
        <main id="main" className={s.content} tabIndex={-1}>
          {meta.data?.demo && (
            <Banner tone="purple" role="status">
              <strong>Demo data.</strong> Every client, account, request and figure shown here is synthetic and deterministic.
              No provider is contacted and demo records are never mixed with production data.
            </Banner>
          )}
          {(session.data?.security_alerts ?? 0) > 0 && (
            <Banner tone="error" icon={ShieldAlert} role="alert">
              <strong>Security alert.</strong> Break-glass recovery was used.{' '}
              <Link to="/settings/audit">Review and acknowledge</Link>.
            </Banner>
          )}
          {children}
        </main>
      </div>
    </div>
  )
}
