import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  LineChart, Line, AreaChart, Area, XAxis, YAxis, CartesianGrid, Tooltip,
  ResponsiveContainer, Legend,
} from 'recharts'
import { TrendingUp } from 'lucide-react'
import { analyticsApi } from '../api/client'
import type { RoutingSeries } from '../types'

const AXIS = { stroke: '#94a3b8', fontSize: 11 }
const GRID = '#334155'
const TOOLTIP = {
  contentStyle: { background: '#1e293b', border: '1px solid #334155', borderRadius: 8 },
  labelStyle: { color: '#e2e8f0' },
}

function bytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 ** 2) return `${(n / 1024).toFixed(1)} KB`
  if (n < 1024 ** 3) return `${(n / 1024 ** 2).toFixed(1)} MB`
  return `${(n / 1024 ** 3).toFixed(2)} GB`
}

function label(iso: string, interval: string): string {
  const d = new Date(iso)
  if (interval === 'day') return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
  if (interval === 'minute') return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
}

function Panel({ title, hint, children }: {
  title: string; hint: string; children: React.ReactNode
}) {
  return (
    <div className="bg-slate-800/60 rounded-lg border border-slate-700 p-4">
      <div className="mb-2">
        <div className="text-sm font-medium text-slate-200">{title}</div>
        <div className="text-xs text-slate-500">{hint}</div>
      </div>
      <div className="h-56">{children}</div>
    </div>
  )
}

export default function RoutingCharts({ hours = 24 }: { hours?: number }) {
  const { data, isLoading, isError, error } = useQuery({
    queryKey: ['analytics-routing-series', hours],
    queryFn: () => analyticsApi.routingSeries(hours),
    refetchInterval: 60000,
    placeholderData: prev => prev,
  })

  const rows = useMemo(() => {
    const d = data as RoutingSeries | undefined
    if (!d) return []
    return d.points.map(p => ({
      label: label(p.time, d.interval),
      // Percentages read better than fractions on an axis.
      reuse: +(p.cache_reuse_rate * 100).toFixed(1),
      cold: +(p.cold_rate * 100).toFixed(1),
      reprocessed: p.reprocessed_tokens,
      saved: p.compression_bytes_saved,
      requests: p.requests,
      deep: p.deep_requests,
      coldCount: p.cold_requests,
      latency: p.mean_latency_ms,
    }))
  }, [data])

  if (isLoading && !data) return <div className="text-slate-400 text-sm">Loading trends…</div>
  if (isError) {
    return (
      <div className="bg-red-500/10 border border-red-500/30 rounded-lg p-4 text-sm text-red-300">
        Failed to load routing series: {(error as Error)?.message ?? 'unknown error'}
      </div>
    )
  }
  if (!rows.length) return null

  const anyCompression = rows.some(r => r.saved > 0)

  return (
    <div className="space-y-4">
      <h3 className="text-lg font-semibold flex items-center gap-2">
        <TrendingUp className="w-5 h-5 text-green-400" /> Trends
        <span className="text-xs font-normal text-slate-500">
          ({(data as RoutingSeries).interval} buckets)
        </span>
      </h3>

      <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
        <Panel
          title="Cache reuse"
          hint="Share of prompt tokens served from the upstream KV cache. Falling reuse means conversations are losing their host or slot."
        >
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart data={rows}>
              <CartesianGrid strokeDasharray="3 3" stroke={GRID} />
              <XAxis dataKey="label" {...AXIS} />
              <YAxis {...AXIS} domain={[0, 100]} unit="%" />
              <Tooltip {...TOOLTIP} formatter={(v: number) => `${v}%`} />
              <Area type="monotone" dataKey="reuse" name="cache reuse"
                stroke="#22c55e" fill="#22c55e" fillOpacity={0.2} />
            </AreaChart>
          </ResponsiveContainer>
        </Panel>

        <Panel
          title="Cold arrivals"
          hint="Deep requests (>2k tokens) that arrived with almost no cache and paid a full re-prefill. This is where the seconds go."
        >
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={rows}>
              <CartesianGrid strokeDasharray="3 3" stroke={GRID} />
              <XAxis dataKey="label" {...AXIS} />
              <YAxis yAxisId="l" {...AXIS} unit="%" domain={[0, 100]} />
              <YAxis yAxisId="r" orientation="right" {...AXIS} />
              <Tooltip {...TOOLTIP} />
              <Legend wrapperStyle={{ fontSize: 11 }} />
              <Line yAxisId="l" type="monotone" dataKey="cold" name="cold rate %"
                stroke="#f59e0b" dot={false} strokeWidth={2} />
              <Line yAxisId="r" type="monotone" dataKey="coldCount" name="cold requests"
                stroke="#ef4444" dot={false} strokeDasharray="4 3" />
            </LineChart>
          </ResponsiveContainer>
        </Panel>

        <Panel
          title="Reprocessed tokens"
          hint="Prompt tokens the upstreams had to prefill again. At ~200-340 tok/s this converts directly to seconds of waiting."
        >
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart data={rows}>
              <CartesianGrid strokeDasharray="3 3" stroke={GRID} />
              <XAxis dataKey="label" {...AXIS} />
              <YAxis {...AXIS} />
              <Tooltip {...TOOLTIP} formatter={(v: number) => v.toLocaleString()} />
              <Area type="monotone" dataKey="reprocessed" name="reprocessed tokens"
                stroke="#f97316" fill="#f97316" fillOpacity={0.2} />
            </AreaChart>
          </ResponsiveContainer>
        </Panel>

        <Panel
          title="Compression saved"
          hint={anyCompression
            ? 'Bytes removed from prompts before they were forwarded.'
            : 'No target has compression enabled, or its traffic contains no JSON — both modes only act on JSON.'}
        >
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart data={rows}>
              <CartesianGrid strokeDasharray="3 3" stroke={GRID} />
              <XAxis dataKey="label" {...AXIS} />
              <YAxis {...AXIS} tickFormatter={bytes} />
              <Tooltip {...TOOLTIP} formatter={(v: number) => bytes(v)} />
              <Area type="monotone" dataKey="saved" name="bytes saved"
                stroke="#38bdf8" fill="#38bdf8" fillOpacity={0.2} />
            </AreaChart>
          </ResponsiveContainer>
        </Panel>
      </div>

      {/* llama.cpp exposes no metric for its prompt cache, so its size and hit rate
          cannot be charted directly. Cold arrivals above are the observable effect:
          a larger cache should push that line down. */}
      <p className="text-xs text-slate-500">
        Upstream prompt-cache usage (llama.cpp <code className="text-slate-400">--cache-ram</code>) is not
        exposed by its API and cannot be graphed directly. Its effect shows up as the cold-arrival
        rate: a larger cache holds more evicted conversations, so fewer return cold.
      </p>
    </div>
  )
}
