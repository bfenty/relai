import { useQuery } from '@tanstack/react-query'
import { dashboardApi, logsApi } from '../api/client'
import { useDashboardStream, ErrorLogEntry } from '../hooks/useDashboardStream'
import {
  Activity, Clock, DollarSign, AlertCircle, Server, Zap, ChevronRight,
  Users, Brain,
} from 'lucide-react'
import {
  BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer, Legend, Cell,
  LineChart, Line,
} from 'recharts'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useBackendColors } from '../utils/colors'
import { BackendStat } from '../types'

type Range = 'today' | 'week' | 'month'

// ── Stat card ─────────────────────────────────────────────────────────────────

function StatCard({ title, value, subtitle, icon: Icon, color }: {
  title: string
  value: string | number
  subtitle?: string
  icon: React.ElementType
  color: string
}) {
  return (
    <div className="bg-slate-800 rounded-lg p-5 border border-slate-700">
      <div className="flex items-start justify-between">
        <div>
          <p className="text-sm text-slate-400">{title}</p>
          <p className="text-2xl font-bold mt-1">{value}</p>
          {subtitle && <p className="text-xs text-slate-500 mt-1">{subtitle}</p>}
        </div>
        <div className={`p-2.5 rounded-lg ${color}`}>
          <Icon className="w-5 h-5" />
        </div>
      </div>
    </div>
  )
}

// ── Backend chart ─────────────────────────────────────────────────────────────

const CHART_METRICS = [
  { key: 'requests',             label: 'Requests' },
  { key: 'cost',                 label: 'Cost ($)' },
  { key: 'avg_latency',          label: 'Avg Latency (ms)' },
  { key: 'avg_tokens_per_second', label: 'Tok/s' },
]

function BackendChart({ data, colorsById }: {
  data: BackendStat[]
  colorsById: Map<number, string>
}) {
  const [metric, setMetric] = useState<'requests' | 'cost' | 'avg_latency' | 'avg_tokens_per_second'>('requests')
  const current = CHART_METRICS.find(m => m.key === metric)!

  return (
    <div className="bg-slate-800 rounded-lg p-5 border border-slate-700">
      <div className="flex flex-wrap items-center justify-between gap-2 mb-4">
        <h3 className="text-base font-semibold flex items-center gap-2">
          <Server className="w-4 h-4 text-slate-400" /> Host Performance
        </h3>
        <div className="flex gap-1">
          {CHART_METRICS.map(m => (
            <button key={m.key} onClick={() => setMetric(m.key as typeof metric)}
              className={`px-2 py-1 rounded text-xs ${metric === m.key
                ? 'bg-blue-600 text-white'
                : 'text-slate-400 hover:text-white hover:bg-slate-700'}`}>
              {m.label}
            </button>
          ))}
        </div>
      </div>
      <div className="h-52">
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={data} margin={{ top: 0, right: 0, bottom: 0, left: 0 }}>
            <CartesianGrid strokeDasharray="3 3" stroke="#1e293b" />
            <XAxis dataKey="backend_name" tick={{ fill: '#94a3b8', fontSize: 11 }} />
            <YAxis tick={{ fill: '#94a3b8', fontSize: 11 }} />
            <Tooltip
              contentStyle={{ backgroundColor: '#1e293b', border: '1px solid #334155', borderRadius: '6px', fontSize: 12 }}
            />
            <Legend wrapperStyle={{ fontSize: 12 }} />
            <Bar dataKey={metric} name={current.label} radius={[3, 3, 0, 0]}>
              {data.map(entry => (
                <Cell key={entry.backend_id} fill={colorsById.get(entry.backend_id) ?? '#3b82f6'} />
              ))}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      </div>
    </div>
  )
}

