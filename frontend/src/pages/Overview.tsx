import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer } from 'recharts'
import { Activity, Zap, Clock, AlertTriangle, Radio } from 'lucide-react'
import { backendsApi, dashboardApi, logsApi, queueApi } from '../api/client'
import { useDashboardStream, type ErrorLogEntry } from '../hooks/useDashboardStream'
import { useBackendColors } from '../utils/colors'
import { LiveRequestsTable } from '../components/LiveRequests'
import { PageHeader, Panel, KpiTile, KpiStrip, Callout } from '../components/ui'
import { compact, money } from '../utils/format'
import { AXIS, GRID, tooltipStyle, sem, status, ENTITY_OTHER } from '../theme'

// "Is anything wrong right now?" The live state of the fleet and today's totals.
// Who-used-what lives on Usage; why-is-it-slow on Performance.

type Health = 'good' | 'warn' | 'crit'
function healthOf(s: string): { tone: Health; label: string } {
  switch (s) {
    case 'active': return { tone: 'good', label: 'Healthy' }
    case 'offline': case 'error': return { tone: 'crit', label: 'Offline' }
    default: return { tone: 'warn', label: s ? s[0].toUpperCase() + s.slice(1) : 'Unknown' }
  }
}

// Status always carries a shape as well as a color.
function HealthIcon({ tone }: { tone: Health }) {
  const fill = tone === 'good' ? status.good : tone === 'warn' ? status.warn : status.bad
  return (
    <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden fill={fill}>
      {tone === 'good' ? <circle cx="5" cy="5" r="4" /> : tone === 'warn' ? <path d="M5 .8 9.5 9H.5z" /> : <rect x="1" y="1" width="8" height="8" rx="1" />}
    </svg>
  )
}

const fmtMs = (v: number) => (v >= 10000 ? `${(v / 1000).toFixed(1)}s` : `${Math.round(v)} ms`)

