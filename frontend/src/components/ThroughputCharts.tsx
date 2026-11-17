import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  LineChart, Line, XAxis, YAxis, CartesianGrid, Tooltip,
  ResponsiveContainer,
} from 'recharts'
import { throughputApi } from '../api/client'
import type { ThroughputSeries } from '../types'
import { Panel as UIPanel, type LegendItem } from './ui'
import { AXIS, GRID, tooltipStyle, sem, entityAt } from '../theme'

const TOOLTIP = tooltipStyle

function label(iso: string, interval: string): string {
  const d = new Date(iso)
  if (interval === 'day') return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
  return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
}

function Panel({ title, hint, legend, children }: {
  title: string; hint: string; legend?: LegendItem[]; children: React.ReactNode
}) {
  return (
    <UIPanel title={title} info={hint} legend={legend}>
      <div className="h-56">{children}</div>
    </UIPanel>
  )
}


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
      legend={hosts.map((h, i) => ({ label: h.name, color: entityAt(i) }))}
      hint="One line per machine, measured on the wall clock: tokens the host emitted per second it actually spent decoding. Concurrent requests share the same second, so this RISES with batching — unlike the per-request rate above, which sags because each stream slows down. The gap between the two charts is the batching win. Only requests logged after this feature shipped carry decode timestamps; earlier history stays blank."
    >
      <ResponsiveContainer width="100%" height="100%">
        <LineChart data={rows}>
          <CartesianGrid vertical={false} stroke={GRID} />
          <XAxis dataKey="label" {...AXIS} />
          <YAxis {...AXIS} unit="" />
          <Tooltip {...TOOLTIP} formatter={(v: number) => `${v.toLocaleString()} tok/s`} />
          {hosts.map((h, i) => (
            <Line key={h.id} type="monotone" dataKey={`host-${h.id}`} name={h.name}
              stroke={entityAt(i)} dot={false} strokeWidth={2} connectNulls />
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
      <div className="bg-crit-500/10 border border-crit-500/30 rounded-lg p-4 text-sm text-crit-300">
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
      <div className="text-xs text-slate-500">{(data as ThroughputSeries).interval} buckets</div>

      <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
        <Panel
          title="Prefill speed"
          hint="Freshly evaluated prompt tokens (KV-cache hits excluded) ÷ time from send to first token, token-weighted per bucket. Cached tokens were read, not processed — counting them would report cache reads as GPU speed. Drops when prompts grow or the KV cache misses; a shared host also pays batch interference."
        >
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={rows}>
              <CartesianGrid vertical={false} stroke={GRID} />
              <XAxis dataKey="label" {...AXIS} />
              <YAxis {...AXIS} unit="" />
              <Tooltip {...TOOLTIP} formatter={(v: number) => `${v.toLocaleString()} tok/s`} />
              <Line type="monotone" dataKey="prefill" name="prefill tok/s"
                stroke={sem.input} dot={false} strokeWidth={2} connectNulls />
            </LineChart>
          </ResponsiveContainer>
        </Panel>

        <Panel
          title="Decode speed"
          hint="Generated tokens ÷ time since first token, token-weighted per bucket. The number that decides how fast answers finish typing — watch it sag under concurrent load."
        >
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={rows}>
              <CartesianGrid vertical={false} stroke={GRID} />
              <XAxis dataKey="label" {...AXIS} />
              <YAxis {...AXIS} unit="" />
              <Tooltip {...TOOLTIP} formatter={(v: number) => `${v.toLocaleString()} tok/s`} />
              <Line type="monotone" dataKey="decode" name="decode tok/s"
                stroke={sem.output} dot={false} strokeWidth={2} connectNulls />
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
                <CartesianGrid vertical={false} stroke={GRID} />
                <XAxis dataKey="label" {...AXIS} />
                <YAxis {...AXIS} unit="%" domain={[0, 100]} />
                <Tooltip {...TOOLTIP} formatter={(v: number) => `${v}%`} />
                <Line type="monotone" dataKey="accept" name="accepted"
                  stroke={sem.output} dot={false} strokeWidth={2} connectNulls />
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
