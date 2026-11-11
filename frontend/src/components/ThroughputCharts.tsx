import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  LineChart, Line, XAxis, YAxis, CartesianGrid, Tooltip, Legend,
  ResponsiveContainer,
} from 'recharts'
import { Gauge } from 'lucide-react'
import { throughputApi } from '../api/client'
import type { ThroughputSeries } from '../types'

const AXIS = { stroke: '#94a3b8', fontSize: 11 }
const GRID = '#334155'
const TOOLTIP = {
  contentStyle: { background: '#1e293b', border: '1px solid #334155', borderRadius: 8 },
  labelStyle: { color: '#e2e8f0' },
}

function label(iso: string, interval: string): string {
  const d = new Date(iso)
  if (interval === 'day') return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
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

// Distinct line colors for per-host series; cycles if there are more hosts than colors.
const HOST_COLORS = ['#22c55e', '#f59e0b', '#8b5cf6', '#ec4899', '#06b6d4', '#84cc16', '#f97316', '#6366f1']

// Per-host aggregate decode rate on the wall clock: each request's tokens are
// attributed to the real-time seconds its decode window occupied, and
// overlapping requests share the clock — so this line RISES with batching
// (total tokens the machine emits per second of real decoding time) while the
// per-request chart sags. The gap between the two charts is the batching win.
function PerHostPanel({ rows, hosts }: {
  rows: Array<Record<string, number | string>>
  hosts: Array<{ id: number; name: string }>
}) {
  if (!hosts.length) {
    return (
      <Panel title="Per-host decode throughput" hint="Appears once requests are logged with a backend.">
        <div className="h-full flex items-center justify-center text-sm text-slate-500">
          No per-host data in this window.
        </div>
      </Panel>
    )
  }
  return (
    <Panel
      title="Per-host decode throughput (aggregate)"
      hint="One line per machine, measured on the wall clock: tokens the host emitted per second it actually spent decoding. Concurrent requests share the same second, so this RISES with batching — unlike the per-request rate above, which sags because each stream slows down. The gap between the two charts is the batching win. Only requests logged after this feature shipped carry decode timestamps; earlier history stays blank."
    >
      <ResponsiveContainer width="100%" height="100%">
        <LineChart data={rows}>
          <CartesianGrid strokeDasharray="3 3" stroke={GRID} />
          <XAxis dataKey="label" {...AXIS} />
          <YAxis {...AXIS} unit="" />
          <Tooltip {...TOOLTIP} formatter={(v: number) => `${v.toLocaleString()} tok/s`} />
          <Legend wrapperStyle={{ fontSize: 11, color: '#94a3b8' }} />
          {hosts.map((h, i) => (
            <Line key={h.id} type="monotone" dataKey={`host-${h.id}`} name={h.name}
              stroke={HOST_COLORS[i % HOST_COLORS.length]} dot={false} strokeWidth={2} connectNulls />
          ))}
        </LineChart>
      </ResponsiveContainer>
    </Panel>
  )
}

// Measured prefill and decode throughput over time, token-weighted per bucket.
// The two rates are different animals: prefill scales with prompt size and KV
// cache reuse, decode with batch pressure — so they get separate charts rather
// than one dual-axis mashup.
export default function ThroughputCharts({ hours = 24 }: { hours?: number }) {
  const { data, isLoading, isError, error } = useQuery({
    queryKey: ['analytics-throughput-series', hours],
    queryFn: () => throughputApi.series(hours),
    refetchInterval: 60000,
    placeholderData: prev => prev,
  })

  const { rows, hosts } = useMemo(() => {
    const d = data as ThroughputSeries | undefined
    if (!d) return { rows: [] as Array<Record<string, number | string>>, hosts: [] as Array<{ id: number; name: string }> }
    const hostMap = new Map<number, string>()
    const rows = d.points.map(p => {
      const row: Record<string, number | string> = {
        label: label(p.time, d.interval),
        prefill: +p.prefill_tok_s.toFixed(1),
        decode: +p.decode_tok_s.toFixed(1),
        requests: p.requests,
        prefillReqs: p.prefill_requests,
        decodeReqs: p.decode_requests,
      }
      if (p.spec_requests > 0) {
        row.accept = +(p.accept_rate * 100).toFixed(1)
        row.specReqs = p.spec_requests
      }
      for (const b of p.by_backend ?? []) {
        hostMap.set(b.backend_id, b.backend_name || `host ${b.backend_id}`)
        row[`host-${b.backend_id}`] = +b.decode_wall_tok_s.toFixed(1)
      }
      return row
    })
    const hosts = [...hostMap.entries()]
      .sort((a, b) => a[0] - b[0])
      .map(([id, name]) => ({ id, name }))
    return { rows, hosts }
  }, [data])

  if (isLoading && !data) return <div className="text-slate-400 text-sm">Loading throughput…</div>
  if (isError) {
    return (
      <div className="bg-red-500/10 border border-red-500/30 rounded-lg p-4 text-sm text-red-300">
        Failed to load throughput series: {(error as Error)?.message ?? 'unknown error'}
      </div>
    )
  }
  const hasData = rows.some(r => (r.prefillReqs as number) > 0 || (r.decodeReqs as number) > 0)
  if (!rows.length || !hasData) {
    return (
      <div className="text-sm text-slate-500">
        No throughput data yet — timings are recorded per streaming request from
        here on, so charts fill in as traffic arrives.
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <h3 className="text-lg font-semibold flex items-center gap-2">
        <Gauge className="w-5 h-5 text-cyan-400" /> Generation speed
        <span className="text-xs font-normal text-slate-500">
          ({(data as ThroughputSeries).interval} buckets)
        </span>
      </h3>

      <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
        <Panel
          title="Prefill speed"
          hint="Freshly evaluated prompt tokens (KV-cache hits excluded) ÷ time from send to first token, token-weighted per bucket. Cached tokens were read, not processed — counting them would report cache reads as GPU speed. Drops when prompts grow or the KV cache misses; a shared host also pays batch interference."
        >
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={rows}>
              <CartesianGrid strokeDasharray="3 3" stroke={GRID} />
              <XAxis dataKey="label" {...AXIS} />
              <YAxis {...AXIS} unit="" />
              <Tooltip {...TOOLTIP} formatter={(v: number) => `${v.toLocaleString()} tok/s`} />
              <Line type="monotone" dataKey="prefill" name="prefill tok/s"
                stroke="#38bdf8" dot={false} strokeWidth={2} connectNulls />
            </LineChart>
          </ResponsiveContainer>
        </Panel>

        <Panel
          title="Decode speed"
          hint="Generated tokens ÷ time since first token, token-weighted per bucket. The number that decides how fast answers finish typing — watch it sag under concurrent load."
        >
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={rows}>
              <CartesianGrid strokeDasharray="3 3" stroke={GRID} />
              <XAxis dataKey="label" {...AXIS} />
              <YAxis {...AXIS} unit="" />
              <Tooltip {...TOOLTIP} formatter={(v: number) => `${v.toLocaleString()} tok/s`} />
              <Line type="monotone" dataKey="decode" name="decode tok/s"
                stroke="#22c55e" dot={false} strokeWidth={2} connectNulls />
            </LineChart>
          </ResponsiveContainer>
        </Panel>

        {rows.some(r => (r.specReqs as number) > 0) && (
          <Panel
            title="Speculation accept rate"
            hint="Drafted tokens the verifier accepted (Halogen MTP). A healthy drafter sits in the 60-85% band; a slide means a model or config regression is making drafts miss, silently costing decode speed."
          >
            <ResponsiveContainer width="100%" height="100%">
              <LineChart data={rows}>
                <CartesianGrid strokeDasharray="3 3" stroke={GRID} />
                <XAxis dataKey="label" {...AXIS} />
                <YAxis {...AXIS} unit="%" domain={[0, 100]} />
                <Tooltip {...TOOLTIP} formatter={(v: number) => `${v}%`} />
                <Line type="monotone" dataKey="accept" name="accepted"
                  stroke="#a78bfa" dot={false} strokeWidth={2} connectNulls />
              </LineChart>
            </ResponsiveContainer>
          </Panel>
        )}
      </div>

      <div className="grid grid-cols-1 gap-4">
        <PerHostPanel rows={rows} hosts={hosts} />
      </div>

      <p className="text-xs text-slate-500">
        Rates exclude requests without a measurable phase (non-streaming, failures).
        Prefill includes queue wait only when Relai itself admitted the request late;
        upstream batch time is part of the measurement.
      </p>
    </div>
  )
}
