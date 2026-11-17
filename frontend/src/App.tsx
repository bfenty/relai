import { lazy, Suspense } from 'react'
import { Routes, Route, Navigate } from 'react-router-dom'
import Layout from './components/Layout'
import Login from './pages/Login'

// Pages load on first visit, so the first screen doesn't wait for every page (and
// the charting library) to download and parse. Layout and Login stay eager: one is
// always on screen, the other is where a signed-out visit lands.
const Overview = lazy(() => import('./pages/Overview'))
const Usage = lazy(() => import('./pages/Usage'))
const Performance = lazy(() => import('./pages/Performance'))
const Hosts = lazy(() => import('./pages/Hosts'))
const HostDetail = lazy(() => import('./pages/Hosts').then(m => ({ default: m.HostDetail })))
const Routers = lazy(() => import('./pages/Routers'))
const LegacyRoutingRules = lazy(() => import('./pages/Routers').then(m => ({ default: m.LegacyRoutingRules })))
const Logs = lazy(() => import('./pages/Logs'))
const Settings = lazy(() => import('./pages/Settings'))
const Chat = lazy(() => import('./pages/Chat'))
const Benchmark = lazy(() => import('./pages/Benchmark'))
const Users = lazy(() => import('./pages/Users'))
const MCP = lazy(() => import('./pages/MCP'))

function PageLoading() {
  return (
    <div className="flex h-64 items-center justify-center">
      <div className="h-6 w-6 animate-spin rounded-full border-2 border-accent-400 border-t-transparent" />
    </div>
  )
}

const page = (el: JSX.Element) => <Suspense fallback={<PageLoading />}>{el}</Suspense>

function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/" element={<Layout />}>
        <Route index element={<Navigate to="/overview" replace />} />
        <Route path="overview" element={page(<Overview />)} />
        <Route path="usage" element={page(<Usage />)} />
        <Route path="performance" element={page(<Performance />)} />
        <Route path="hosts" element={page(<Hosts />)} />
        <Route path="hosts/:id" element={page(<HostDetail />)} />
        <Route path="routers" element={page(<Routers />)} />
        <Route path="routers/legacy-rules" element={page(<LegacyRoutingRules />)} />
        <Route path="users" element={page(<Users />)} />
        <Route path="mcp" element={page(<MCP />)} />
        <Route path="logs" element={page(<Logs />)} />
        <Route path="settings" element={page(<Settings />)} />
        <Route path="chat" element={page(<Chat />)} />
        <Route path="benchmark" element={page(<Benchmark />)} />
        {/* Earlier addresses, kept working for bookmarks. */}
        <Route path="dashboard" element={<Navigate to="/overview" replace />} />
        <Route path="queue" element={<Navigate to="/overview" replace />} />
        <Route path="analytics" element={<Navigate to="/usage" replace />} />
      </Route>
    </Routes>
  )
}

export default App
