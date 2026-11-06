import { type ReactNode, useEffect } from 'react'
import { Link, useNavigate, useRouterState } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { useStore } from '@tanstack/react-store'
import { ActionIcon, Alert, AppShell, Avatar, Badge, Burger, Group, Kbd, Menu, ScrollArea, Text, Tooltip, UnstyledButton } from '@mantine/core'
import { useDisclosure, useOs, useViewportSize } from '@mantine/hooks'
import { notifications } from '@mantine/notifications'
import { Spotlight, type SpotlightActionData, spotlight } from '@mantine/spotlight'
import { Check, ChevronsUpDown, Info, LogOut, Menu as MenuIcon, Monitor, Moon, PanelLeftClose, PanelLeftOpen, Search, ShieldAlert, ShipWheel, Sun, UserRound } from 'lucide-react'
import { metaQuery, sessionQuery } from '@/lib/api/queries'
import { DegradationBanner } from '@/features/compat/Degradations'
import { uiStore, updatePrefs } from '@/lib/store'
import { useLogout } from '@/lib/session'
import { useCompact } from '@/ui/kit'
import { themeChoices } from '@/lib/theme'
import type { ThemePref } from '@/lib/store'
import { type NavItem, activeNavItem, bottomTabs, navItems, navSections, settingsItem } from './nav'
import { friendlyVersion, matchesQuery, paletteEntries, shortcutLabel } from './palette'
import s from './Shell.module.css'


