import { useQuery } from '@tanstack/react-query'
import { modelMappingsApi, routersApi, proxyChat } from '../api/client'
import { ModelMap, Router } from '../types'
import {
  BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer,
} from 'recharts'
import { useState, useRef } from 'react'
import { Play, Square, Gauge } from 'lucide-react'

interface RunResult {
  latencyMs: number
  tokens: number
  error?: string
}

interface Stats {
  total: number
  errors: number
  min: number
  max: number
  avg: number
  p50: number
  p95: number
  p99: number
  tokensPerSec: number
}

function pct(sorted: number[], p: number): number {
  if (!sorted.length) return 0
  return sorted[Math.min(Math.floor(sorted.length * p / 100), sorted.length - 1)]
}

function computeStats(results: RunResult[]): Stats {
  const ok = results.filter(r => !r.error)
  const lat = ok.map(r => r.latencyMs).sort((a, b) => a - b)
  const totalTokens = ok.reduce((s, r) => s + r.tokens, 0)
  const totalMs = ok.reduce((s, r) => s + r.latencyMs, 0)
  return {
    total: results.length,
    errors: results.length - ok.length,
    min: lat[0] ?? 0,
    max: lat[lat.length - 1] ?? 0,
    avg: lat.length ? totalMs / lat.length : 0,
    p50: pct(lat, 50),
    p95: pct(lat, 95),
    p99: pct(lat, 99),
    tokensPerSec: totalMs > 0 ? (totalTokens / totalMs) * 1000 : 0,
  }
}

function buildHistogram(results: RunResult[], buckets = 12) {
  const ok = results.filter(r => !r.error).map(r => r.latencyMs)
  if (!ok.length) return []
  const min = Math.min(...ok)
  const max = Math.max(...ok)
  const width = max === min ? 1 : (max - min) / buckets
  const counts = Array.from({ length: buckets }, (_, i) => ({
    label: `${((min + i * width) / 1000).toFixed(1)}s`,
    count: 0,
  }))
  for (const v of ok) {
    const idx = Math.min(Math.floor((v - min) / width), buckets - 1)
    counts[idx].count++
  }
  return counts
}

function StatBox({ label, value }: { label: string; value: string }) {
  return (
    <div className="bg-slate-800 rounded-lg p-4 border border-slate-700 text-center">
      <p className="text-xs text-slate-400 mb-1">{label}</p>
      <p className="text-xl font-bold text-white">{value}</p>
    </div>
  )
}