export default function Overview() {
  const colorsById = useBackendColors()
  useDashboardStream()

  const { data: stats } = useQuery({
    queryKey: ['dashboard-stats', 'today'],
    queryFn: () => dashboardApi.stats('today'),
    refetchInterval: 30000,
    placeholderData: p => p,
  })
  const { data: backends = [] } = useQuery({ queryKey: ['backends'], queryFn: backendsApi.list, refetchInterval: 15000 })
  const { data: queue } = useQuery({ queryKey: ['queue'], queryFn: queueApi.snapshot, refetchInterval: 1000, staleTime: 0 })
  const { data: speedData } = useQuery({ queryKey: ['queue-speeds'], queryFn: queueApi.speeds, refetchInterval: 5000 })
  const { data: errorLogs } = useQuery({
    queryKey: ['recent-errors'],
    queryFn: () => logsApi.list({ limit: 8, errors_only: true }),
    refetchInterval: 30000,
  })
  const { data: liveErrors = [] } = useQuery<ErrorLogEntry[]>({ queryKey: ['live-errors'], initialData: [] })

  const entries = queue?.active ?? []
  const queued = entries.filter(e => e.status === 'queued').length
  const speeds = new Map((speedData?.speeds ?? []).map(s => [s.backend_id, s]))
  const learnedPromptTokS = new Map((speedData?.speeds ?? []).map(s => [s.backend_id, s.prompt_tok_s]))

  const fleet = useMemo(() => {
    const today = new Map((stats?.backend_stats ?? []).map(b => [b.backend_id, b]))
    return backends.map(b => {
      const active = entries.filter(e => e.backend_id === b.id).length
      return { b, health: healthOf(b.status), active, today: today.get(b.id), speed: speeds.get(b.id) }
    })
  }, [backends, entries, stats, speeds])
  const offline = fleet.filter(f => f.health.tone === 'crit')

  const requests = stats?.total_requests_today ?? 0
  const errors = stats?.error_count_today ?? 0
  const errorRate = requests ? errors / requests : 0

  // Errors stack on successes on one axis: the bar is the hour's volume, red is the failed part.
  const series = (stats?.usage_series ?? []).map(p => ({
    label: new Date(p.time).toLocaleTimeString([], { hour: 'numeric' }),
    time: p.time,
    ok: Math.max(0, p.requests - p.errors),
    errors: p.errors,
  }))

  // Live errors arrive over SSE; recent ones from the log. Show the newest of both.
  const recentErrors: ErrorLogEntry[] = [...liveErrors, ...(errorLogs?.data ?? [])]
    .sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
    .filter((e, i, all) => i === 0 || e.created_at !== all[i - 1].created_at || e.model !== all[i - 1].model)
    .slice(0, 8)

  return (
    <div>
      <PageHeader title="Overview" description="Live state of the fleet and today’s totals" />

      {offline.length > 0 ? (
        <div className="mb-3">
          <Callout
            title={`${offline.length} host${offline.length === 1 ? '' : 's'} offline.`}
            action={<Link to="/hosts" className="shrink-0 text-[13px] font-semibold text-accent-400 hover:text-accent-300">Hosts →</Link>}
          >
            {offline.map(f => f.b.name).join(', ')} {offline.length === 1 ? 'is' : 'are'} not taking traffic.
          </Callout>
        </div>
      ) : errorRate > 0.05 && errors >= 5 ? (
        <div className="mb-3">
          <Callout
            title={`${(errorRate * 100).toFixed(1)}% of today’s requests failed.`}
            action={<Link to="/logs?errors_only=true" className="shrink-0 text-[13px] font-semibold text-accent-400 hover:text-accent-300">Logs →</Link>}
          >
            {errors.toLocaleString()} errors so far today.
          </Callout>
        </div>
      ) : null}

      <KpiStrip className="mb-3 grid-cols-2 md:grid-cols-3 xl:grid-cols-6">
        <KpiTile label="In flight" icon={Radio} value={String(entries.length)} hero
          sub={queued > 0 ? `${queued} queued` : 'none queued'} color={sem.attention} />
        <KpiTile label="Requests today" icon={Activity} value={compact(requests)}
          trend={series.map(s => s.ok + s.errors)} color={sem.volume} sub={`${requests.toLocaleString()} total`} />
        <KpiTile label="Tokens today" icon={Zap} value={compact(stats?.total_tokens_today ?? 0)} color={sem.volume}
          sub={`${money(stats?.total_cost_today ?? 0)} at configured prices`} />
        <KpiTile label="Decode" icon={Zap} value={(stats?.avg_tokens_per_second_today ?? 0).toFixed(1)}
          sub="tok/s, output ÷ latency" color={sem.output} />
        <KpiTile label="Avg latency" icon={Clock} value={fmtMs(stats?.avg_latency_ms ?? 0)} color={sem.neutral} />
        <KpiTile label="Errors today" icon={AlertTriangle} value={errors.toLocaleString()}
          sub={requests ? `${(errorRate * 100).toFixed(errorRate < 0.1 ? 2 : 1)}% of requests` : undefined}
          trend={series.map(s => s.errors)} color={status.bad}
          tone={errorRate > 0.05 ? 'bad' : undefined} />
      </KpiStrip>

      <div className="mb-3 grid grid-cols-1 gap-3 xl:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)]">
        <Panel title="Fleet" subtitle={`${fleet.length} host${fleet.length === 1 ? '' : 's'}`} bodyClassName="overflow-x-auto"
          info="Live state per host. Prefill and decode are the speeds relai has learned from recent requests; today’s columns count since midnight."
          actions={<Link to="/hosts" className="text-xs text-slate-400 hover:text-accent-400">Manage →</Link>}>
          <table className="table-pro">
            <thead>
              <tr>
                <th>Host</th><th>State</th>
                <th className="!text-right">Active</th>
                <th className="!text-right">Prefill tok/s</th>
                <th className="!text-right">Decode tok/s</th>
                <th className="!text-right">Requests</th>
                <th className="!text-right">Latency</th>
                <th>Load</th>
              </tr>
            </thead>
            <tbody>
              {fleet.length === 0 && <tr><td colSpan={8} className="py-8 text-center text-slate-500">No hosts yet</td></tr>}
              {fleet.map(({ b, health, active, today, speed }) => {
                const load = b.max_concurrent > 0 ? Math.min(1, active / b.max_concurrent) : 0
                return (
                  <tr key={b.id}>
                    <td>
                      <span className="inline-flex items-center gap-2 font-medium text-slate-100">
                        <i className="h-2 w-2 rounded-sm" style={{ background: colorsById.get(b.id) ?? ENTITY_OTHER }} />
                        {b.name}
                      </span>
                    </td>
                    <td><span className="inline-flex items-center gap-1.5 text-slate-300"><HealthIcon tone={health.tone} />{health.label}</span></td>
                    <td className="text-right font-mono tabular">{active}{b.max_concurrent > 0 ? `/${b.max_concurrent}` : ''}</td>
                    <td className="text-right font-mono tabular">{speed && speed.samples > 0 ? speed.prompt_tok_s.toFixed(0) : '—'}</td>
                    <td className="text-right font-mono tabular">{speed && speed.samples > 0 ? speed.decode_tok_s.toFixed(1) : '—'}</td>
                    <td className="text-right font-mono tabular">{today ? today.requests.toLocaleString() : '0'}</td>
                    <td className="text-right font-mono tabular">{today && today.requests ? fmtMs(today.avg_latency) : '—'}</td>
                    <td className="w-28">
                      <div className="h-2 overflow-hidden rounded-sm bg-slate-700/70" title={`${active} of ${b.max_concurrent || '∞'} slots busy`}>
                        <div className="h-full rounded-sm" style={{ width: `${load * 100}%`, background: load >= 1 ? sem.attention : sem.volume }} />
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </Panel>

        <Panel title="Requests today" subtitle="per hour"
          legend={[{ label: 'Succeeded', color: sem.neutral }, { label: 'Failed', color: status.bad }]}>
          <div className="h-56">
            <ResponsiveContainer width="100%" height="100%">
              <BarChart data={series} margin={{ top: 4, right: 4, bottom: 0, left: 0 }} barCategoryGap="20%">
                <CartesianGrid vertical={false} stroke={GRID} />
                <XAxis dataKey="label" {...AXIS} interval="preserveStartEnd" minTickGap={24} />
                <YAxis {...AXIS} axisLine={false} width={40} allowDecimals={false} />
                <Tooltip {...tooltipStyle}
                  labelFormatter={(_, items) => items?.[0]?.payload?.time ? new Date(items[0].payload.time).toLocaleString() : ''} />
                <Bar dataKey="ok" name="Succeeded" stackId="r" fill={sem.neutral} isAnimationActive={false} />
                <Bar dataKey="errors" name="Failed" stackId="r" fill={status.bad} radius={[2, 2, 0, 0]} isAnimationActive={false} />
              </BarChart>
            </ResponsiveContainer>
          </div>
        </Panel>
      </div>

      <div className="grid grid-cols-1 gap-3 xl:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)]">
        <Panel title="Live requests" subtitle={`${entries.length} in flight${queued ? ` · ${queued} queued` : ''}`} bodyClassName=""
          legend={[
            { label: 'Prefill', color: sem.input },
            { label: 'Decode', color: sem.output },
          ]}>
          <LiveRequestsTable entries={entries} colorsById={colorsById} learnedPromptTokS={learnedPromptTokS} />
        </Panel>

        <Panel title="Recent errors" bodyClassName=""
          actions={<Link to="/logs?errors_only=true" className="text-xs text-slate-400 hover:text-accent-400">All errors →</Link>}>
          {recentErrors.length === 0 ? (
            <div className="py-10 text-center text-sm text-slate-500">No recent errors</div>
          ) : (
            <ul className="divide-y divide-slate-700/60">
              {recentErrors.map((e, i) => (
                <li key={i} className="flex items-center gap-3 px-3 py-2 text-[13px]">
                  <span className="shrink-0 font-mono text-xs text-slate-500 tabular">{new Date(e.created_at).toLocaleTimeString([], { hour12: false })}</span>
                  <span className="shrink-0 rounded bg-crit-500/15 px-1.5 font-mono text-xs text-crit-300">{e.status_code}</span>
                  <span className="shrink-0 truncate font-mono text-slate-300">{e.model}</span>
                  <span className="min-w-0 flex-1 truncate text-xs text-slate-400" title={e.error_message}>{e.error_message || `HTTP ${e.status_code}`}</span>
                </li>
              ))}
            </ul>
          )}
        </Panel>
      </div>
    </div>
  )
}
