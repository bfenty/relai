import { Outlet, Link, useLocation, useNavigate } from 'react-router-dom'
import { Settings, Menu, X, Zap, LogOut, CircleUser, ChevronDown } from 'lucide-react'
import { useState, useEffect, useRef, type ReactNode } from 'react'
import { authApi } from '../api/client'
import { useBranding } from '../branding'
import { TimeRangeProvider } from '../hooks/useTimeRange'

type NavItem = { label: string; path: string }

// One row of top navigation, grouped the way an operator works: watch the system,
// the machines and routes it runs on, who may use it. Hands-on tools sit in a menu.
const navGroups: NavItem[][] = [
  [
    { label: 'Overview', path: '/overview' },
    { label: 'Usage', path: '/usage' },
    { label: 'Performance', path: '/performance' },
    { label: 'Logs', path: '/logs' },
  ],
  [
    { label: 'Hosts', path: '/hosts' },
    { label: 'Routers', path: '/routers' },
    { label: 'MCP', path: '/mcp' },
  ],
  [{ label: 'Users', path: '/users' }],
]
const tools: NavItem[] = [
  { label: 'Chat', path: '/chat' },
  { label: 'Benchmark', path: '/benchmark' },
]

const isActive = (pathname: string, item: NavItem) => pathname.startsWith(item.path)

function TopLink({ item }: { item: NavItem }) {
  const { pathname } = useLocation()
  const active = isActive(pathname, item)
  return (
    <Link
      to={item.path}
      aria-current={active ? 'page' : undefined}
      className={`relative flex h-11 items-center px-2.5 text-[13px] transition-colors ${
        active ? 'text-slate-50' : 'text-slate-400 hover:text-slate-100'
      }`}
    >
      {item.label}
      {active && <span className="absolute inset-x-2.5 bottom-0 h-0.5 bg-accent-500" />}
    </Link>
  )
}

/** A small click-to-open menu that closes on outside click. */
function Dropdown({ button, children, align = 'left' }: {
  button: (open: boolean) => ReactNode
  children: (close: () => void) => ReactNode
  align?: 'left' | 'right'
}) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const close = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', close)
    return () => document.removeEventListener('mousedown', close)
  }, [open])
  return (
    <div ref={ref} className="relative">
      <div onClick={() => setOpen(o => !o)}>{button(open)}</div>
      {open && (
        <div className={`absolute top-full z-50 mt-1 w-44 rounded-lg border border-slate-700 bg-slate-800 py-1 shadow-popover ${align === 'right' ? 'right-0' : 'left-0'}`}>
          {children(() => setOpen(false))}
        </div>
      )}
    </div>
  )
}

const menuItem = 'flex w-full items-center gap-2 px-3 py-1.5 text-[13px] text-slate-300 hover:bg-slate-700/60 hover:text-slate-50'

function ToolsMenu() {
  const { pathname } = useLocation()
  const active = tools.some(t => isActive(pathname, t))
  return (
    <Dropdown button={open => (
      <button aria-expanded={open} className={`relative flex h-11 items-center gap-1 px-2.5 text-[13px] ${active ? 'text-slate-50' : 'text-slate-400 hover:text-slate-100'}`}>
        Tools <ChevronDown className="h-3.5 w-3.5" />
        {active && <span className="absolute inset-x-2.5 bottom-0 h-0.5 bg-accent-500" />}
      </button>
    )}>
      {close => tools.map(t => <Link key={t.path} to={t.path} onClick={close} className={menuItem}>{t.label}</Link>)}
    </Dropdown>
  )
}

function Logo() {
  const { branding } = useBranding()
  return branding.logo_url ? (
    <img src={branding.logo_url} alt="" className="h-5 w-5 object-contain" />
  ) : (
    <span className="grid h-5 w-5 place-items-center rounded bg-accent-500 text-slate-950">
      <Zap className="h-3 w-3" strokeWidth={2.5} />
    </span>
  )
}

