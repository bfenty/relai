import { useQuery } from '@tanstack/react-query'
import {
  ResponsiveContainer, BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip, Legend,
} from 'recharts'
import { analyticsApi } from '../api/client'
import type { PrefixCause, PrefixStats } from '../types'

/**
 * Why prompts missed the backend's KV cache.
 *
 * Cache reuse already reports that a miss happened. This reports the cause, which is
 * the part that can be acted on: a rewritten system prompt is the client's to fix, a
 * conversation moved between hosts is the router's, and a recycled slot is a sizing
 * problem. Everything is ranked by reprocessed tokens rather than request count,
 * because one miss at deep context outweighs a hundred shallow ones.
 */

const CAUSES: Record<PrefixCause, { label: string; color: string; blurb: string }> = {
  append: {
    label: 'Append (healthy)', color: '#22c55e',
    blurb: 'History untouched — only new messages added. Nothing was forfeited.',
  },
  system: {
    label: 'System prompt changed', color: '#ef4444',
    blurb: 'A system message differed, invalidating the entire prompt. Usually a timestamp or session id injected into the system prompt — the most expensive fault there is, and normally the easiest to fix.',
  },
  head: {
    label: 'Early message rewritten', color: '#f97316',
    blurb: 'A message near the start of the conversation changed, forfeiting everything after it.',
  },
  tail: {
    label: 'Last message rewritten', color: '#eab308',
    blurb: 'Only the final turn or two changed — a retried tool call or an edited message. Cheap, and usually legitimate.',
  },
  truncated: {
    label: 'History trimmed', color: '#a855f7',
    blurb: 'The client dropped messages from the front. A sliding context window forfeits the whole cached prefix every time it slides.',
  },
  host_change: {
    label: 'Moved to another host', color: '#3b82f6',
    blurb: 'History was intact but the conversation landed on a different backend, which had no prefix to reuse. A routing cost, not a client one.',
  },
  evicted: {
    label: 'Slot recycled', color: '#06b6d4',
    blurb: 'History was intact and the host was the same, but the cached prefix was gone — the backend is juggling more conversations than its slots or cache-ram can hold.',
  },
  new_conversation: {
    label: 'New conversation', color: '#64748b',
    blurb: 'Nothing recent resembled this prompt. Cold by nature, not a fault.',
  },
}

function fmtDuration(seconds: number): string {
  if (seconds < 1) return '<1s'
  if (seconds < 90) return `${seconds.toFixed(0)}s`
  if (seconds < 5400) return `${(seconds / 60).toFixed(1)} min`
  return `${(seconds / 3600).toFixed(1)} h`
}

