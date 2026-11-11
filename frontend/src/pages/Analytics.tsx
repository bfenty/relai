import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import RoutingHealth from '../components/RoutingHealth'
import RoutingCharts from '../components/RoutingCharts'
import PrefixHealth from '../components/PrefixHealth'
import ThroughputCharts from '../components/ThroughputCharts'
import Section from '../components/Section'
import { Link } from 'react-router-dom'
import {
  BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer, Legend,
} from 'recharts'
import { Database,
  BarChart3, Activity, Zap, DollarSign, AlertTriangle, ChevronRight, Share2, SearchCheck, Gauge,
} from 'lucide-react'
import { analyticsApi } from '../api/client'
import { CacheTelemetryCard, useCacheTelemetry } from '../components/CacheTelemetry'
import { useDashboardStream } from '../hooks/useDashboardStream'
import {
  AnalyticsGroupBy, AnalyticsInterval, AnalyticsMetric, AnalyticsGroup,
} from '../types'

// ── Constants ───────────────────────────────────────────────────────────────

const CHART_PALETTE = [
  '#3b82f6', '#22c55e', '#f59e0b', '#ec4899', '#8b5cf6', '#06b6d4',
  '#f97316', '#10b981', '#e11d48', '#a78bfa', '#eab308', '#14b8a6',
  '#f43f5e', '#6366f1', '#84cc16',
]
const OTHER_COLOR = '#64748b'

const GROUP_OPTIONS: { value: AnalyticsGroupBy; label: string }[] = [
  { value: 'model',   label: 'Model' },
  { value: 'user',    label: 'User' },
  { value: 'api_key', label: 'API Key' },
  { value: 'backend', label: 'Host' },
]

const METRIC_OPTIONS: { value: AnalyticsMetric; label: string }[] = [
  { value: 'tokens',   label: 'Tokens' },
  { value: 'requests', label: 'Requests' },
  { value: 'cost',     label: 'Cost' },
]

const INTERVAL_OPTIONS: { value: AnalyticsInterval | 'auto'; label: string }[] = [
  { value: 'auto', label: 'Auto' },
  { value: 'hour', label: 'Hourly' },
  { value: 'day',  label: 'Daily' },
  { value: 'week', label: 'Weekly' },
]

const PRESETS: { value: string; label: string; days: number }[] = [
  { value: '24h', label: '24h', days: 1 },
  { value: '7d',  label: '7 days',  days: 7 },
  { value: '30d', label: '30 days', days: 30 },
  { value: '90d', label: '90 days', days: 90 },
]

// ── Helpers ─────────────────────────────────────────────────────────────────

function colorForGroup(key: string, index: number): string {
  if (key === '__other__') return OTHER_COLOR
  return CHART_PALETTE[index % CHART_PALETTE.length]
}

