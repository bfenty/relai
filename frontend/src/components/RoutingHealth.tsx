import { useQuery } from '@tanstack/react-query'
import { GitCompareArrows, Zap, Database, Server, AlertTriangle } from 'lucide-react'
import { analyticsApi } from '../api/client'
import type { RoutingStats } from '../types'

/** Bytes in the most readable unit, for savings that range from KB to GB. */
function bytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 ** 2) return `${(n / 1024).toFixed(1)} KB`
  if (n < 1024 ** 3) return `${(n / 1024 ** 2).toFixed(1)} MB`
  return `${(n / 1024 ** 3).toFixed(2)} GB`
}

const pct = (v: number) => `${(v * 100).toFixed(1)}%`
const num = (n: number) => n.toLocaleString()

/** Green when healthy, amber when marginal, red when it needs attention. */
function rateTone(v: number, good: number, poor: number): string {
  if (v >= good) return 'text-green-400'
  if (v >= poor) return 'text-amber-400'
  return 'text-red-400'
}

function Stat({ label, value, tone, hint }: {
  label: string; value: string; tone?: string; hint?: string
}) {
  return (
    <div className="bg-slate-800/60 rounded-lg border border-slate-700 p-4">
      <div className="text-xs text-slate-400 mb-1">{label}</div>
      <div className={`text-2xl font-semibold ${tone ?? 'text-white'}`}>{value}</div>
      {hint && <div className="text-xs text-slate-500 mt-1">{hint}</div>}
    </div>
  )
}

