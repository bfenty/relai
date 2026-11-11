import { Outlet, Link, useLocation, useNavigate } from 'react-router-dom'
import { LayoutDashboard, Server, ScrollText, Network, Settings, Menu, X, Zap, MessageSquare, Gauge, Users, ListVideo, LogOut, Puzzle, BarChart3 } from 'lucide-react'
import { useState, useEffect } from 'react'
import { authApi } from '../api/client'
import { useBranding } from '../branding'

const navItems = [
  { label: 'Dashboard', path: '/dashboard', icon: LayoutDashboard },
  { label: 'Analytics', path: '/analytics', icon: BarChart3 },
  { label: 'Queue',     path: '/queue',     icon: ListVideo },
  { label: 'Hosts',     path: '/hosts',     icon: Server },
  { label: 'Routers',   path: '/routers',   icon: Network },
  { label: 'Users',     path: '/users',     icon: Users },
  { label: 'MCP',       path: '/mcp',       icon: Puzzle },
  { label: 'Logs',      path: '/logs',      icon: ScrollText },
  { label: 'Chat',      path: '/chat',      icon: MessageSquare },
  { label: 'Benchmark', path: '/benchmark', icon: Gauge },
]

const bottomNavItems = [
  { label: 'Settings', path: '/settings', icon: Settings },
]

function NavLinks({ onClick, onLogout }: { onClick?: () => void; onLogout: () => void }) {
  const location = useLocation()
  const { branding } = useBranding()
  return (
    <nav className="flex flex-col h-full">
      <div className="flex-1">
        {navItems.map(({ label, path, icon: Icon }) => {
          const active = location.pathname.startsWith(path)
          return (
            <Link key={path} to={path} onClick={onClick}
              className={`flex items-center gap-3 px-6 py-3 text-sm transition-colors ${
                active
                  ? 'border-r-2'
                  : 'text-slate-400 hover:text-slate-200 hover:bg-slate-700/50'
              }`}
              style={active ? {
                backgroundColor: `${branding.primary_color}20`,
                borderColor: branding.accent_color,
                color: branding.accent_color,
              } : undefined}
            >
              <Icon className="w-4 h-4" />
              {label}
            </Link>
          )
        })}
      </div>
      <div className="border-t border-slate-700/50">
        {bottomNavItems.map(({ label, path, icon: Icon }) => {
          const active = location.pathname.startsWith(path)
          return (
            <Link key={path} to={path} onClick={onClick}
              className={`flex items-center gap-3 px-6 py-3 text-sm transition-colors ${
                active
                  ? 'border-r-2'
                  : 'text-slate-400 hover:text-slate-200 hover:bg-slate-700/50'
              }`}
              style={active ? {
                backgroundColor: `${branding.primary_color}20`,
                borderColor: branding.accent_color,
                color: branding.accent_color,
              } : undefined}
            >
              <Icon className="w-4 h-4" />
              {label}
            </Link>
          )
        })}
        <button
          onClick={onLogout}
          className="w-full flex items-center gap-3 px-6 py-3 text-sm text-slate-400 hover:text-red-400 hover:bg-slate-700/50 transition-colors"
        >
          <LogOut className="w-4 h-4" />
          Sign out
        </button>
      </div>
    </nav>
  )
}

export default function Layout() {
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [authChecked, setAuthChecked] = useState(false)
  const { branding } = useBranding()
  const close = () => setDrawerOpen(false)
  const navigate = useNavigate()

  useEffect(() => {
    authApi.me()
      .then(() => setAuthChecked(true))
      .catch(() => navigate('/login', { replace: true }))
  }, [navigate])

  const logout = async () => {
    try { await authApi.logout() } catch { /* ignore */ }
    navigate('/login', { replace: true })
  }

  if (!authChecked) return null

  return (
    <div className="min-h-screen bg-slate-900 text-slate-100">

      {/* ── Mobile top bar ── */}
      <header className="md:hidden fixed top-0 left-0 right-0 z-30 h-14 bg-slate-800 border-b border-slate-700 flex items-center px-4 gap-3">
        <button onClick={() => setDrawerOpen(true)} className="p-1 text-slate-400 hover:text-white">
          <Menu className="w-5 h-5" />
        </button>
        {branding.logo_url ? (
          <img src={branding.logo_url} alt="" className="w-5 h-5 object-contain" />
        ) : (
          <Zap className="w-5 h-5" style={{ color: branding.accent_color }} />
        )}
        <span className="font-bold tracking-tight">{branding.app_name}</span>
      </header>

      {/* ── Mobile drawer backdrop ── */}
      {drawerOpen && (
        <div
          className="md:hidden fixed inset-0 z-40 bg-black/60"
          onClick={close}
        />
      )}

      {/* ── Sidebar (drawer on mobile, fixed on desktop) ── */}
      <aside className={`
        fixed top-0 left-0 h-full w-60 z-50
        bg-slate-800 border-r border-slate-700
        transform transition-transform duration-200
        md:translate-x-0
        ${drawerOpen ? 'translate-x-0' : '-translate-x-full'}
      `}>
        {/* Desktop logo */}
        <div className="hidden md:flex p-6 pb-4 items-center gap-2">
          {branding.logo_url ? (
            <img src={branding.logo_url} alt="" className="w-7 h-7 object-contain" />
          ) : (
            <Zap className="w-7 h-7" style={{ color: branding.accent_color }} />
          )}
          <span className="text-xl font-bold tracking-tight">{branding.app_name}</span>
        </div>

        {/* Mobile drawer header */}
        <div className="md:hidden flex items-center justify-between px-6 h-14 border-b border-slate-700">
          <div className="flex items-center gap-2">
            {branding.logo_url ? (
              <img src={branding.logo_url} alt="" className="w-5 h-5 object-contain" />
            ) : (
              <Zap className="w-5 h-5" style={{ color: branding.accent_color }} />
            )}
            <span className="font-bold tracking-tight">{branding.app_name}</span>
          </div>
          <button onClick={close} className="p-1 text-slate-400 hover:text-white">
            <X className="w-5 h-5" />
          </button>
        </div>

        <div className="mt-2 h-[calc(100vh-88px)] md:h-[calc(100vh-88px)]">
          <NavLinks onClick={close} onLogout={logout} />
        </div>
      </aside>

      {/* ── Main content ── */}
      <main className="md:ml-60 pt-14 md:pt-0 p-4 md:p-8">
        <div className="max-w-7xl mx-auto">
          <Outlet />
        </div>
      </main>
    </div>
  )
}