function fmtTokens(n: number): string {
  if (n >= 1e9) return `${(n / 1e9).toFixed(1)}B`
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`
  if (n >= 1e3) return `${(n / 1e3).toFixed(1)}k`
  return String(n)
}

function Stat({ label, value, hint, tone = 'text-white' }: {
  label: string; value: string; hint?: string; tone?: string
}) {
  return (
    <div className="bg-slate-800 rounded-lg p-4 border border-slate-700">
      <div className="text-xs text-slate-400 mb-1">{label}</div>
      <div className={`text-2xl font-bold tabular-nums ${tone}`}>{value}</div>
      {hint && <div className="text-xs text-slate-500 mt-1">{hint}</div>}
    </div>
  )
}

export default function PrefixHealth({ hours }: { hours: number }) {
  const { data, isLoading, isError, error } = useQuery({
    queryKey: ['prefix-analytics', hours],
    queryFn: () => analyticsApi.prefix(hours),
    refetchInterval: 30000,
    placeholderData: prev => prev,
  })

  const { data: series } = useQuery({
    queryKey: ['prefix-series', hours],
    queryFn: () => analyticsApi.prefixSeries(hours),
    refetchInterval: 60000,
    placeholderData: prev => prev,
  })

  if (isError) {
    return <div className="text-sm text-red-400">{(error as Error)?.message || 'Failed to load'}</div>
  }
  if (isLoading && !data) {
    return (
      <div className="h-32 flex items-center justify-center">
        <div className="animate-spin w-6 h-6 border-2 border-blue-400 border-t-transparent rounded-full" />
      </div>
    )
  }

  const s: PrefixStats | undefined = data
  if (!s || s.diagnosed_requests === 0) {
    return (
      <div className="text-sm text-slate-400 bg-slate-800/60 rounded-lg p-4 border border-slate-700">
        No diagnosed requests in this range yet. Diagnosis is recorded for traffic
        routed through a virtual router; requests logged before this feature shipped
        carry no cause and are excluded rather than counted as healthy.
      </div>
    )
  }

  const faultRate = s.diagnosed_requests > 0 ? (s.fault_requests / s.diagnosed_requests) * 100 : 0
  const worst = s.causes.find(c => c.cause !== 'append' && c.cause !== 'new_conversation')

  const chartData = (series?.points ?? []).map(p => ({
    label: new Date(p.time).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: 'numeric' }),
    time: p.time,
    ...Object.fromEntries(
      (Object.keys(CAUSES) as PrefixCause[]).map(c => [CAUSES[c].label, p[c]]),
    ),
  }))

  return (
    <div className="space-y-5">
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        <Stat label="Diagnosed requests" value={s.diagnosed_requests.toLocaleString()} />
        <Stat label="Attributed faults" value={`${faultRate.toFixed(1)}%`}
          tone={faultRate > 20 ? 'text-orange-400' : 'text-white'}
          hint={`${s.fault_requests.toLocaleString()} requests`} />
        <Stat label="Reprocessed tokens" value={fmtTokens(s.reprocessed_tokens)}
          hint="prompt re-evaluated from scratch" />
        <Stat label="GPU time lost" value={fmtDuration(s.estimated_seconds)}
          tone={s.estimated_seconds > 600 ? 'text-red-400' : 'text-white'}
          hint="at ~259 tok/s prefill" />
      </div>

      {worst && (
        <div className="text-sm rounded-lg p-4 border bg-slate-800/60 border-slate-700">
          <span className="text-slate-400">Biggest cost: </span>
          <span className="font-semibold" style={{ color: CAUSES[worst.cause]?.color }}>
            {CAUSES[worst.cause]?.label ?? worst.cause}
          </span>
          <span className="text-slate-400">
            {' '}— {fmtDuration(worst.estimated_seconds)} across {worst.requests.toLocaleString()} requests.
          </span>
          <div className="text-xs text-slate-500 mt-1.5">{CAUSES[worst.cause]?.blurb}</div>
        </div>
      )}

      {/* Cause breakdown, ranked by cost rather than frequency. */}
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-xs text-slate-400 border-b border-slate-700">
              <th className="py-2 pr-4 font-medium">Cause</th>
              <th className="py-2 px-3 font-medium text-right">Requests</th>
              <th className="py-2 px-3 font-medium text-right">Share</th>
              <th className="py-2 px-3 font-medium text-right">Reprocessed</th>
              <th className="py-2 pl-3 font-medium text-right">GPU time</th>
            </tr>
          </thead>
          <tbody>
            {s.causes.map(c => {
              const meta = CAUSES[c.cause]
              return (
                <tr key={c.cause} className="border-b border-slate-700/50 last:border-0">
                  <td className="py-2.5 pr-4">
                    <div className="flex items-center gap-2">
                      <span className="w-2.5 h-2.5 rounded-sm shrink-0"
                        style={{ backgroundColor: meta?.color ?? '#64748b' }} />
                      <span className="font-medium">{meta?.label ?? c.cause}</span>
                    </div>
                    {meta && <div className="text-xs text-slate-500 mt-0.5 ml-4.5">{meta.blurb}</div>}
                  </td>
                  <td className="py-2.5 px-3 text-right tabular-nums text-slate-300">
                    {c.requests.toLocaleString()}
                  </td>
                  <td className="py-2.5 px-3 text-right tabular-nums text-slate-400">
                    {(c.share * 100).toFixed(1)}%
                  </td>
                  <td className="py-2.5 px-3 text-right tabular-nums text-slate-300">
                    {fmtTokens(c.reprocessed_tokens)}
                  </td>
                  <td className="py-2.5 pl-3 text-right tabular-nums text-slate-300">
                    {c.estimated_seconds > 0 ? fmtDuration(c.estimated_seconds) : '—'}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>

      {chartData.length > 1 && (
        <div>
          <h4 className="text-xs text-slate-400 mb-2">Cause mix over time</h4>
          <div className="h-56">
            <ResponsiveContainer width="100%" height="100%">
              <BarChart data={chartData} margin={{ top: 0, right: 8, bottom: 0, left: 0 }}>
                <CartesianGrid strokeDasharray="3 3" stroke="#1e293b" />
                <XAxis dataKey="label" tick={{ fill: '#94a3b8', fontSize: 11 }} interval="preserveStartEnd" />
                <YAxis tick={{ fill: '#94a3b8', fontSize: 11 }} allowDecimals={false} />
                <Tooltip
                  contentStyle={{ backgroundColor: '#1e293b', border: '1px solid #334155', borderRadius: 6, fontSize: 12 }}
                  labelFormatter={(_, items) => items?.[0]?.payload?.time
                    ? new Date(items[0].payload.time).toLocaleString() : ''}
                />
                <Legend wrapperStyle={{ fontSize: 11 }} />
                {(Object.keys(CAUSES) as PrefixCause[]).map(c => (
                  <Bar key={c} dataKey={CAUSES[c].label} stackId="c" fill={CAUSES[c].color} />
                ))}
              </BarChart>
            </ResponsiveContainer>
          </div>
        </div>
      )}

      {s.offenders.length > 0 && (
        <div>
          <h4 className="text-xs text-slate-400 mb-2">
            Where the faults come from — ranked by GPU time lost
          </h4>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-slate-400 border-b border-slate-700">
                  <th className="py-2 pr-4 font-medium">Model</th>
                  <th className="py-2 px-3 font-medium">API key</th>
                  <th className="py-2 px-3 font-medium">Cause</th>
                  <th className="py-2 px-3 font-medium text-right">Requests</th>
                  <th className="py-2 pl-3 font-medium text-right">GPU time</th>
                </tr>
              </thead>
              <tbody>
                {s.offenders.map((o, i) => (
                  <tr key={i} className="border-b border-slate-700/50 last:border-0">
                    <td className="py-2 pr-4 font-medium truncate">{o.model || '—'}</td>
                    <td className="py-2 px-3 text-slate-400 truncate">{o.api_key_name || '—'}</td>
                    <td className="py-2 px-3">
                      <span className="text-xs px-2 py-0.5 rounded-full"
                        style={{
                          backgroundColor: `${CAUSES[o.cause]?.color ?? '#64748b'}20`,
                          color: CAUSES[o.cause]?.color ?? '#94a3b8',
                        }}>
                        {CAUSES[o.cause]?.label ?? o.cause}
                      </span>
                    </td>
                    <td className="py-2 px-3 text-right tabular-nums text-slate-300">
                      {o.requests.toLocaleString()}
                    </td>
                    <td className="py-2 pl-3 text-right tabular-nums text-slate-300">
                      {fmtDuration(o.estimated_seconds)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  )
}