/** "Live" with a ticking clock: the page is streaming, and this is when you are looking. */
function LiveClock() {
  const [now, setNow] = useState(() => new Date())
  useEffect(() => {
    const t = setInterval(() => setNow(new Date()), 1000)
    return () => clearInterval(t)
  }, [])
  return (
    <span className="hidden items-center gap-2 text-xs text-slate-400 sm:flex">
      <span className="live-dot" /> Live
      <span className="font-mono text-slate-300 tabular">{now.toLocaleTimeString([], { hour12: false })}</span>
    </span>
  )
}

export default function Layout() {
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [authChecked, setAuthChecked] = useState(false)
  const { branding } = useBranding()
  const navigate = useNavigate()
  const { pathname } = useLocation()

  useEffect(() => {
    authApi.me()
      .then(() => setAuthChecked(true))
      .catch(() => navigate('/login', { replace: true }))
  }, [navigate])

  // A route change closes the phone menu.
  useEffect(() => setDrawerOpen(false), [pathname])

  const logout = async () => {
    try { await authApi.logout() } catch { /* ignore */ }
    navigate('/login', { replace: true })
  }

  if (!authChecked) return null

  return (
    <div className="min-h-screen bg-slate-900 text-slate-100">
      <header className="sticky top-0 z-40 flex h-11 items-center gap-1 border-b border-slate-700 bg-slate-800 px-3 md:px-4">
        <button
          onClick={() => setDrawerOpen(o => !o)}
          className="rounded p-1.5 text-slate-400 hover:bg-slate-700 hover:text-slate-50 lg:hidden"
          aria-label={drawerOpen ? 'Close navigation' : 'Open navigation'}
        >
          {drawerOpen ? <X className="h-5 w-5" /> : <Menu className="h-5 w-5" />}
        </button>
        <Link to="/overview" className="mr-4 flex items-center gap-2">
          <Logo />
          <span className="text-[14px] font-semibold text-slate-50">{branding.app_name}</span>
        </Link>
        <nav className="hidden items-center lg:flex" aria-label="Main">
          {navGroups.map((group, i) => (
            <div key={i} className="flex items-center">
              {i > 0 && <span className="mx-1.5 h-4 w-px bg-slate-700" />}
              {group.map(item => <TopLink key={item.path} item={item} />)}
            </div>
          ))}
          <span className="mx-1.5 h-4 w-px bg-slate-700" />
          <ToolsMenu />
        </nav>
        <div className="ml-auto flex items-center gap-3">
          <LiveClock />
          <Dropdown align="right" button={open => (
            <button aria-expanded={open} className="flex h-8 items-center gap-2 rounded px-2 text-[13px] text-slate-300 hover:bg-slate-700 hover:text-slate-50">
              <CircleUser className="h-[18px] w-[18px]" />
              <span className="hidden sm:inline">admin</span>
            </button>
          )}>
            {close => (
              <>
                <Link to="/settings" onClick={close} className={menuItem}><Settings className="h-4 w-4" /> Settings</Link>
                <button onClick={logout} className={`${menuItem} hover:text-crit-300`}><LogOut className="h-4 w-4" /> Sign out</button>
              </>
            )}
          </Dropdown>
        </div>
      </header>

      {/* Narrow screens: the same links as a drop-down sheet. */}
      {drawerOpen && (
        <nav className="sticky top-11 z-30 border-b border-slate-700 bg-slate-800 px-3 py-2 lg:hidden" aria-label="Main">
          {[...navGroups, tools, [{ label: 'Settings', path: '/settings' }]].map((group, i) => (
            <div key={i} className={`flex flex-wrap ${i > 0 ? 'mt-1 border-t border-slate-700 pt-1' : ''}`}>
              {group.map(item => (
                <Link key={item.path} to={item.path}
                  className={`rounded px-3 py-2 text-[13px] ${isActive(pathname, item) ? 'bg-slate-700 text-slate-50' : 'text-slate-300'}`}>
                  {item.label}
                </Link>
              ))}
            </div>
          ))}
        </nav>
      )}

      <main className="mx-auto max-w-[1600px] px-4 py-4">
        <TimeRangeProvider>
          <Outlet />
        </TimeRangeProvider>
      </main>
    </div>
  )
}
