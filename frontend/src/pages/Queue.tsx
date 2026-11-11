import { useQuery } from '@tanstack/react-query'
import { queueApi } from '../api/client'
import { QueueEntry } from '../types'
import { Brain, Clock3, Loader2, Radio, Server, Activity } from 'lucide-react'
import { useBackendColors } from '../utils/colors'

// ── Phase badge ───────────────────────────────────────────────────────────────

function PhaseBadge({ status }: { status: QueueEntry['status'] }) {
  const cfg = {
    queued:            { label: 'Queued',            color: 'bg-amber-500/20 text-amber-300 border-amber-700/40', Icon: Clock3, spin: false },
    processing_prompt: { label: 'Processing prompt', color: 'bg-blue-500/20 text-blue-300 border-blue-700/40',   Icon: Loader2,  spin: true  },
    thinking:          { label: 'Thinking',           color: 'bg-purple-500/20 text-purple-300 border-purple-700/40', Icon: Brain, spin: false },
    streaming:         { label: 'Streaming',          color: 'bg-green-500/20 text-green-300 border-green-700/40',  Icon: Radio, spin: false },
  }[status] ?? { label: status, color: 'bg-slate-500/20 text-slate-400 border-slate-600', Icon: Activity, spin: false }

  const { Icon, spin, label, color } = cfg
  return (
    <span className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-xs border ${color}`}>
      <Icon className={`w-3 h-3 ${spin ? 'animate-spin' : ''}`} />
      {label}
    </span>
  )
}

// ── Duration ──────────────────────────────────────────────────────────────────

function ms(n: number) {
  if (n < 1000) return `${n}ms`
  return `${(n / 1000).toFixed(1)}s`
}

// ── Prefill progress ──────────────────────────────────────────────────────────

// When the backend reports real prompt progress (Halogen's prompt_progress
// stream events) the bar is engine-truthful: processed ÷ total tokens actually
// evaluated, straight from the host. Otherwise it falls back to the estimate —
// estimated prompt size ÷ (learned prompt tok/s × elapsed) — capped at 95%
// until the phase actually flips, because claiming a finished bar while the
// host is still silent would be a lie. Cold backends (no learned speed and no
// real progress) get an indeterminate shimmer.
function PrefillProgress({ entry, learnedTokS }: { entry: QueueEntry; learnedTokS: number }) {
  if (entry.status !== 'processing_prompt') return null

  const real = entry.prompt_total > 0
  let pct = 0
  const estimated = entry.est_prompt_tokens > 0 && learnedTokS > 0
  if (real) {
    // Cache hits count as done instantly — they were never evaluated, but the
    // client shouldn't watch the bar crawl through tokens that were free.
    const done = Math.max(entry.prompt_processed, 0) + Math.min(entry.prompt_cached, entry.prompt_total)
    pct = Math.min(99, (done / entry.prompt_total) * 100)
  } else if (estimated) {
    const etaMs = (entry.est_prompt_tokens / learnedTokS) * 1000
    pct = Math.min(95, (entry.phase_ms / etaMs) * 100)
  }

  const tip = real
    ? `${entry.prompt_processed}/${entry.prompt_total} tok evaluated` +
      (entry.prompt_cached > 0 ? `, ${entry.prompt_cached} cached` : '') +
      `, ${entry.prompt_engine_ms}ms on host`
    : estimated
      ? `Est. ${entry.est_prompt_tokens} tok ÷ learned ${learnedTokS.toFixed(0)} tok/s`
      : 'No learned speed for this host yet'

  return (
    <div className="mt-1.5 w-28" title={tip}>
      <div className="h-1 rounded-full bg-slate-700 overflow-hidden">
        {real ? (
          <div
            className="h-full rounded-full bg-emerald-400 transition-[width] duration-500 ease-linear"
            style={{ width: `${pct.toFixed(1)}%` }}
          />
        ) : estimated ? (
          <div
            className="h-full rounded-full bg-blue-400 transition-[width] duration-1000 ease-linear"
            style={{ width: `${pct.toFixed(1)}%` }}
          />
        ) : (
          <div className="h-full w-1/3 rounded-full bg-blue-400/60 animate-pulse" />
        )}
      </div>
    </div>
  )
}

// ── Speed cell ────────────────────────────────────────────────────────────────

function tokS(n: number) {
  if (n <= 0) return '—'
  return n >= 100 ? `${Math.round(n)}` : n.toFixed(1)
}

// ── Per-backend summary ───────────────────────────────────────────────────────

function BackendSummary({ entries, colorsById }: { entries: QueueEntry[]; colorsById: Map<number, string> }) {
  const byBackend = new Map<number, { name: string; entries: QueueEntry[] }>()
  for (const e of entries) {
    if (!byBackend.has(e.backend_id)) byBackend.set(e.backend_id, { name: e.backend_name, entries: [] })
    byBackend.get(e.backend_id)!.entries.push(e)
  }

  if (byBackend.size === 0) return null

  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3 mb-6">
      {[...byBackend.entries()].map(([id, { name, entries: bEntries }]) => {
        const color = colorsById.get(id) ?? '#3b82f6'
        const counts = { queued: 0, processing_prompt: 0, thinking: 0, streaming: 0 }
        bEntries.forEach(e => counts[e.status]++)
        return (
          <div key={id} className="bg-slate-800 rounded-lg p-4 border border-slate-700">
            <div className="flex items-center gap-2 mb-3">
              <span className="w-2 h-2 rounded-full flex-shrink-0" style={{ backgroundColor: color }} />
              <span className="font-medium text-sm">{name}</span>
              <span className="ml-auto text-xs text-slate-400">{bEntries.length} live</span>
            </div>
            <div className="flex gap-3 text-xs">
              {counts.queued > 0 && (
                <span className="text-amber-400 flex items-center gap-1">
                  <Clock3 className="w-3 h-3" />{counts.queued}
                </span>
              )}
              {counts.processing_prompt > 0 && (
                <span className="text-blue-400 flex items-center gap-1">
                  <Loader2 className="w-3 h-3 animate-spin" />{counts.processing_prompt}
                </span>
              )}
              {counts.thinking > 0 && (
                <span className="text-purple-400 flex items-center gap-1">
                  <Brain className="w-3 h-3" />{counts.thinking}
                </span>
              )}
              {counts.streaming > 0 && (
                <span className="text-green-400 flex items-center gap-1">
                  <Radio className="w-3 h-3" />{counts.streaming}
                </span>
              )}
            </div>
          </div>
        )
      })}
    </div>
  )
}

// ── Page ──────────────────────────────────────────────────────────────────────

export default function Queue() {
  const colorsById = useBackendColors()

  const { data } = useQuery({
    queryKey: ['queue'],
    queryFn: queueApi.snapshot,
    refetchInterval: 1000,
    staleTime: 0,
  })

  // Learned per-backend speeds change slowly — 5s is plenty.
  const { data: speedData } = useQuery({
    queryKey: ['queue-speeds'],
    queryFn: queueApi.speeds,
    refetchInterval: 5000,
  })
  const learnedPromptTokS = new Map((speedData?.speeds ?? []).map(s => [s.backend_id, s.prompt_tok_s]))

  const entries = data?.active ?? []
  const count = data?.count ?? 0
  const queued = entries.filter(e => e.status === 'queued').length

  return (
    <div>
      <div className="flex items-center gap-3 mb-6">
        <h2 className="text-2xl font-bold">Live Queue</h2>
        <span className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium border ${
          count > 0
            ? 'bg-green-500/10 text-green-400 border-green-700/40'
            : 'bg-slate-700/50 text-slate-400 border-slate-600'
        }`}>
          <span className={`w-1.5 h-1.5 rounded-full ${count > 0 ? 'bg-green-400 animate-pulse' : 'bg-slate-500'}`} />
          {count} live
        </span>
        {queued > 0 && (
          <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium border bg-amber-500/10 text-amber-300 border-amber-700/40">
            <Clock3 className="w-3 h-3" />
            {queued} queued
          </span>
        )}
      </div>

      <BackendSummary entries={entries} colorsById={colorsById} />

      {entries.length === 0 ? (
        <div className="flex flex-col items-center justify-center h-48 bg-slate-800 rounded-lg border border-slate-700 text-slate-500">
          <Server className="w-8 h-8 mb-2 opacity-30" />
          <p className="text-sm">No requests in flight</p>
        </div>
      ) : (
        <div className="bg-slate-800 rounded-lg border border-slate-700 overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-700">
                <th className="text-left px-4 py-3 text-xs text-slate-400">Host</th>
                <th className="text-left px-4 py-3 text-xs text-slate-400">Model</th>
                <th className="text-left px-4 py-3 text-xs text-slate-400">Client</th>
                <th className="text-left px-4 py-3 text-xs text-slate-400">Status</th>
                <th className="text-left px-4 py-3 text-xs text-slate-400 whitespace-nowrap" title="Estimated: freshly evaluated prompt tokens (KV-cache hits excluded) ÷ measured prefill time">Prefill tok/s</th>
                <th className="text-left px-4 py-3 text-xs text-slate-400 whitespace-nowrap" title="Measured: streamed tokens ÷ time since first token">Decode tok/s</th>
                <th className="text-left px-4 py-3 text-xs text-slate-400 whitespace-nowrap">In phase</th>
                <th className="text-left px-4 py-3 text-xs text-slate-400">Elapsed</th>
              </tr>
            </thead>
            <tbody>
              {entries.map(e => (
                <tr key={e.id} className="border-b border-slate-700/50 last:border-0">
                  <td className="px-4 py-3">
                    <span className="inline-flex items-center gap-1.5 text-slate-300">
                      {colorsById.get(e.backend_id) && (
                        <span className="w-1.5 h-1.5 rounded-full flex-shrink-0" style={{ backgroundColor: colorsById.get(e.backend_id) }} />
                      )}
                      {e.backend_name}
                    </span>
                  </td>
                  <td className="px-4 py-3 font-mono text-slate-200">{e.model}</td>
                  <td className="px-4 py-3 font-mono text-slate-400 text-xs">{e.client_key ?? '-'}</td>
                  <td className="px-4 py-3">
                    <PhaseBadge status={e.status} />
                    <PrefillProgress entry={e} learnedTokS={learnedPromptTokS.get(e.backend_id) ?? 0} />
                  </td>
                  <td className="px-4 py-3 text-slate-400 tabular-nums">{tokS(e.prompt_tok_s)}</td>
                  <td className="px-4 py-3 text-slate-300 tabular-nums">{tokS(e.decode_tok_s)}</td>
                  <td className="px-4 py-3 text-slate-400 tabular-nums">{ms(e.phase_ms)}</td>
                  <td className="px-4 py-3 text-slate-500 tabular-nums">{ms(e.elapsed_ms)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
