import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { backendsApi } from '../api/client'
import { Backend } from '../types'
import { Plus, Pencil, Trash2, RefreshCw, CheckCircle, XCircle } from 'lucide-react'
import { useState } from 'react'

function StatusBadge({ status }: { status: string }) {
  const colors: Record<string, string> = {
    active: 'bg-green-500/20 text-green-400',
    paused: 'bg-yellow-500/20 text-yellow-400',
    error: 'bg-red-500/20 text-red-400',
  }
  const icons: Record<string, React.ElementType> = {
    active: CheckCircle,
    paused: XCircle,
    error: XCircle,
  }
  const Icon = icons[status] || XCircle
  return (
    <span className={`inline-flex items-center gap-1 px-2 py-1 rounded-full text-xs ${colors[status] || 'bg-slate-500/20 text-slate-400'}`}>
      <Icon className="w-3 h-3" />
      {status}
    </span>
  )
}

function BackendForm({ backend, onSubmit, onCancel }: {
  backend?: Backend | null
  onSubmit: (data: Partial<Backend>) => void
  onCancel: () => void
}) {
  const [name, setName] = useState(backend?.name || '')
  const [baseURL, setBaseURL] = useState(backend?.base_url || '')
  const [provider, setProvider] = useState<Backend['provider']>(backend?.provider || 'openai-compatible')
  const [origin, setOrigin] = useState<Backend['origin']>(backend?.origin || 'local')
  const [apiKey, setApiKey] = useState(backend?.api_key || '')
  const [weight, setWeight] = useState(backend?.weight || 1)

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    onSubmit({ name, base_url: baseURL, provider, origin, api_key: apiKey, weight })
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-4 p-6 bg-slate-800 rounded-lg border border-slate-700">
      <div>
        <label className="block text-sm text-slate-400 mb-1">Name</label>
        <input
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500"
          required
        />
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">Base URL</label>
        <input
          type="url"
          value={baseURL}
          onChange={(e) => setBaseURL(e.target.value)}
          placeholder="https://api.openai.com"
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500"
          required
        />
      </div>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div>
          <label className="block text-sm text-slate-400 mb-1">Provider</label>
          <select
            value={provider}
            onChange={(e) => setProvider(e.target.value as Backend['provider'])}
            className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500"
          >
            <option value="openai-compatible">OpenAI-compatible</option>
            <option value="openai">OpenAI API</option>
          </select>
        </div>
        <div>
          <label className="block text-sm text-slate-400 mb-1">Origin</label>
          <select
            value={origin}
            onChange={(e) => setOrigin(e.target.value as Backend['origin'])}
            className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500"
          >
            <option value="local">Local</option>
            <option value="external">External / paid fallback</option>
          </select>
        </div>
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">API Key</label>
        <input
          type="password"
          value={apiKey}
          onChange={(e) => setApiKey(e.target.value)}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500"
        />
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">Weight</label>
        <input
          type="number"
          value={weight}
          onChange={(e) => setWeight(Number(e.target.value))}
          min={1}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500"
        />
      </div>
      <div className="flex gap-2 justify-end">
        <button type="button" onClick={onCancel} className="px-4 py-2 text-slate-400 hover:text-white">Cancel</button>
        <button type="submit" className="px-4 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg">
          {backend ? 'Update' : 'Create'}
        </button>
      </div>
    </form>
  )
}

function Backends() {
  const queryClient = useQueryClient()
  const [showForm, setShowForm] = useState(false)
  const [editing, setEditing] = useState<Backend | null>(null)

  const { data: backends, isLoading } = useQuery({
    queryKey: ['backends'],
    queryFn: backendsApi.list,
  })

  const createMutation = useMutation({
    mutationFn: backendsApi.create,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['backends'] })
      setShowForm(false)
    },
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: number; data: Partial<Backend> }) => backendsApi.update(id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['backends'] })
      setEditing(null)
    },
  })

  const deleteMutation = useMutation({
    mutationFn: backendsApi.delete,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['backends'] }),
  })

  const healthMutation = useMutation({
    mutationFn: backendsApi.healthCheck,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['backends'] }),
  })

  if (isLoading) {
    return <div className="flex items-center justify-center h-64"><div className="animate-spin w-8 h-8 border-2 border-blue-400 border-t-transparent rounded-full" /></div>
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h2 className="text-2xl font-bold">Backends</h2>
        <button
          onClick={() => setShowForm(true)}
          className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg"
        >
          <Plus className="w-4 h-4" />
          Add Backend
        </button>
      </div>

      {showForm && (
        <div className="mb-6">
          <BackendForm
            onSubmit={(data) => createMutation.mutate(data)}
            onCancel={() => setShowForm(false)}
          />
        </div>
      )}

      {editing && (
        <div className="mb-6">
          <BackendForm
            backend={editing}
            onSubmit={(data) => updateMutation.mutate({ id: editing.id, data })}
            onCancel={() => setEditing(null)}
          />
        </div>
      )}

      <div className="bg-slate-800 rounded-lg border border-slate-700 overflow-hidden">
        <table className="w-full">
          <thead>
            <tr className="border-b border-slate-700">
              <th className="text-left px-6 py-3 text-sm text-slate-400">Name</th>
              <th className="text-left px-6 py-3 text-sm text-slate-400">URL</th>
              <th className="text-left px-6 py-3 text-sm text-slate-400">Provider</th>
              <th className="text-left px-6 py-3 text-sm text-slate-400">Origin</th>
              <th className="text-left px-6 py-3 text-sm text-slate-400">Status</th>
              <th className="text-left px-6 py-3 text-sm text-slate-400">Latency</th>
              <th className="text-left px-6 py-3 text-sm text-slate-400">Weight</th>
              <th className="text-right px-6 py-3 text-sm text-slate-400">Actions</th>
            </tr>
          </thead>
          <tbody>
            {backends?.map((b) => (
              <tr key={b.id} className="border-b border-slate-700/50 hover:bg-slate-700/30">
                <td className="px-6 py-4 font-medium">{b.name}</td>
                <td className="px-6 py-4 text-slate-400 text-sm">{b.base_url}</td>
                <td className="px-6 py-4 text-slate-400 text-sm">{b.provider}</td>
                <td className="px-6 py-4 text-slate-400 text-sm">{b.origin}</td>
                <td className="px-6 py-4"><StatusBadge status={b.status} /></td>
                <td className="px-6 py-4 text-slate-400">{b.last_latency_ms}ms</td>
                <td className="px-6 py-4 text-slate-400">{b.weight}</td>
                <td className="px-6 py-4">
                  <div className="flex items-center justify-end gap-2">
                    <button
                      onClick={() => healthMutation.mutate(b.id)}
                      className="p-1 text-slate-400 hover:text-white"
                      title="Health check"
                    >
                      <RefreshCw className={`w-4 h-4 ${healthMutation.isPending ? 'animate-spin' : ''}`} />
                    </button>
                    <button
                      onClick={() => setEditing(b)}
                      className="p-1 text-slate-400 hover:text-white"
                    >
                      <Pencil className="w-4 h-4" />
                    </button>
                    <button
                      onClick={() => deleteMutation.mutate(b.id)}
                      className="p-1 text-slate-400 hover:text-red-400"
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </div>
                </td>
              </tr>
            ))}
            {(!backends || backends.length === 0) && (
              <tr>
                <td colSpan={8} className="px-6 py-8 text-center text-slate-500">
                  No backends configured. Add one to get started.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}

export default Backends
