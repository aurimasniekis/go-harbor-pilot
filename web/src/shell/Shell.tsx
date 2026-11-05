import { type ReactNode, useEffect } from 'react'
import { Link, useNavigate, useRouterState } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { useStore } from '@tanstack/react-store'
import { ActionIcon, Alert, AppShell, Avatar, Badge, Burger, Group, Kbd, Menu, ScrollArea, Text, Tooltip, UnstyledButton } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { Spotlight, type SpotlightActionData, spotlight } from '@mantine/spotlight'
import { ChevronsUpDown, Info, LogOut, Menu as MenuIcon, Moon, PanelLeftClose, PanelLeftOpen, Search, ShieldAlert, ShipWheel, Sun, UserRound } from 'lucide-react'
import { metaQuery, sessionQuery } from '@/lib/api/queries'
import { DegradationBanner } from '@/features/compat/Degradations'
import { uiStore, updatePrefs } from '@/lib/store'
import { useLogout } from '@/lib/session'
import { useCompact } from '@/ui/kit'
import { type NavItem, activeNavItem, bottomTabs, navItems, navSections, settingsItem } from './nav'
import s from './Shell.module.css'

const quickActions: { id: string; label: string; description: string; to: string; perm?: string }[] = [
  { id: 'connect', label: 'Connect a tool', description: 'Set up Claude Code, Codex, OpenCode or Pi', to: '/clients/setup', perm: 'config.read' },
  { id: 'new-client', label: 'Add a client', description: 'Create a client and its access token', to: '/clients', perm: 'config.write' },
  { id: 'new-provider', label: 'Add a provider', description: 'API key, local model or CLI subscription', to: '/providers', perm: 'config.write' },
  { id: 'simulate', label: 'Test a request', description: 'See where a routing plan would send it', to: '/routing/plans', perm: 'config.read' },
  { id: 'budgets', label: 'Budgets', description: 'Spend limits and how close you are', to: '/usage/budgets', perm: 'usage.read' },
  { id: 'people', label: 'People', description: 'Invite and manage users', to: '/settings/users', perm: 'users.manage' },
  { id: 'audit', label: 'Audit log', description: 'Who changed what', to: '/settings/audit', perm: 'audit.read' },
  { id: 'backups', label: 'System & backups', description: 'Version, storage and backups', to: '/settings/system', perm: 'system.manage' },
]

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

  const actions: SpotlightActionData[] = [
    ...navItems.filter((i) => can(i.perm)).map((i) => ({
      id: `go-${i.to}`,
      label: i.label,
      description: `Go to ${i.label}`,
      keywords: i.keywords,
      leftSection: <i.icon size={16} aria-hidden="true" />,
      onClick: () => void navigate({ to: i.to }),
      group: 'Pages',
    })),
    ...quickActions.filter((a) => can(a.perm)).map((a) => ({
      id: a.id,
      label: a.label,
      description: a.description,
      onClick: () => void navigate({ to: a.to }),
      group: 'Actions',
    })),
    {
      id: 'theme',
      label: theme === 'dark' ? 'Use light theme' : 'Use dark theme',
      onClick: () => updatePrefs({ theme: theme === 'dark' ? 'light' : 'dark' }),
      group: 'Actions',
    },
  ]

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
      <AppShell.Header className={s.header}>
        <Group h="100%" px="md" justify="space-between" wrap="nowrap">
          <Group gap="sm" wrap="nowrap">
            <Burger opened={opened} onClick={toggle} size="sm" aria-label={opened ? 'Close navigation' : 'Open navigation'} />
            <Brand demo={meta.data?.demo} />
          </Group>
          <ActionIcon variant="subtle" color="gray" size="lg" aria-label="Search" onClick={() => spotlight.open()}>
            <Search size={18} />
          </ActionIcon>
        </Group>
      </AppShell.Header>

      <AppShell.Navbar className={s.navbar} data-rail={rail || undefined} aria-label="Primary">
        <AppShell.Section className={s.navTop}>
          {!compact && (
            <Group justify="space-between" wrap="nowrap" gap={4} className={s.brandRow}>
              {!rail && <Brand demo={meta.data?.demo} />}
              <Tooltip label={rail ? 'Expand sidebar' : 'Collapse sidebar'} position="right">
                <ActionIcon variant="subtle" color="gray" aria-label={rail ? 'Expand sidebar' : 'Collapse sidebar'} onClick={() => updatePrefs({ sidebarCollapsed: !collapsed })}>
                  {rail ? <PanelLeftOpen size={17} /> : <PanelLeftClose size={17} />}
                </ActionIcon>
              </Tooltip>
            </Group>
          )}
          {rail ? (
            <Tooltip label="Search (Ctrl K)" position="right">
              <ActionIcon variant="default" size="lg" aria-label="Search" onClick={() => spotlight.open()} className={s.railSearch}>
                <Search size={16} />
              </ActionIcon>
            </Tooltip>
          ) : (
            <UnstyledButton className={s.search} onClick={() => spotlight.open()} aria-label="Search pages and actions">
              <Search size={14} aria-hidden="true" />
              <span>Search</span>
              <Kbd size="xs" className={s.kbd}>
                Ctrl K
              </Kbd>
            </UnstyledButton>
          )}
        </AppShell.Section>

        <AppShell.Section grow component={ScrollArea} type="never">
          <nav className={s.nav} aria-label="Main navigation">
            {navSections.map((sec, i) => {
              const items = sec.items.filter((it) => can(it.perm))
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
          <Menu position={rail ? 'right-end' : 'top-start'} width={232} withinPortal>
            <Menu.Target>
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
            </Menu.Target>
            <Menu.Dropdown>
              {user && <Menu.Label>{user.email}</Menu.Label>}
              {user && (
                <Menu.Item component={Link} to="/account" leftSection={<UserRound size={14} />}>
                  My account
                </Menu.Item>
              )}
              <Menu.Item leftSection={theme === 'dark' ? <Sun size={14} /> : <Moon size={14} />} onClick={() => updatePrefs({ theme: theme === 'dark' ? 'light' : 'dark' })}>
                {theme === 'dark' ? 'Light theme' : 'Dark theme'}
              </Menu.Item>
              {meta.data && (
                <Menu.Item leftSection={<Info size={14} />} disabled>
                  Version {meta.data.version}
                </Menu.Item>
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
        limit={12}
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