function UsageGraph({ data, interval = 'hour', title }: {
  data: { time: string; requests: number; tokens: number; cost: number; errors: number }[]
  interval?: 'hour' | 'day'
  title?: string
}) {
  const chartData = data.map(point => ({
    ...point,
    label: interval === 'hour'
      ? new Date(point.time).toLocaleTimeString([], { hour: 'numeric' })
      : new Date(point.time).toLocaleDateString([], { month: 'short', day: 'numeric' }),
  }))

  return (
    <div className="bg-slate-800 rounded-lg p-5 border border-slate-700">
      <div className="flex items-center justify-between mb-4">
        <h3 className="text-base font-semibold flex items-center gap-2">
          <Activity className="w-4 h-4 text-slate-400" /> {title || 'Usage Over 24h'}
        </h3>
      </div>
      <div className="h-56">
        <ResponsiveContainer width="100%" height="100%">
          <LineChart data={chartData} margin={{ top: 0, right: 8, bottom: 0, left: 0 }}>
            <CartesianGrid strokeDasharray="3 3" stroke="#1e293b" />
            <XAxis dataKey="label" tick={{ fill: '#94a3b8', fontSize: 11 }} />
            <YAxis yAxisId="left" tick={{ fill: '#94a3b8', fontSize: 11 }} />
            <YAxis yAxisId="right" orientation="right" tick={{ fill: '#94a3b8', fontSize: 11 }} />
            <Tooltip
              contentStyle={{ backgroundColor: '#1e293b', border: '1px solid #334155', borderRadius: '6px', fontSize: 12 }}
              labelFormatter={(_, items) => items?.[0]?.payload?.time ? new Date(items[0].payload.time).toLocaleString() : ''}
            />
            <Legend wrapperStyle={{ fontSize: 12 }} />
            <Line yAxisId="left" type="monotone" dataKey="tokens" name="Tokens" stroke="#22c55e" strokeWidth={2} dot={false} />
            <Line yAxisId="right" type="monotone" dataKey="requests" name="Requests" stroke="#60a5fa" strokeWidth={2} dot={false} />
            <Line yAxisId="right" type="monotone" dataKey="errors" name="Errors" stroke="#f87171" strokeWidth={2} dot={false} />
          </LineChart>
        </ResponsiveContainer>
      </div>
    </div>
  )
}

function Leaderboard<T>({ title, icon: Icon, rows, empty, render }: {
  title: string
  icon: React.ElementType
  rows: T[]
  empty: string
  render: (row: T, index: number) => React.ReactNode
}) {
  return (
    <div className="bg-slate-800 rounded-lg p-5 border border-slate-700">
      <h3 className="text-base font-semibold mb-4 flex items-center gap-2">
        <Icon className="w-4 h-4 text-slate-400" /> {title}
      </h3>
      <div className="space-y-2">
        {rows.length ? rows.slice(0, 6).map(render) : (
          <p className="text-slate-500 text-sm">{empty}</p>
        )}
      </div>
    </div>
  )
}

// ── Page ──────────────────────────────────────────────────────────────────────

