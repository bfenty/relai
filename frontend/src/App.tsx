import { Routes, Route, Navigate } from 'react-router-dom'
import Layout from './components/Layout'
import Login from './pages/Login'
import Dashboard from './pages/Dashboard'
import Analytics from './pages/Analytics'
import Hosts from './pages/Hosts'
import Routers from './pages/Routers'
import Logs from './pages/Logs'
import Settings from './pages/Settings'
import Chat from './pages/Chat'
import Benchmark from './pages/Benchmark'
import Users from './pages/Users'
import Queue from './pages/Queue'
import MCP from './pages/MCP'

function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/" element={<Layout />}>
        <Route index element={<Navigate to="/dashboard" replace />} />
        <Route path="dashboard" element={<Dashboard />} />
        <Route path="analytics" element={<Analytics />} />
        <Route path="hosts" element={<Hosts />} />
        <Route path="routers" element={<Routers />} />
        <Route path="users" element={<Users />} />
        <Route path="mcp" element={<MCP />} />
        <Route path="logs" element={<Logs />} />
        <Route path="settings" element={<Settings />} />
        <Route path="chat" element={<Chat />} />
        <Route path="benchmark" element={<Benchmark />} />
        <Route path="queue" element={<Queue />} />
      </Route>
    </Routes>
  )
}

export default App