export function Shell({ children }: { children: ReactNode }) {
  const collapsed = useStore(uiStore, (st) => st.sidebarCollapsed)
  const theme = useStore(uiStore, (st) => st.theme)
  const compact = useCompact()
  const [opened, { toggle, close }] = useDisclosure(false)
  const meta = useQuery(metaQuery)
  const session = useQuery(sessionQuery)
  const logout = useLogout()
  const navigate = useNavigate()
  const path = useRouterState({ select: (r) => r.location.pathname })
  useEffect(close, [path, close])
  const perms = new Set(session.data?.permissions ?? [])
  const can = (perm?: string) => !perm || perms.has(perm)
  const user = session.data?.user
  const rail = collapsed && !compact
  const active = activeNavItem(path)

  const os = useOs()
  const viewport = useViewportSize()
  const kbd = shortcutLabel(os)
  const actions: SpotlightActionData[] = paletteEntries(can, theme, !!session.data?.authenticated && !!user).map((e) => ({
    id: e.id,
    label: e.label,
    description: e.description,
    keywords: e.keywords,
    leftSection: <e.icon size={16} aria-hidden="true" />,
    group: e.group,
    onClick: () => {
      const r = e.run
      if (r.kind === 'go') void navigate({ to: r.to, hash: r.hash })
      else if (r.kind === 'theme') updatePrefs({ theme: r.value })
      else logout.mutate()
    },
  }))
  const version = friendlyVersion(meta.data?.version)
  const copyVersion = () => {
    if (!meta.data?.version) return
    void navigator.clipboard?.writeText(meta.data.version).then(
      () => notifications.show({ message: `Copied ${meta.data?.version}`, autoClose: 2500 }),
      () => undefined,
    )
  }
  const themeIcon = (t: ThemePref) => (t === 'auto' ? <Monitor size={14} /> : t === 'light' ? <Sun size={14} /> : <Moon size={14} />)

  const navLink = (item: NavItem) => {
    const Icon = item.icon
    const isActive = active?.to === item.to
    const link = (
      <Link
        key={item.to}
        to={item.to}
        className={s.navLink}
        data-active={isActive || undefined}
        aria-current={isActive ? 'page' : undefined}
        aria-label={rail ? item.label : undefined}
      >
        <Icon size={16} aria-hidden="true" className={s.navIcon} />
        {!rail && <span className={s.navLabel}>{item.label}</span>}
      </Link>
    )
    return rail ? (
      <Tooltip key={item.to} label={item.label} position="right">
        {link}
      </Tooltip>
    ) : (
      link
    )
  }

  const tabs = bottomTabs.map((to) => navItems.find((i) => i.to === to)!).filter((i) => can(i.perm))

  return (
    <AppShell
      header={{ height: 52, collapsed: !compact }}
      footer={compact ? { height: 60 } : undefined}
      navbar={{ width: rail ? 60 : 228, breakpoint: 'sm', collapsed: { mobile: !opened } }}
      padding={0}
      className={s.shell}
      style={{ ['--hp-nav-w' as string]: compact ? '0px' : `${rail ? 60 : 228}px` }}
    >
      <a href="#main" className="skip-link">
        Skip to content
      </a>
      {/* The phone header exists only at phone widths: a collapsed Mantine
          header is merely moved off-screen and would stay in the tab order. */}
      {compact && (
        <AppShell.Header className={s.header}>
          <Group h="100%" px="md" justify="space-between" wrap="nowrap">
            <Group gap="sm" wrap="nowrap">
              <Burger opened={opened} onClick={toggle} size="sm" className={s.burger} aria-label={opened ? 'Close navigation' : 'Open navigation'} />
              <Brand demo={meta.data?.demo} />
            </Group>
            <ActionIcon variant="subtle" color="gray" size="lg" aria-label="Search" onClick={() => spotlight.open()}>
              <Search size={18} />
            </ActionIcon>
          </Group>
        </AppShell.Header>
      )}

      <AppShell.Navbar className={s.navbar} data-rail={rail || undefined} data-compact={compact || undefined} aria-label="Primary">
        {/* On phones the drawer only adds what the bottom bar and the header
            (search) do not already offer. */}
        {!compact && (
          <AppShell.Section className={s.navTop}>
            {rail ? (
              <>
                <Tooltip label="Home" position="right">
                  <Link to="/" className={s.railLogo} aria-label="Harbor Pilot home">
                    <ShipWheel size={16} />
                  </Link>
                </Tooltip>
                <Tooltip label="Expand sidebar" position="right">
                  <ActionIcon variant="subtle" color="gray" aria-label="Expand sidebar" onClick={() => updatePrefs({ sidebarCollapsed: false })}>
                    <PanelLeftOpen size={17} />
                  </ActionIcon>
                </Tooltip>
                <Tooltip label={`Search (${kbd})`} position="right">
                  <ActionIcon variant="subtle" color="gray" size="lg" aria-label="Search" onClick={() => spotlight.open()}>
                    <Search size={16} />
                  </ActionIcon>
                </Tooltip>
              </>
            ) : (
              <>
                <Group justify="space-between" wrap="nowrap" gap={4} className={s.brandRow}>
                  <Brand demo={meta.data?.demo} />
                  <Tooltip label="Collapse sidebar" position="right">
                    <ActionIcon variant="subtle" color="gray" aria-label="Collapse sidebar" onClick={() => updatePrefs({ sidebarCollapsed: true })}>
                      <PanelLeftClose size={17} />
                    </ActionIcon>
                  </Tooltip>
                </Group>
                <UnstyledButton className={s.search} onClick={() => spotlight.open()} aria-label="Search pages and actions">
                  <Search size={14} aria-hidden="true" />
                  <span>Search</span>
                  <Kbd size="xs" className={s.kbd}>
                    {kbd}
                  </Kbd>
                </UnstyledButton>
              </>
            )}
          </AppShell.Section>
        )}

        <AppShell.Section grow component={ScrollArea} type="never">
          <nav className={s.nav} aria-label="Main navigation">
            {navSections.map((sec, i) => {
              const items = sec.items.filter((it) => can(it.perm) && !(compact && bottomTabs.includes(it.to)))
              if (items.length === 0) return null
              return (
                <div key={sec.label ?? i} className={s.navSection}>
                  {sec.label && (rail ? <div className={s.railDivider} aria-hidden="true" /> : <div className={s.sectionLabel}>{sec.label}</div>)}
                  {items.map(navLink)}
                </div>
              )
            })}
          </nav>
        </AppShell.Section>

        <AppShell.Section className={s.navFooter}>
          {can(settingsItem.perm) && <div className={s.navSection}>{navLink(settingsItem)}</div>}
          <Menu position={rail ? 'right-end' : 'top-start'} width={rail ? 232 : 'target'} withinPortal>
            <Menu.Target>
              <Tooltip label={`${user?.display_name ?? 'Service'} · account menu`} position="right" disabled={!rail}>
              <UnstyledButton className={s.user} aria-label="Account menu">
                <Avatar size={26} radius="xl" color="blue" variant="light">
                  {(user?.display_name ?? user?.email ?? '?').slice(0, 1).toUpperCase()}
                </Avatar>
                {!rail && (
                  <>
                    <span className={s.userText}>
                      <Text size="sm" fw={500} truncate>
                        {user?.display_name ?? 'Service'}
                      </Text>
                      <Text size="xs" c="dimmed" truncate>
                        {session.data?.role ? session.data.role.charAt(0).toUpperCase() + session.data.role.slice(1) : ''}
                      </Text>
                    </span>
                    <ChevronsUpDown size={14} aria-hidden="true" className={s.userChevron} />
                  </>
                )}
              </UnstyledButton>
              </Tooltip>
            </Menu.Target>
            <Menu.Dropdown>
              {user && <Menu.Label>{user.email}</Menu.Label>}
              {user && (
                <Menu.Item component={Link} to="/account" leftSection={<UserRound size={14} />}>
                  My account
                </Menu.Item>
              )}
              <Menu.Label>Theme</Menu.Label>
              {themeChoices.map((c) => (
                <Menu.Item
                  key={c.value}
                  leftSection={themeIcon(c.value)}
                  rightSection={theme === c.value ? <Check size={14} aria-hidden="true" /> : undefined}
                  aria-checked={theme === c.value}
                  role="menuitemradio"
                  onClick={() => updatePrefs({ theme: c.value })}
                >
                  {c.label}
                </Menu.Item>
              ))}
              {version && (
                <>
                  <Menu.Divider />
                  <Menu.Item leftSection={<Info size={14} />} onClick={copyVersion} title="Copy the full version">
                    <Text size="xs" c="dimmed" span>
                      Harbor Pilot {version}
                    </Text>
                  </Menu.Item>
                </>
              )}
              {session.data?.authenticated && (
                <>
                  <Menu.Divider />
                  <Menu.Item leftSection={<LogOut size={14} />} onClick={() => logout.mutate()}>
                    Sign out
                  </Menu.Item>
                </>
              )}
            </Menu.Dropdown>
          </Menu>
        </AppShell.Section>
      </AppShell.Navbar>

      <AppShell.Main className={s.main} id="main" tabIndex={-1}>
        <div className={s.content}>
          {(session.data?.security_alerts ?? 0) > 0 && (
            <Alert color="red" variant="light" icon={<ShieldAlert size={16} />} role="alert" py="xs">
              Break-glass recovery was used. <Link to="/settings/audit">Review and acknowledge</Link>
            </Alert>
          )}
          <DegradationBanner />
          {children}
        </div>
      </AppShell.Main>

      {compact && (
        <AppShell.Footer className={s.bottomBar}>
          <nav aria-label="Quick navigation" className={s.bottomNav}>
            {tabs.map((t) => (
              <Link key={t.to} to={t.to} className={s.bottomTab} data-active={active?.to === t.to || undefined} aria-current={active?.to === t.to ? 'page' : undefined}>
                <t.icon size={20} aria-hidden="true" />
                <span>{t.label}</span>
              </Link>
            ))}
            <UnstyledButton className={s.bottomTab} onClick={toggle} aria-label="More pages">
              <MenuIcon size={20} aria-hidden="true" />
              <span>More</span>
            </UnstyledButton>
          </nav>
        </AppShell.Footer>
      )}

      <Spotlight
        actions={actions}
        shortcut={['mod + K']}
        nothingFound="Nothing found"
        highlightQuery
        searchProps={{ leftSection: <Search size={16} />, placeholder: 'Search pages and actions' }}
        filter={(query, items) =>
          (items as SpotlightActionData[]).filter((a) => matchesQuery([a.label, a.description, ...((a.keywords as string[] | undefined) ?? [])], query))
        }
        limit={60}
        fullScreen={compact}
        scrollable
        maxHeight={compact ? Math.max(240, viewport.height - 64) : 480}
        styles={compact ? { body: { height: '100dvh' }, actionsList: { maxHeight: `${Math.max(240, viewport.height - 64)}px` } } : undefined}
      />
    </AppShell>
  )
}

function Brand({ demo }: { demo?: boolean }) {
  return (
    <Group gap={8} wrap="nowrap" className={s.brand}>
      <Link to="/" className={s.brandLink}>
        <span className={s.logo} aria-hidden="true">
          <ShipWheel size={16} />
        </span>
        <span>Harbor Pilot</span>
      </Link>
      {demo && (
        <Tooltip label="Demo data: every record is synthetic and no provider is contacted.">
          <Badge size="xs" color="violet" variant="light" tabIndex={0}>
            Demo
          </Badge>
        </Tooltip>
      )}
    </Group>
  )
}