export default function Dashboard() {
  const [range, setRange] = useState<Range>('today')
  const colorsById = useBackendColors()
  useDashboardStream()

  const { data: stats, isLoading, isFetching } = useQuery({
    queryKey: ['dashboard-stats', range],
    queryFn: () => dashboardApi.stats(range),
    refetchInterval: 30000,
    placeholderData: (prev) => prev,
  })

  const { data: errorLogs } = useQuery({
    queryKey: ['recent-errors'],
    queryFn: () => logsApi.list({ limit: 5, errors_only: true }),
    refetchInterval: 30000,
  })

  const { data: liveErrors } = useQuery<ErrorLogEntry[]>({
    queryKey: ['live-errors'],
    initialData: [],
  })

  if (isLoading && !stats) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="animate-spin w-8 h-8 border-2 border-blue-400 border-t-transparent rounded-full" />
      </div>
    )
  }

  const requests = range === 'today' ? stats?.total_requests_today
    : range === 'week'  ? stats?.total_requests_week
    : stats?.total_requests_month

  const cost = range === 'today' ? stats?.total_cost_today
    : range === 'week'  ? stats?.total_cost_week
    : stats?.total_cost_month

  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-3 mb-6">
        <div className="flex items-center gap-3">
          <h2 className="text-2xl font-bold">Dashboard</h2>
          <span className="flex items-center gap-1.5 text-xs text-slate-400">
            <span className={`w-2 h-2 rounded-full ${isFetching ? 'bg-blue-400 animate-pulse' : 'bg-green-400'}`} />
            {isFetching ? 'updating…' : 'live'}
          </span>
        </div>
        <div className="flex items-center gap-2">
          <Link to="/analytics" className="text-xs text-slate-400 hover:text-blue-400 flex items-center gap-0.5">
            Detailed analytics <ChevronRight className="w-3 h-3" />
          </Link>
          <div className="flex gap-1 bg-slate-800 border border-slate-700 rounded-lg p-1">
            {(['today', 'week', 'month'] as Range[]).map(r => (
              <button key={r} onClick={() => setRange(r)}
                className={`px-3 py-1 rounded text-sm capitalize transition-colors ${
                  range === r ? 'bg-blue-600 text-white' : 'text-slate-400 hover:text-white'
                }`}>
                {r === 'today' ? 'Today' : r === 'week' ? '7 days' : '30 days'}
              </button>
            ))}
          </div>
        </div>
      </div>

      <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-4 mb-6">
        <StatCard
          title="Requests"
          value={(requests ?? 0).toLocaleString()}
          icon={Activity}
          color="bg-blue-500/20 text-blue-400"
        />
        <StatCard
          title="Cost"
          value={`$${(cost ?? 0).toFixed(4)}`}
          icon={DollarSign}
          color="bg-green-500/20 text-green-400"
        />
        <StatCard
          title="Avg Latency"
          value={`${Math.round(stats?.avg_latency_ms ?? 0)}ms`}
          icon={Clock}
          color="bg-yellow-500/20 text-yellow-400"
        />
        <StatCard
          title="Tokens Today"
          value={(stats?.total_tokens_today ?? 0).toLocaleString()}
          icon={Zap}
          color="bg-purple-500/20 text-purple-400"
        />
        <StatCard
          title="Tok/s (today)"
          value={`${(stats?.avg_tokens_per_second_today ?? 0).toFixed(1)}`}
          subtitle="output tokens / latency"
          icon={Zap}
          color="bg-cyan-500/20 text-cyan-400"
        />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4 mb-4">
        <div className="lg:col-span-2">
          <UsageGraph
            data={stats?.usage_series ?? []}
            interval={stats?.series_interval ?? 'hour'}
            title={
              range === 'today'
                ? 'Usage Over 24h'
                : stats?.usage_series.length === 7
                  ? 'Usage Over 7 Days'
                  : 'Usage Over 30 Days'
            }
          />
        </div>
        <Leaderboard
          title="Top Users"
          icon={Users}
          rows={stats?.top_users ?? []}
          empty="No user usage yet"
          render={(user, i) => (
            <div key={user.user_id} className="flex items-center justify-between py-1.5">
              <div className="flex items-center gap-2">
                <span className="text-xs text-slate-500 w-4">{i + 1}</span>
                <span className="text-sm font-medium">{user.user_name}</span>
              </div>
              <div className="text-right text-xs text-slate-400">
                <div>{user.tokens.toLocaleString()} tok</div>
                <div>{user.requests.toLocaleString()} req</div>
              </div>
            </div>
          )}
        />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4 mb-4">
        <div className="lg:col-span-2">
          <BackendChart data={stats?.backend_stats ?? []} colorsById={colorsById} />
        </div>

        <Leaderboard
          title="Top API Keys"
          icon={Zap}
          rows={stats?.top_api_keys ?? []}
          empty="No key usage yet"
          render={(key, i) => (
            <div key={key.api_key_id} className="flex items-center justify-between py-1.5">
                <div className="flex items-center gap-2">
                  <span className="text-xs text-slate-500 w-4">{i + 1}</span>
                  <span className="text-sm font-medium">{key.key_name}</span>
                </div>
                <div className="text-right text-xs text-slate-400">
                  <div>{key.tokens.toLocaleString()} tok</div>
                  <div>{key.requests.toLocaleString()} req</div>
                </div>
              </div>
          )}
        />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4 mb-4">
        <Leaderboard
          title="Top Models"
          icon={Brain}
          rows={stats?.top_models ?? []}
          empty="No model usage yet"
          render={(model, i) => (
            <div key={model.model} className="flex items-center justify-between py-1.5">
              <div className="flex items-center gap-2 min-w-0">
                <span className="text-xs text-slate-500 w-4 shrink-0">{i + 1}</span>
                <span className="text-sm font-medium truncate">{model.model}</span>
              </div>
              <div className="text-right text-xs text-slate-400 shrink-0">
                <div>{model.tokens.toLocaleString()} tok</div>
                <div>{model.requests.toLocaleString()} req</div>
              </div>
            </div>
          )}
        />
      </div>

      {/* Live errors via SSE */}
      {liveErrors?.length > 0 && (
        <div className="bg-slate-800 rounded-lg p-5 border border-slate-700 mb-4">
          <div className="flex items-center justify-between mb-4">
            <h3 className="text-base font-semibold flex items-center gap-2">
              <AlertCircle className="w-4 h-4 text-red-400" /> Live Errors
              <span className="ml-2 flex items-center gap-1">
                <span className="w-2 h-2 bg-green-400 rounded-full animate-pulse" />
                <span className="text-xs text-slate-400">real-time</span>
              </span>
            </h3>
            <Link to="/logs?errors_only=true" className="text-xs text-slate-400 hover:text-blue-400 flex items-center gap-0.5">
              View all <ChevronRight className="w-3 h-3" />
            </Link>
          </div>
          <div className="space-y-2 max-h-48 overflow-y-auto">
            {liveErrors.slice(0, 10).map((err, i) => (
              <div key={i} className="flex items-start gap-3 py-2 border-b border-slate-700/50 last:border-0">
                <span className="text-xs text-slate-500 whitespace-nowrap">
                  {new Date(err.created_at).toLocaleTimeString()}
                </span>
                <span className="text-xs bg-red-500/20 text-red-400 px-1.5 py-0.5 rounded font-mono">
                  {err.status_code}
                </span>
                <span className="text-sm font-mono text-slate-300">{err.model}</span>
                <span className="text-xs text-red-400 flex-1 truncate">{err.error_message}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Recent errors */}
      <div className="bg-slate-800 rounded-lg p-5 border border-slate-700">
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-base font-semibold flex items-center gap-2">
            <AlertCircle className="w-4 h-4 text-red-400" /> Recent Errors
          </h3>
          <Link to="/logs?errors_only=true" className="text-xs text-slate-400 hover:text-blue-400 flex items-center gap-0.5">
            View all <ChevronRight className="w-3 h-3" />
          </Link>
        </div>
        <div className="space-y-2">
          {errorLogs?.data?.length ? errorLogs.data.map(log => (
            <div key={log.id} className="flex items-start gap-3 py-2 border-b border-slate-700/50 last:border-0">
              <span className="text-xs text-slate-500 whitespace-nowrap mt-0.5">
                {new Date(log.created_at).toLocaleTimeString()}
              </span>
              <span className="text-sm font-mono text-slate-300">{log.model}</span>
              <span className="text-xs text-red-400 flex-1 truncate">{log.error_message || `HTTP ${log.status_code}`}</span>
            </div>
          )) : (
            <p className="text-sm text-slate-500">No recent errors</p>
          )}
        </div>
      </div>
    </div>
  )
}