function toDateInput(d: Date): string {
  // local YYYY-MM-DD for <input type="date">
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

function computeRange(preset: string, fromDate: string, toDate: string): { from: string; to: string } {
  const now = new Date()
  if (preset === 'custom' && fromDate && toDate) {
    const from = new Date(`${fromDate}T00:00:00`)
    const to = new Date(`${toDate}T23:59:59`)
    return { from: from.toISOString(), to: to.toISOString() }
  }
  const days = PRESETS.find(p => p.value === preset)?.days ?? 7
  const from = new Date(now.getTime() - days * 24 * 60 * 60 * 1000)
  return { from: from.toISOString(), to: now.toISOString() }
}

function formatMetric(value: number, metric: AnalyticsMetric): string {
  if (metric === 'cost') return `$${value.toFixed(value < 1 ? 4 : 2)}`
  return value.toLocaleString()
}

function formatAxis(value: number, metric: AnalyticsMetric): string {
  if (metric === 'cost') return `$${value.toFixed(2)}`
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}k`
  return String(value)
}

function formatBucketLabel(iso: string, interval: AnalyticsInterval): string {
  const d = new Date(iso)
  if (interval === 'hour') return d.toLocaleTimeString([], { hour: 'numeric' })
  return d.toLocaleDateString([], { month: 'short', day: 'numeric' })
}

function logsLinkFor(groupBy: AnalyticsGroupBy, g: AnalyticsGroup): string | null {
  if (g.key === '__other__' || g.key === 'none') return null
  switch (groupBy) {
    case 'model':   return `/logs?model=${encodeURIComponent(g.label)}`
    case 'api_key': return `/logs?api_key_id=${g.key}`
    case 'backend': return `/logs?backend_id=${g.key}`
    default:        return null // user has no direct log filter
  }
}

// ── Summary cards ─────────────────────────────────────────────────────────────

// Headline figure for a collapsed section header, so health is visible without
// expanding it.
function fmtLost(seconds: number): string {
  if (seconds < 90) return `${seconds.toFixed(0)}s`
  if (seconds < 5400) return `${(seconds / 60).toFixed(0)} min`
  return `${(seconds / 3600).toFixed(1)} h`
}

function SummaryCard({ title, value, icon: Icon, color }: {
  title: string
  value: string
  icon: React.ElementType
  color: string
}) {
  return (
    <div className="bg-slate-800 rounded-lg p-4 border border-slate-700">
      <div className="flex items-center justify-between">
        <div>
          <p className="text-xs text-slate-400">{title}</p>
          <p className="text-xl font-bold mt-0.5">{value}</p>
        </div>
        <div className={`p-2 rounded-lg ${color}`}>
          <Icon className="w-4 h-4" />
        </div>
      </div>
    </div>
  )
}

// ── Toggle button group ───────────────────────────────────────────────────────

function Toggle<T extends string>({ options, value, onChange }: {
  options: { value: T; label: string }[]
  value: T
  onChange: (v: T) => void
}) {
  return (
    <div className="flex flex-wrap gap-1 bg-slate-800 border border-slate-700 rounded-lg p-1">
      {options.map(o => (
        <button key={o.value} onClick={() => onChange(o.value)}
          className={`px-2.5 py-1 rounded text-xs transition-colors ${
            value === o.value ? 'bg-blue-600 text-white' : 'text-slate-400 hover:text-white'
          }`}>
          {o.label}
        </button>
      ))}
    </div>
  )
}

// ── Page ──────────────────────────────────────────────────────────────────────

function KVCacheSection() {
  const { all } = useCacheTelemetry()
  const live = all.filter(t => t.available)
  if (all.length === 0) {
    return <p className="text-sm text-slate-500">No backend exposes KV-cache telemetry yet.</p>
  }
  const saved = live.reduce((a, t) => a + t.prompt_tokens_saved, 0)
  return (
    <div>
      <p className="text-xs text-slate-500 mb-3">
        Counters are cumulative since each backend started. Pool occupancy is the binding
        memory constraint: when it approaches 100% the engine evicts conversation caches.
        {saved > 0 && <> Across live hosts the cache has saved {(saved / 1_000_000).toFixed(1)}M prompt-token reprocesses.</>}
      </p>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
        {all.map(t => <CacheTelemetryCard key={t.model_map_id} t={t} />)}
      </div>
    </div>
  )
}

export default function Analytics() {
  const [groupBy, setGroupBy] = useState<AnalyticsGroupBy>('model')
  const [metric, setMetric] = useState<AnalyticsMetric>('tokens')
  const [interval, setInterval] = useState<AnalyticsInterval | 'auto'>('auto')
  const [preset, setPreset] = useState<string>('7d')

  const now = new Date()
  const [fromDate, setFromDate] = useState<string>(toDateInput(new Date(now.getTime() - 7 * 86400000)))
  const [toDate, setToDate] = useState<string>(toDateInput(now))

  useDashboardStream()

  const { from, to } = useMemo(() => computeRange(preset, fromDate, toDate), [preset, fromDate, toDate])

  // Routing health is windowed in hours; follow whatever range the page is showing
  // so the two halves of the page always describe the same period.
  const rangeHours = useMemo(() => {
    const ms = new Date(to).getTime() - new Date(from).getTime()
    return Math.max(1, Math.min(24 * 90, Math.round(ms / 3600000)))
  }, [from, to])

  const { data, isLoading, isError, error } = useQuery({
    queryKey: ['analytics', groupBy, interval, from, to],
    queryFn: () => analyticsApi.usage({
      from, to, group_by: groupBy,
      interval: interval === 'auto' ? undefined : interval,
    }),
    refetchInterval: 30000,
    placeholderData: prev => prev,
  })

  // Headline figures for the collapsed section headers. These reuse the exact query
  // keys of the components inside those sections, so react-query serves both from a
  // single request instead of fetching twice.
  const { data: routing } = useQuery({
    queryKey: ['analytics-routing', rangeHours],
    queryFn: () => analyticsApi.routing(rangeHours),
    refetchInterval: 30000,
    placeholderData: prev => prev,
  })
  const { data: prefix } = useQuery({
    queryKey: ['prefix-analytics', rangeHours],
    queryFn: () => analyticsApi.prefix(rangeHours),
    refetchInterval: 30000,
    placeholderData: prev => prev,
  })

  const effectiveInterval: AnalyticsInterval = data?.interval ?? 'day'
  const groups = data?.groups ?? []

  // Reshape buckets into recharts rows: one row per bucket, one numeric key per group.
  const chartData = useMemo(() => {
    if (!data) return []
    return data.buckets.map(b => {
      const row: Record<string, number | string> = {
        label: formatBucketLabel(b.time, data.interval),
        time: b.time,
      }
      for (const g of groups) {
        row[g.key] = b.groups[g.key]?.[metric] ?? 0
      }
      return row
    })
  }, [data, groups, metric])

  const sortedGroups = useMemo(
    () => [...groups].sort((a, b) => b[metric] - a[metric]),
    [groups, metric],
  )

  const metricTotal = data?.summary[metric] ?? 0
  const errorRate = data && data.summary.requests > 0
    ? (data.summary.errors / data.summary.requests) * 100
    : 0

  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-3 mb-6">
        <h2 className="text-2xl font-bold flex items-center gap-2">
          <BarChart3 className="w-6 h-6 text-blue-400" /> Analytics
        </h2>
        <div className="flex items-center gap-2">
          <span className="flex items-center gap-1 text-xs text-slate-400">
            <span className="w-2 h-2 bg-green-400 rounded-full animate-pulse" /> live
          </span>
        </div>
      </div>

      {/* ── Controls ── */}
      <div className="bg-slate-800/50 rounded-lg border border-slate-700 p-4 mb-6">
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-4 gap-4">
          <div>
            <label className="block text-xs text-slate-400 mb-1.5">Time range</label>
            <div className="flex flex-wrap gap-1 bg-slate-800 border border-slate-700 rounded-lg p-1">
              {PRESETS.map(p => (
                <button key={p.value} onClick={() => setPreset(p.value)}
                  className={`px-2.5 py-1 rounded text-xs transition-colors ${
                    preset === p.value ? 'bg-blue-600 text-white' : 'text-slate-400 hover:text-white'
                  }`}>
                  {p.label}
                </button>
              ))}
              <button onClick={() => setPreset('custom')}
                className={`px-2.5 py-1 rounded text-xs transition-colors ${
                  preset === 'custom' ? 'bg-blue-600 text-white' : 'text-slate-400 hover:text-white'
                }`}>
                Custom
              </button>
            </div>
            {preset === 'custom' && (
              <div className="flex items-center gap-2 mt-2">
                <input type="date" value={fromDate} max={toDate}
                  onChange={e => setFromDate(e.target.value)}
                  className="bg-slate-800 border border-slate-700 rounded px-2 py-1 text-xs text-slate-200" />
                <span className="text-slate-500 text-xs">to</span>
                <input type="date" value={toDate} min={fromDate} max={toDateInput(new Date())}
                  onChange={e => setToDate(e.target.value)}
                  className="bg-slate-800 border border-slate-700 rounded px-2 py-1 text-xs text-slate-200" />
              </div>
            )}
          </div>

          <div>
            <label className="block text-xs text-slate-400 mb-1.5">Stack by</label>
            <Toggle options={GROUP_OPTIONS} value={groupBy} onChange={setGroupBy} />
          </div>

          <div>
            <label className="block text-xs text-slate-400 mb-1.5">Metric</label>
            <Toggle options={METRIC_OPTIONS} value={metric} onChange={setMetric} />
          </div>

          <div>
            <label className="block text-xs text-slate-400 mb-1.5">Interval</label>
            <Toggle options={INTERVAL_OPTIONS} value={interval} onChange={setInterval} />
          </div>
        </div>
      </div>

      {/* ── Traffic & tokens ───────────────────────────────────────────────
          Kept first and expanded by default: usage is what most visits are for,
          and the cache diagnostics below had been pushing it off the fold. ── */}
      <Section id="tokens" title="Traffic & tokens"
        subtitle="requests, tokens, cost and errors"
        icon={Zap} iconColor="text-purple-400"
        badge={<span className="text-xs text-slate-400 tabular-nums">
          {(data?.summary.tokens ?? 0).toLocaleString()} tokens</span>}>
        {/* ── Summary ── */}
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
          <SummaryCard title="Requests" value={(data?.summary.requests ?? 0).toLocaleString()}
            icon={Activity} color="bg-blue-500/20 text-blue-400" />
          <SummaryCard title="Tokens" value={(data?.summary.tokens ?? 0).toLocaleString()}
            icon={Zap} color="bg-purple-500/20 text-purple-400" />
          <SummaryCard title="Cost" value={`$${(data?.summary.cost ?? 0).toFixed(4)}`}
            icon={DollarSign} color="bg-green-500/20 text-green-400" />
          <SummaryCard title="Error rate" value={`${errorRate.toFixed(1)}%`}
            icon={AlertTriangle} color="bg-red-500/20 text-red-400" />
        </div>

        {/* ── Chart ── */}
        <div className="bg-slate-800 rounded-lg p-5 border border-slate-700 mb-6">
          <h3 className="text-base font-semibold mb-4">
            {METRIC_OPTIONS.find(m => m.value === metric)?.label} by {GROUP_OPTIONS.find(g => g.value === groupBy)?.label}
            <span className="text-slate-500 font-normal text-sm ml-2">({effectiveInterval})</span>
          </h3>
          {isError ? (
            <div className="h-80 flex items-center justify-center text-red-400 text-sm">
              {(error as Error)?.message || 'Failed to load analytics'}
            </div>
          ) : isLoading && !data ? (
            <div className="h-80 flex items-center justify-center">
              <div className="animate-spin w-8 h-8 border-2 border-blue-400 border-t-transparent rounded-full" />
            </div>
          ) : chartData.length === 0 || groups.length === 0 ? (
            <div className="h-80 flex items-center justify-center text-slate-500 text-sm">
              No usage in this range
            </div>
          ) : (
            <div className="h-80">
              <ResponsiveContainer width="100%" height="100%">
                <BarChart data={chartData} margin={{ top: 0, right: 8, bottom: 0, left: 0 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#1e293b" />
                  <XAxis dataKey="label" tick={{ fill: '#94a3b8', fontSize: 11 }} interval="preserveStartEnd" />
                  <YAxis tick={{ fill: '#94a3b8', fontSize: 11 }} tickFormatter={v => formatAxis(v, metric)} />
                  <Tooltip
                    contentStyle={{ backgroundColor: '#1e293b', border: '1px solid #334155', borderRadius: '6px', fontSize: 12 }}
                    formatter={(value: number, name: string) => [formatMetric(value, metric), name]}
                    labelFormatter={(_, items) => items?.[0]?.payload?.time
                      ? new Date(items[0].payload.time).toLocaleString() : ''}
                    itemSorter={(item) => -(item.value as number)}
                  />
                  <Legend wrapperStyle={{ fontSize: 12 }} />
                  {groups.map((g, i) => (
                    <Bar key={g.key} dataKey={g.key} name={g.label} stackId="a"
                      fill={colorForGroup(g.key, i)} />
                  ))}
                </BarChart>
              </ResponsiveContainer>
            </div>
          )}
        </div>

        {/* ── Drill-down table ── */}
        <div className="bg-slate-800 rounded-lg border border-slate-700 overflow-hidden">
          <div className="px-5 py-4 border-b border-slate-700">
            <h3 className="text-base font-semibold">Breakdown by {GROUP_OPTIONS.find(g => g.value === groupBy)?.label}</h3>
          </div>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-slate-400 border-b border-slate-700">
                  <th className="px-5 py-2.5 font-medium">{GROUP_OPTIONS.find(g => g.value === groupBy)?.label}</th>
                  <th className="px-5 py-2.5 font-medium text-right">Requests</th>
                  <th className="px-5 py-2.5 font-medium text-right">Tokens</th>
                  <th className="px-5 py-2.5 font-medium text-right">Cost</th>
                  <th className="px-5 py-2.5 font-medium text-right">Errors</th>
                  <th className="px-5 py-2.5 font-medium text-right w-40">Share</th>
                </tr>
              </thead>
              <tbody>
                {sortedGroups.length === 0 ? (
                  <tr><td colSpan={6} className="px-5 py-6 text-center text-slate-500">No data</td></tr>
                ) : sortedGroups.map((g) => {
                  const share = metricTotal > 0 ? (g[metric] / metricTotal) * 100 : 0
                  const link = logsLinkFor(groupBy, g)
                  const color = colorForGroup(g.key, groups.findIndex(x => x.key === g.key))
                  return (
                    <tr key={g.key} className="border-b border-slate-700/50 last:border-0 hover:bg-slate-700/30">
                      <td className="px-5 py-2.5">
                        <div className="flex items-center gap-2">
                          <span className="w-2.5 h-2.5 rounded-sm shrink-0" style={{ backgroundColor: color }} />
                          {link ? (
                            <Link to={link} className="font-medium hover:text-blue-400 flex items-center gap-1 truncate">
                              {g.label} <ChevronRight className="w-3 h-3 opacity-50" />
                            </Link>
                          ) : (
                            <span className="font-medium truncate">{g.label}</span>
                          )}
                        </div>
                      </td>
                      <td className="px-5 py-2.5 text-right tabular-nums text-slate-300">{g.requests.toLocaleString()}</td>
                      <td className="px-5 py-2.5 text-right tabular-nums text-slate-300">{g.tokens.toLocaleString()}</td>
                      <td className="px-5 py-2.5 text-right tabular-nums text-slate-300">${g.cost.toFixed(4)}</td>
                      <td className="px-5 py-2.5 text-right tabular-nums">
                        <span className={g.errors > 0 ? 'text-red-400' : 'text-slate-500'}>{g.errors.toLocaleString()}</span>
                      </td>
                      <td className="px-5 py-2.5">
                        <div className="flex items-center gap-2 justify-end">
                          <div className="w-20 h-1.5 bg-slate-700 rounded-full overflow-hidden">
                            <div className="h-full rounded-full" style={{ width: `${share}%`, backgroundColor: color }} />
                          </div>
                          <span className="text-xs text-slate-400 tabular-nums w-10 text-right">{share.toFixed(1)}%</span>
                        </div>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </div>
      </Section>

      {/* ── Diagnostics ───────────────────────────────────────────────────
          Collapsed by default so the page opens focused on usage. The state
          is remembered per section, so anyone working a cache problem keeps
          these open across visits. Each header carries its headline figure so
          a collapsed section still shows whether it needs attention. ── */}
      <Section id="routing" title="Routing & cache reuse"
        subtitle="session affinity, cache hits, compression"
        icon={Share2} iconColor="text-blue-400" defaultOpen={false}
        badge={routing ? (
          <span className={`text-xs tabular-nums ${
            routing.cache_reuse_rate < 0.7 ? 'text-orange-400' : 'text-slate-400'}`}>
            {(routing.cache_reuse_rate * 100).toFixed(1)}% reuse
          </span>
        ) : null}>
        <RoutingHealth hours={rangeHours} />
        <div className="mt-6 pt-6 border-t border-slate-700">
          <RoutingCharts hours={rangeHours} />
        </div>
      </Section>

      <Section id="prefix" title="Cache miss diagnosis"
        subtitle="why prompts lost their cached prefix"
        icon={SearchCheck} iconColor="text-amber-400" defaultOpen={false}
        badge={prefix && prefix.diagnosed_requests > 0 ? (
          <span className={`text-xs tabular-nums ${
            prefix.estimated_seconds > 600 ? 'text-red-400' : 'text-slate-400'}`}>
            {fmtLost(prefix.estimated_seconds)} lost
          </span>
        ) : null}>
        <PrefixHealth hours={rangeHours} />
      </Section>

      <Section id="kvcache" title="KV cache utilization"
        subtitle="per-host KV pool occupancy and hit rates (live, polled every 30s)"
        icon={Database} iconColor="text-emerald-400" defaultOpen={false}>
        <KVCacheSection />
      </Section>

      <Section id="throughput" title="Generation speed"
        subtitle="measured prefill and decode tok/s over time"
        icon={Gauge} iconColor="text-cyan-400" defaultOpen={false}>
        <ThroughputCharts hours={rangeHours} />
      </Section>
    </div>
  )
}
