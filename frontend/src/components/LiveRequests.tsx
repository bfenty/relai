import { QueueEntry } from '../types'
import { Brain, Clock3, Loader2, Radio, Activity } from 'lucide-react'

// Requests in flight, one row each: where they are, which phase, and how fast.
// Prefill progress is engine-truthful when the host reports it, estimated otherwise.

// ── Phase badge ───────────────────────────────────────────────────────────────

function PhaseBadge({ status }: { status: QueueEntry['status'] }) {
  const cfg = {
    queued:            { label: 'Queued',            color: 'bg-slate-500/20 text-slate-300 border-slate-600', Icon: Clock3, spin: false },
    processing_prompt: { label: 'Processing prompt', color: 'bg-input-500/20 text-input-300 border-input-700/40',   Icon: Loader2,  spin: true  },
    thinking:          { label: 'Thinking',           color: 'bg-output-500/20 text-output-300 border-output-700/40', Icon: Brain, spin: false },
    streaming:         { label: 'Streaming',          color: 'bg-output-500/25 text-output-200 border-output-600/50',  Icon: Radio, spin: false },
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
            className="h-full rounded-full bg-input-400 transition-[width] duration-500 ease-linear"
            style={{ width: `${pct.toFixed(1)}%` }}
          />
        ) : estimated ? (
          <div
            className="h-full rounded-full bg-input-400/70 transition-[width] duration-1000 ease-linear"
            style={{ width: `${pct.toFixed(1)}%` }}
          />
        ) : (
          <div className="h-full w-1/3 rounded-full bg-input-400/40 animate-pulse" />
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


export function LiveRequestsTable({ entries, colorsById, learnedPromptTokS }: {
  entries: QueueEntry[]
  colorsById: Map<number, string>
  learnedPromptTokS: Map<number, number>
}) {
  if (entries.length === 0) {
    return <div className="py-10 text-center text-sm text-slate-500">No requests in flight</div>
  }
  return (
    <div className="overflow-x-auto">
      <table className="table-pro">
        <thead>
          <tr>
            <th>Host</th>
            <th>Model</th>
            <th>Client</th>
            <th>Status</th>
            <th className="whitespace-nowrap" title="Estimated: freshly evaluated prompt tokens (KV-cache hits excluded) ÷ measured prefill time">Prefill tok/s</th>
            <th className="whitespace-nowrap" title="Measured: streamed tokens ÷ time since first token">Decode tok/s</th>
            <th className="whitespace-nowrap">In phase</th>
            <th>Elapsed</th>
          </tr>
        </thead>
        <tbody>
          {entries.map(e => (
            <tr key={e.id}>
              <td>
                <span className="inline-flex items-center gap-1.5 text-slate-300">
                  {colorsById.get(e.backend_id) && (
                    <span className="w-1.5 h-1.5 rounded-full flex-shrink-0" style={{ backgroundColor: colorsById.get(e.backend_id) }} />
                  )}
                  {e.backend_name}
                </span>
              </td>
              <td className="font-mono text-slate-200">{e.model}</td>
              <td className="font-mono text-slate-400 text-xs">{e.client_key ?? '-'}</td>
              <td>
                <PhaseBadge status={e.status} />
                <PrefillProgress entry={e} learnedTokS={learnedPromptTokS.get(e.backend_id) ?? 0} />
              </td>
              <td className="text-slate-400 font-mono tabular">{tokS(e.prompt_tok_s)}</td>
              <td className="text-slate-300 font-mono tabular">{tokS(e.decode_tok_s)}</td>
              <td className="text-slate-400 font-mono tabular">{ms(e.phase_ms)}</td>
              <td className="text-slate-500 font-mono tabular">{ms(e.elapsed_ms)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
