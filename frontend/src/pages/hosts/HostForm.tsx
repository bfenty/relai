import { Backend } from '../../types'
import { useState } from 'react'
import { HOST_PALETTE, backendColor } from '../../utils/colors'

// ── Host form (create / edit) ─────────────────────────────────────────────────

interface HostFormData {
  name: string
  base_url: string
  api_key: string
  provider: Backend['provider']
  origin: Backend['origin']
  weight: number
  max_concurrent: number
  color: string
}

export function HostForm({ initial, defaultColor, onSubmit, onClose }: {
  initial?: Backend
  defaultColor?: string
  onSubmit: (data: HostFormData) => void
  onClose: () => void
}) {
  const [name, setName]           = useState(initial?.name           ?? '')
  const [url, setUrl]             = useState(initial?.base_url       ?? '')
  const [apiKey, setApiKey]       = useState(initial?.api_key        ?? '')
  const [provider, setProvider]   = useState<Backend['provider']>(initial?.provider ?? 'openai-compatible')
  const [origin, setOrigin]       = useState<Backend['origin']>(initial?.origin ?? 'local')
  const [weight, setWeight]       = useState(initial?.weight         ?? 1)
  const [maxConcurrent, setMaxConcurrent] = useState(initial?.max_concurrent ?? 1)
  const [color, setColor]         = useState(initial ? backendColor(initial) : (defaultColor ?? HOST_PALETTE[0]))

  return (
    <form onSubmit={e => { e.preventDefault(); onSubmit({ name, base_url: url, api_key: apiKey, provider, origin, weight, max_concurrent: maxConcurrent, color }) }}
      className="space-y-4">
      <div>
        <label className="block text-sm text-slate-400 mb-1">Name</label>
        <input value={name} onChange={e => setName(e.target.value)} required
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-accent-500" />
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">Base URL</label>
        <input type="url" value={url} onChange={e => setUrl(e.target.value)} required
          placeholder="https://api.openai.com"
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-accent-500" />
      </div>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div>
          <label className="block text-sm text-slate-400 mb-1">Provider</label>
          <select value={provider} onChange={e => setProvider(e.target.value as Backend['provider'])}
            className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-accent-500">
            <option value="openai-compatible">OpenAI-compatible</option>
            <option value="openai">OpenAI API</option>
          </select>
        </div>
        <div>
          <label className="block text-sm text-slate-400 mb-1">Origin</label>
          <select value={origin} onChange={e => setOrigin(e.target.value as Backend['origin'])}
            className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-accent-500">
            <option value="local">Local</option>
            <option value="external">External / paid fallback</option>
          </select>
        </div>
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">API Key</label>
        <input type="password" value={apiKey} onChange={e => setApiKey(e.target.value)}
          placeholder={initial?.has_api_key ? '(unchanged — leave blank to keep)' : 'sk-…'}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-accent-500" />
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">Weight <span className="text-slate-500">(for weighted routing)</span></label>
        <input type="number" value={weight} onChange={e => setWeight(Number(e.target.value))} min={1}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-accent-500" />
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">Max concurrent requests <span className="text-slate-500">(per host, across all its models)</span></label>
        <input type="number" value={maxConcurrent} onChange={e => setMaxConcurrent(Number(e.target.value))} min={1}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-accent-500" />
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-2">Color</label>
        <div className="flex gap-2 flex-wrap">
          {HOST_PALETTE.map(c => (
            <button
              key={c}
              type="button"
              onClick={() => setColor(c)}
              title={c}
              className={`w-6 h-6 rounded-full transition-transform ${color === c ? 'ring-2 ring-white ring-offset-2 ring-offset-slate-800 scale-110' : 'opacity-70 hover:opacity-100'}`}
              style={{ backgroundColor: c }}
            />
          ))}
        </div>
      </div>
      <div className="flex gap-2 justify-end pt-1">
        <button type="button" onClick={onClose} className="px-4 py-2 text-slate-400 hover:text-white text-sm">Cancel</button>
        <button type="submit" className="px-4 py-2 bg-accent-500 hover:bg-accent-400 text-slate-950 font-medium rounded-lg text-sm">
          {initial ? 'Save changes' : 'Add host'}
        </button>
      </div>
    </form>
  )
}