export default function Benchmark() {
  const { data: maps = [] }    = useQuery<ModelMap[]>({ queryKey: ['model-mappings'], queryFn: modelMappingsApi.list })
  const { data: routers = [] } = useQuery<Router[]>({ queryKey: ['routers'], queryFn: routersApi.list })

  const models = Array.from(new Set(maps.map(m => m.local_model))).sort()

  const [model, setModel]           = useState('')
  const [prompt, setPrompt]         = useState('Say "hello" and nothing else.')
  const [numRequests, setNumRequests] = useState(10)
  const [concurrency, setConcurrency] = useState(1)
  const [running, setRunning]       = useState(false)
  const [progress, setProgress]     = useState(0)
  const [results, setResults]       = useState<RunResult[] | null>(null)
  const abortRef = useRef<AbortController | null>(null)

  async function run() {
    if (!model || running) return
    const controller = new AbortController()
    abortRef.current = controller
    setRunning(true)
    setProgress(0)
    setResults(null)

    const out: RunResult[] = new Array(numRequests)
    let completed = 0
    let qIdx = 0

    const runOne = async (): Promise<RunResult> => {
      const start = Date.now()
      try {
        const res = await proxyChat(
          model,
          [{ role: 'user', content: prompt }],
          false,
          controller.signal,
        )
        const latencyMs = Date.now() - start
        if (!res.ok) return { latencyMs, tokens: 0, error: `HTTP ${res.status}` }
        const data = await res.json()
        return { latencyMs, tokens: data.usage?.total_tokens ?? 0 }
      } catch (e: any) {
        if (e.name === 'AbortError') return { latencyMs: Date.now() - start, tokens: 0, error: 'cancelled' }
        return { latencyMs: Date.now() - start, tokens: 0, error: e.message }
      }
    }

    const worker = async () => {
      while (qIdx < numRequests && !controller.signal.aborted) {
        const myIdx = qIdx++
        if (myIdx >= numRequests) break
        out[myIdx] = await runOne()
        completed++
        setProgress(completed / numRequests)
      }
    }

    await Promise.all(Array.from({ length: concurrency }, worker))
    setResults(out.filter(Boolean))
    setRunning(false)
    abortRef.current = null
  }

  function stop() {
    abortRef.current?.abort()
  }

  const stats = results ? computeStats(results) : null
  const hist  = results ? buildHistogram(results) : []

  return (
    <div>
      <div className="flex items-center gap-3 mb-6">
        <Gauge className="w-6 h-6 text-slate-400" />
        <div>
          <h2 className="text-2xl font-bold">Benchmark</h2>
          <p className="text-slate-400 text-sm mt-0.5">Measure latency and throughput for any model or router</p>
        </div>
      </div>

      {/* ── Config ── */}
      <div className="bg-slate-800 rounded-lg border border-slate-700 p-5 mb-5 space-y-4">
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div>
            <label className="block text-sm text-slate-400 mb-1">Model / Router</label>
            <select
              value={model}
              onChange={e => setModel(e.target.value)}
              className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-sm text-white font-mono focus:outline-none focus:border-blue-500"
            >
              <option value="">— select —</option>
              {models.length > 0 && (
                <optgroup label="Models">
                  {models.map(m => <option key={m} value={m}>{m}</option>)}
                </optgroup>
              )}
              {routers.length > 0 && (
                <optgroup label="Routers">
                  {routers.map(r => <option key={r.name} value={r.name}>{r.name}</option>)}
                </optgroup>
              )}
            </select>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-sm text-slate-400 mb-1">Requests</label>
              <input
                type="number" value={numRequests} min={1} max={200}
                onChange={e => setNumRequests(Number(e.target.value))}
                className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-sm text-white focus:outline-none focus:border-blue-500"
              />
            </div>
            <div>
              <label className="block text-sm text-slate-400 mb-1">
                Concurrency
                <span className="text-slate-500 ml-1">({concurrency})</span>
              </label>
              <input
                type="range" value={concurrency} min={1} max={10}
                onChange={e => setConcurrency(Number(e.target.value))}
                className="w-full mt-2 accent-blue-500"
              />
            </div>
          </div>
        </div>
        <div>
          <label className="block text-sm text-slate-400 mb-1">Prompt</label>
          <textarea
            value={prompt}
            onChange={e => setPrompt(e.target.value)}
            rows={3}
            className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-sm text-slate-100 resize-y focus:outline-none focus:border-blue-500"
          />
        </div>

        <div className="flex items-center gap-3">
          {running ? (
            <>
              <button onClick={stop}
                className="flex items-center gap-2 px-4 py-2 bg-red-600 hover:bg-red-700 rounded-lg text-sm">
                <Square className="w-4 h-4" /> Stop
              </button>
              <div className="flex-1 bg-slate-700 rounded-full h-2">
                <div
                  className="bg-blue-500 h-2 rounded-full transition-all"
                  style={{ width: `${Math.round(progress * 100)}%` }}
                />
              </div>
              <span className="text-sm text-slate-400 tabular-nums w-12 text-right">
                {Math.round(progress * 100)}%
              </span>
            </>
          ) : (
            <button onClick={run} disabled={!model}
              className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:opacity-40 rounded-lg text-sm">
              <Play className="w-4 h-4" /> Run
            </button>
          )}
        </div>
      </div>

      {/* ── Results ── */}
      {stats && (
        <>
          <div className="grid grid-cols-2 sm:grid-cols-4 lg:grid-cols-8 gap-3 mb-5">
            <StatBox label="Requests" value={String(stats.total)} />
            <StatBox label="Errors" value={String(stats.errors)} />
            <StatBox label="Min" value={`${(stats.min / 1000).toFixed(2)}s`} />
            <StatBox label="Avg" value={`${(stats.avg / 1000).toFixed(2)}s`} />
            <StatBox label="p50" value={`${(stats.p50 / 1000).toFixed(2)}s`} />
            <StatBox label="p95" value={`${(stats.p95 / 1000).toFixed(2)}s`} />
            <StatBox label="p99" value={`${(stats.p99 / 1000).toFixed(2)}s`} />
            <StatBox label="tok/s" value={stats.tokensPerSec.toFixed(1)} />
          </div>

          {hist.length > 0 && (
            <div className="bg-slate-800 rounded-lg border border-slate-700 p-5">
              <h3 className="text-sm font-semibold text-slate-300 mb-4">Latency distribution</h3>
              <div className="h-48">
                <ResponsiveContainer width="100%" height="100%">
                  <BarChart data={hist} margin={{ top: 0, right: 0, bottom: 0, left: 0 }}>
                    <CartesianGrid strokeDasharray="3 3" stroke="#1e293b" />
                    <XAxis dataKey="label" tick={{ fill: '#94a3b8', fontSize: 11 }} />
                    <YAxis tick={{ fill: '#94a3b8', fontSize: 11 }} allowDecimals={false} />
                    <Tooltip
                      contentStyle={{ backgroundColor: '#1e293b', border: '1px solid #334155', borderRadius: '6px', fontSize: 12 }}
                      formatter={(v: number) => [v, 'requests']}
                    />
                    <Bar dataKey="count" fill="#3b82f6" name="Requests" radius={[3, 3, 0, 0]} />
                  </BarChart>
                </ResponsiveContainer>
              </div>
            </div>
          )}

          {stats.errors > 0 && (
            <div className="mt-4 p-4 bg-red-900/20 border border-red-700/40 rounded-lg">
              <p className="text-sm text-red-400">
                {stats.errors} of {stats.total} requests failed.
                {results?.filter(r => r.error).slice(0, 3).map((r, i) => (
                  <span key={i} className="block font-mono text-xs mt-1 text-red-300">{r.error}</span>
                ))}
              </p>
            </div>
          )}
        </>
      )}
    </div>
  )
}