export default function RoutingHealth({ hours = 24 }: { hours?: number }) {
  const { data, isLoading, isError, error } = useQuery({
    queryKey: ['analytics-routing', hours],
    queryFn: () => analyticsApi.routing(hours),
    refetchInterval: 30000,
    placeholderData: prev => prev,
  })

  if (isLoading && !data) {
    return <div className="text-slate-400 text-sm">Loading routing health…</div>
  }
  if (isError) {
    return (
      <div className="bg-red-500/10 border border-red-500/30 rounded-lg p-4 text-sm text-red-300">
        Failed to load routing analytics: {(error as Error)?.message ?? 'unknown error'}
      </div>
    )
  }
  const d = data as RoutingStats
  if (!d) return null

  const measured = d.measured_requests
  const noData = measured === 0

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between flex-wrap gap-2">
        <h3 className="text-lg font-semibold flex items-center gap-2">
          <Zap className="w-5 h-5 text-amber-400" /> Routing &amp; cache health
        </h3>
        <span className="text-xs text-slate-500">
          {num(measured)} measured of {num(d.total_requests)} requests · last {hours}h
        </span>
      </div>

      {/* Rows logged before routing was recorded have no cache data, so they are
          excluded from the rates rather than dragging them toward zero. */}
      {d.unmeasured_requests > 0 && (
        <div className="flex items-start gap-2 text-xs text-slate-400 bg-slate-800/40 border border-slate-700 rounded-lg p-3">
          <AlertTriangle className="w-4 h-4 text-amber-400 shrink-0 mt-0.5" />
          <span>
            {num(d.unmeasured_requests)} request{d.unmeasured_requests === 1 ? '' : 's'} predate
            routing instrumentation and carry no cache data. They are excluded from the
            rates below so the percentages reflect only what was actually measured.
          </span>
        </div>
      )}

      {noData ? (
        <div className="text-sm text-slate-400 bg-slate-800/40 border border-slate-700 rounded-lg p-4">
          No instrumented requests in this window yet.
        </div>
      ) : (
        <>
          <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-4 gap-3">
            <Stat
              label="Cache reuse"
              value={pct(d.cache_reuse_rate)}
              tone={rateTone(d.cache_reuse_rate, 0.6, 0.3)}
              hint="Share of prompt tokens served from the upstream KV cache"
            />
            <Stat
              label="Sticky hit rate"
              value={pct(d.sticky_hit_rate)}
              tone={rateTone(d.sticky_hit_rate, 0.6, 0.3)}
              hint="Conversations routed back to the host holding their context"
            />
            <Stat
              label="Reprocessed"
              value={num(d.reprocessed_tokens)}
              tone={d.reprocessed_tokens > 0 ? 'text-amber-400' : 'text-green-400'}
              hint="Prompt tokens the upstreams had to prefill again"
            />
            <Stat
              label="Compression saved"
              value={bytes(d.compression_bytes_saved)}
              tone={d.compression_bytes_saved > 0 ? 'text-green-400' : 'text-slate-400'}
              hint={d.compressed_requests > 0
                ? `across ${num(d.compressed_requests)} compressed request${d.compressed_requests === 1 ? '' : 's'}`
                : 'no target has compression enabled'}
            />
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
            <Stat label="Spill rate" value={pct(d.spill_rate)}
              tone={d.spill_rate > 0.1 ? 'text-amber-400' : 'text-slate-300'}
              hint="Gave up on the warm host and paid a reprocess" />
            <Stat label="Mean wait" value={`${num(d.mean_wait_ms)} ms`}
              hint="Time spent waiting for a host" />
            <Stat label="p95 wait" value={`${num(d.p95_wait_ms)} ms`}
              hint="Worst-case wait for a host" />
          </div>

          {d.decision_stats && Object.keys(d.decision_stats).length > 0 && (
            <div>
              <h4 className="text-sm font-medium text-slate-300 mb-2 flex items-center gap-2">
                <GitCompareArrows className="w-4 h-4 text-emerald-400" /> Per decision economics
              </h4>
              <p className="text-xs text-slate-500 mb-2">
                Spill verdict: spills are saving time when sticky_spill shows low mean wait and
                modest reprocessed tokens while sticky_wait waits run long. The reverse means
                spills are paying cache misses for nothing.
              </p>
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="text-left text-xs text-slate-400 border-b border-slate-700">
                      <th className="py-2 pr-4 font-medium">Decision</th>
                      <th className="py-2 pr-4 font-medium text-right">Requests</th>
                      <th className="py-2 pr-4 font-medium text-right">Mean wait</th>
                      <th className="py-2 pr-4 font-medium text-right">Mean latency</th>
                      <th className="py-2 pr-4 font-medium text-right">Reprocessed tok</th>
                      <th className="py-2 pr-4 font-medium text-right">Cache reuse</th>
                    </tr>
                  </thead>
                  <tbody>
                    {Object.entries(d.decision_stats)
                      .sort((a, b) => b[1].requests - a[1].requests)
                      .map(([name, st]) => {
                        const reuse = st.prompt_tokens > 0 ? st.cached_tokens / st.prompt_tokens : 0
                        return (
                          <tr key={name} className="border-b border-slate-700/40">
                            <td className="py-1.5 pr-4 font-mono text-slate-200">{name}</td>
                            <td className="py-1.5 pr-4 text-right tabular-nums">{num(st.requests)}</td>
                            <td className="py-1.5 pr-4 text-right tabular-nums">{num(st.mean_wait_ms)} ms</td>
                            <td className="py-1.5 pr-4 text-right tabular-nums">{num(st.mean_latency_ms)} ms</td>
                            <td className="py-1.5 pr-4 text-right tabular-nums">{num(st.reprocessed_tokens)}</td>
                            <td className={`py-1.5 pr-4 text-right tabular-nums ${rateTone(reuse, 0.6, 0.3)}`}>
                              {pct(reuse)}
                            </td>
                          </tr>
                        )
                      })}
                  </tbody>
                </table>
              </div>
            </div>
          )}

          <div>
            <h4 className="text-sm font-medium text-slate-300 mb-2 flex items-center gap-2">
              <Server className="w-4 h-4 text-blue-400" /> Per host
            </h4>
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="text-left text-xs text-slate-400 border-b border-slate-700">
                    <th className="py-2 pr-4 font-medium">Host</th>
                    <th className="py-2 pr-4 font-medium text-right">Requests</th>
                    <th className="py-2 pr-4 font-medium text-right">Share</th>
                    <th className="py-2 pr-4 font-medium text-right">Cache reuse</th>
                    <th className="py-2 pr-4 font-medium text-right">Mean latency</th>
                    <th className="py-2 font-medium text-right">Compression</th>
                  </tr>
                </thead>
                <tbody>
                  {d.hosts.map(h => (
                    <tr key={h.backend_id ?? h.backend_name}
                      className="border-b border-slate-700/50 hover:bg-slate-700/20">
                      <td className="py-2 pr-4">{h.backend_name}</td>
                      <td className="py-2 pr-4 text-right tabular-nums">{num(h.requests)}</td>
                      <td className="py-2 pr-4 text-right tabular-nums">{pct(h.share)}</td>
                      <td className={`py-2 pr-4 text-right tabular-nums ${rateTone(h.cache_reuse_rate, 0.6, 0.3)}`}>
                        {pct(h.cache_reuse_rate)}
                      </td>
                      <td className="py-2 pr-4 text-right tabular-nums">{num(h.mean_latency_ms)} ms</td>
                      <td className="py-2 text-right tabular-nums">
                        {h.compression_bytes_saved > 0 ? bytes(h.compression_bytes_saved) : '—'}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>

          <div>
            <h4 className="text-sm font-medium text-slate-300 mb-2 flex items-center gap-2">
              <Database className="w-4 h-4 text-purple-400" /> Routing decisions
            </h4>
            <div className="flex flex-wrap gap-2">
              {Object.entries(d.decisions)
                .sort((a, b) => b[1] - a[1])
                .map(([k, v]) => (
                  <span key={k}
                    className="px-2.5 py-1 rounded-full text-xs bg-slate-800 border border-slate-700 text-slate-300">
                    {k.replace(/_/g, ' ')}: <span className="tabular-nums font-medium">{num(v)}</span>
                  </span>
                ))}
            </div>
          </div>
        </>
      )}
    </div>
  )
}
