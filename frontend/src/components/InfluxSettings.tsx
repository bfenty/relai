import { useEffect, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Database, CheckCircle2, XCircle } from 'lucide-react'
import { settingsApi } from '../api/client'
import type { InfluxSettings as Settings } from '../types'

const input =
  'w-full px-3 py-2 bg-slate-900 border border-slate-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500'

export default function InfluxSettings() {
  const qc = useQueryClient()
  const { data } = useQuery({ queryKey: ['influx-settings'], queryFn: settingsApi.influx })

  const [form, setForm] = useState<Settings | null>(null)
  const [token, setToken] = useState('')
  const [result, setResult] = useState<{ ok: boolean; msg: string } | null>(null)

  useEffect(() => {
    if (data && !form) setForm(data)
  }, [data, form])

  const save = useMutation({
    mutationFn: () => settingsApi.updateInflux({ ...(form as Settings), token: token || undefined }),
    onSuccess: saved => {
      setForm(saved)
      setToken('')
      setResult({ ok: true, msg: 'Saved.' })
      qc.invalidateQueries({ queryKey: ['influx-settings'] })
    },
    onError: (e: Error) => setResult({ ok: false, msg: e.message }),
  })

  const test = useMutation({
    mutationFn: () => settingsApi.testInflux({ ...(form as Settings), token: token || undefined }),
    onSuccess: r => setResult({ ok: true, msg: r.message }),
    onError: (e: Error) => setResult({ ok: false, msg: e.message }),
  })

  if (!form) return null
  const set = <K extends keyof Settings>(k: K, v: Settings[K]) =>
    setForm(prev => (prev ? { ...prev, [k]: v } : prev))

  return (
    <div className="bg-slate-800/50 rounded-lg border border-slate-700 p-6 space-y-4">
      <div>
        <h3 className="text-lg font-semibold flex items-center gap-2">
          <Database className="w-5 h-5 text-cyan-400" /> Metrics export (InfluxDB)
        </h3>
        <p className="text-sm text-slate-400 mt-1">
          Periodically writes routing health — cache reuse, cold arrivals, reprocessed tokens and
          compression savings — to an InfluxDB v2 bucket, so the history outlives the request log
          and can sit alongside your other host metrics.
        </p>
      </div>

      <label className="flex items-center gap-3 cursor-pointer">
        <button type="button" onClick={() => set('enabled', !form.enabled)}
          className={`relative w-10 h-5 rounded-full transition-colors ${form.enabled ? 'bg-blue-600' : 'bg-slate-600'}`}>
          <span className={`absolute top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform ${form.enabled ? 'translate-x-5' : 'translate-x-0.5'}`} />
        </button>
        <span className="text-sm text-slate-300">Enable export</span>
      </label>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div>
          <label className="block text-sm text-slate-400 mb-1">URL</label>
          <input className={input} value={form.url} placeholder="http://127.0.0.1:8086"
            onChange={e => set('url', e.target.value)} />
        </div>
        <div>
          <label className="block text-sm text-slate-400 mb-1">Bucket</label>
          <input className={input} value={form.bucket} placeholder="relai"
            onChange={e => set('bucket', e.target.value)} />
        </div>
        <div>
          <label className="block text-sm text-slate-400 mb-1">
            Org <span className="text-slate-500">(v2; leave blank if unused)</span>
          </label>
          <input className={input} value={form.org} onChange={e => set('org', e.target.value)} />
        </div>
        <div>
          <label className="block text-sm text-slate-400 mb-1">
            Write interval <span className="text-slate-500">(seconds, 10–3600)</span>
          </label>
          <input className={input} type="number" min={10} max={3600}
            value={form.interval_seconds}
            onChange={e => set('interval_seconds', Number(e.target.value))} />
        </div>
        <div className="md:col-span-2">
          <label className="block text-sm text-slate-400 mb-1">
            Token{' '}
            <span className="text-slate-500">
              {form.has_token ? '(stored — leave blank to keep)' : '(required to enable)'}
            </span>
          </label>
          <input className={input} type="password" value={token}
            placeholder={form.has_token ? '••••••••' : 'InfluxDB API token'}
            onChange={e => setToken(e.target.value)} />
        </div>
      </div>

      {result && (
        <div className={`flex items-start gap-2 text-sm rounded-md p-3 border ${
          result.ok
            ? 'bg-green-500/10 border-green-500/30 text-green-300'
            : 'bg-red-500/10 border-red-500/30 text-red-300'
        }`}>
          {result.ok ? <CheckCircle2 className="w-4 h-4 mt-0.5 shrink-0" />
                     : <XCircle className="w-4 h-4 mt-0.5 shrink-0" />}
          <span>{result.msg}</span>
        </div>
      )}

      <div className="flex gap-2">
        <button onClick={() => { setResult(null); save.mutate() }} disabled={save.isPending}
          className="px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 rounded-lg text-white text-sm">
          {save.isPending ? 'Saving…' : 'Save'}
        </button>
        <button onClick={() => { setResult(null); test.mutate() }} disabled={test.isPending}
          className="px-4 py-2 bg-slate-700 hover:bg-slate-600 disabled:opacity-50 rounded-lg text-white text-sm">
          {test.isPending ? 'Testing…' : 'Write test point'}
        </button>
      </div>

      <p className="text-xs text-slate-500">
        Measurements: <code className="text-slate-400">relai_routing</code> (cluster-wide) and{' '}
        <code className="text-slate-400">relai_routing_host</code> (tagged by host). Each write
        covers the window since the previous one, so the interval also sets the series resolution.
      </p>
    </div>
  )
}
