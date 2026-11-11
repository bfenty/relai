import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiKeysApi, backendsApi, usersApi } from '../api/client'
import { APIKey, Backend, User } from '../types'
import { Plus, Pencil, Trash2, Copy, Key, ToggleLeft, ToggleRight, Sparkles } from 'lucide-react'
import { useState } from 'react'
import { Modal } from '../components/Modal'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { EmptyState } from '../components/EmptyState'
import { TagInput } from '../components/TagInput'
import { useToast } from '../components/Toast'

const SCOPE_OPTIONS = [
  { value: 'inference', label: 'Inference' },
  { value: 'models:read', label: 'Models' },
  { value: 'embeddings', label: 'Embeddings' },
  { value: 'rerank', label: 'Rerank' },
]

const defaultScopes = SCOPE_OPTIONS.map(s => s.value)

function toDateTimeLocal(value?: string) {
  if (!value) return ''
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return ''
  return new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16)
}

function fromDateTimeLocal(value: string) {
  return value ? new Date(value).toISOString() : undefined
}

// ── Key form ─────────────────────────────────────────────────────────────────

function KeyForm({ initial, backends, users, onSubmit, onClose }: {
  initial?: APIKey
  backends: Backend[]
  users: User[]
  onSubmit: (data: Partial<APIKey>) => void
  onClose: () => void
}) {
  const [name, setName]           = useState(initial?.name ?? '')
  const [owner, setOwner]         = useState(initial?.owner ?? '')
  const [scopes, setScopes]       = useState<string[]>(initial?.scopes?.length ? initial.scopes : defaultScopes)
  const [expiresAt, setExpiresAt] = useState(toDateTimeLocal(initial?.expires_at))
  const [rateLimit, setRateLimit] = useState(initial?.rate_limit_rpm ?? 60)
  const [priority, setPriority]   = useState(initial?.priority ?? 0)
  const [allowedModels, setAllowedModels] = useState<string[]>(initial?.allowed_models ?? [])
  const [allowedBackends, setAllowedBackends] = useState<number[]>(initial?.allowed_backends ?? [])
  const [userId, setUserId]       = useState<number | ''>(initial?.user_id ?? '')

  const toggleBackend = (id: number) => {
    setAllowedBackends(prev =>
      prev.includes(id) ? prev.filter(x => x !== id) : [...prev, id]
    )
  }

  const toggleScope = (scope: string) => {
    setScopes(prev =>
      prev.includes(scope) ? prev.filter(x => x !== scope) : [...prev, scope]
    )
  }

  return (
    <form onSubmit={e => {
      e.preventDefault()
      onSubmit({
        name,
        owner,
        scopes,
        expires_at: fromDateTimeLocal(expiresAt),
        rate_limit_rpm: rateLimit,
        priority,
        allowed_models: allowedModels,
        allowed_backends: allowedBackends,
        user_id: userId === '' ? undefined : userId,
      })
    }} className="space-y-4">
      <div>
        <label className="block text-sm text-slate-400 mb-1">Name</label>
        <input value={name} onChange={e => setName(e.target.value)} required
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">Owner <span className="text-slate-500">(optional label)</span></label>
        <input value={owner} onChange={e => setOwner(e.target.value)}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
      </div>
      {users.length > 0 && (
        <div>
          <label className="block text-sm text-slate-400 mb-1">User <span className="text-slate-500">(optional)</span></label>
          <select value={userId} onChange={e => setUserId(e.target.value === '' ? '' : Number(e.target.value))}
            className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500">
            <option value="">— no user —</option>
            {users.map(u => (
              <option key={u.id} value={u.id}>{u.name} ({u.email})</option>
            ))}
          </select>
        </div>
      )}
      <div>
        <label className="block text-sm text-slate-400 mb-1">Rate limit <span className="text-slate-500">(requests/min)</span></label>
        <input type="number" value={rateLimit} onChange={e => setRateLimit(Number(e.target.value))} min={1}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
      </div>
      <div>
        <label className="flex items-center gap-2 text-sm text-slate-300 cursor-pointer">
          <input type="checkbox" checked={priority > 0}
            onChange={e => setPriority(e.target.checked ? 1 : 0)}
            className="rounded border-slate-500 bg-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-slate-800" />
          Interactive <span className="text-slate-500">(skip the queue behind long prompts on supporting backends)</span>
        </label>
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">Expires <span className="text-slate-500">(optional)</span></label>
        <input type="datetime-local" value={expiresAt} onChange={e => setExpiresAt(e.target.value)}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">Scopes</label>
        <div className="grid grid-cols-2 gap-2">
          {SCOPE_OPTIONS.map(scope => (
            <label key={scope.value} className="flex items-center gap-2 text-sm text-slate-300">
              <input
                type="checkbox"
                checked={scopes.includes(scope.value)}
                onChange={() => toggleScope(scope.value)}
                className="rounded border-slate-500 bg-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-slate-800"
              />
              {scope.label}
            </label>
          ))}
        </div>
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">
          Allowed models <span className="text-slate-500">(empty = all models allowed)</span>
        </label>
        <TagInput values={allowedModels} onChange={setAllowedModels} placeholder="gpt-4o  (press Enter)" />
      </div>
      {backends.length > 0 && (
        <div>
          <label className="block text-sm text-slate-400 mb-1">
            Allowed hosts <span className="text-slate-500">(none checked = all hosts allowed)</span>
          </label>
          <div className="space-y-1 p-3 bg-slate-700 border border-slate-600 rounded-lg max-h-40 overflow-y-auto">
            {backends.map(b => (
              <label key={b.id} className="flex items-center gap-2 text-sm cursor-pointer hover:text-white text-slate-300">
                <input
                  type="checkbox"
                  checked={allowedBackends.includes(b.id)}
                  onChange={() => toggleBackend(b.id)}
                  className="rounded border-slate-500 bg-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-slate-700"
                />
                {b.name}
                <span className="text-slate-500 text-xs">{b.base_url}</span>
              </label>
            ))}
          </div>
        </div>
      )}
      <div className="flex gap-2 justify-end pt-1">
        <button type="button" onClick={onClose} className="px-4 py-2 text-slate-400 hover:text-white text-sm">Cancel</button>
        <button type="submit" className="px-4 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg text-sm">
          {initial ? 'Save changes' : 'Create key'}
        </button>
      </div>
    </form>
  )
}

// ── Page ──────────────────────────────────────────────────────────────────────

export default function APIKeys() {
  const { toast } = useToast()
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate]     = useState(false)
  const [editing, setEditing]           = useState<APIKey | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<APIKey | null>(null)
  const [newKeyValue, setNewKeyValue]   = useState<string | null>(null)

  const { data: keys = [], isLoading } = useQuery({
    queryKey: ['api-keys'],
    queryFn: apiKeysApi.list,
  })

  const { data: backends = [] } = useQuery({
    queryKey: ['backends'],
    queryFn: backendsApi.list,
  })

  const { data: users = [] } = useQuery({
    queryKey: ['users'],
    queryFn: usersApi.list,
  })

  const createMutation = useMutation({
    mutationFn: apiKeysApi.create,
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ['api-keys'] })
      setNewKeyValue(data.value)
      setShowCreate(false)
    },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: number; data: Partial<APIKey> }) => apiKeysApi.update(id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['api-keys'] })
      toast('Key updated', 'success')
      setEditing(null)
    },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  const deleteMutation = useMutation({
    mutationFn: apiKeysApi.delete,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['api-keys'] })
      toast('Key deleted', 'success')
      setDeleteTarget(null)
    },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  const copy = (text: string) => {
    navigator.clipboard.writeText(text)
    toast('Copied to clipboard', 'success')
  }

  const createTestKeyMutation = useMutation({
    mutationFn: apiKeysApi.createTestKey,
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ['api-keys'] })
      setNewKeyValue(data.value)
      toast('Test key created (expires in 2 hours)', 'success')
    },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  const cleanupMutation = useMutation({
    mutationFn: apiKeysApi.cleanupTestKeys,
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ['api-keys'] })
      if (data.deleted > 0) {
        toast(`Cleaned up ${data.deleted} expired test keys`, 'success')
      }
    },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-3 mb-6">
        <div>
          <h2 className="text-2xl font-bold">API Keys</h2>
          <p className="text-slate-400 text-sm mt-1">Keys that clients use to authenticate with Relai.</p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => createTestKeyMutation.mutate()}
            disabled={createTestKeyMutation.isPending}
            className="flex items-center gap-2 px-4 py-2 bg-amber-600 hover:bg-amber-700 disabled:bg-amber-800 disabled:opacity-50 rounded-lg text-sm"
          >
            <Sparkles className="w-4 h-4" /> Create test key
          </button>
          <button
            onClick={() => cleanupMutation.mutate()}
            disabled={cleanupMutation.isPending}
            className="flex items-center gap-2 px-4 py-2 bg-slate-600 hover:bg-slate-700 disabled:bg-slate-700 disabled:opacity-50 rounded-lg text-sm"
          >
            <Trash2 className="w-4 h-4" /> Cleanup expired
          </button>
          <button
            onClick={() => { setShowCreate(true); setNewKeyValue(null) }}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg text-sm"
          >
            <Plus className="w-4 h-4" /> Create key
          </button>
        </div>
      </div>

      {newKeyValue && (
        <div className="mb-6 p-4 bg-green-900/30 border border-green-700/50 rounded-lg">
          <p className="text-sm text-green-400 mb-2 font-medium">Key created — save it now, it won't be shown again.</p>
          <div className="flex items-center gap-2">
            <code className="flex-1 px-3 py-2 bg-slate-900 rounded text-sm font-mono text-green-300">{newKeyValue}</code>
            <button onClick={() => copy(newKeyValue)} className="p-2 text-slate-400 hover:text-white" title="Copy">
              <Copy className="w-4 h-4" />
            </button>
          </div>
        </div>
      )}

      <div className="bg-slate-800 rounded-lg border border-slate-700 overflow-hidden">
        {isLoading ? (
          <div className="flex items-center justify-center h-48">
            <div className="animate-spin w-7 h-7 border-2 border-blue-400 border-t-transparent rounded-full" />
          </div>
        ) : keys.length === 0 ? (
          <EmptyState icon={Key} message="No API keys yet" hint="Create a key to let clients use the proxy." />
        ) : (
          <div className="overflow-x-auto">
          <table className="w-full min-w-[960px]">
            <thead>
              <tr className="border-b border-slate-700">
                <th className="text-left px-6 py-3 text-sm text-slate-400">Name</th>
                <th className="text-left px-6 py-3 text-sm text-slate-400">User</th>
                <th className="text-left px-6 py-3 text-sm text-slate-400">Prefix</th>
                <th className="text-left px-6 py-3 text-sm text-slate-400">Rate limit</th>
                <th className="text-left px-6 py-3 text-sm text-slate-400">Scopes</th>
                <th className="text-left px-6 py-3 text-sm text-slate-400">Expires</th>
                <th className="text-left px-6 py-3 text-sm text-slate-400">Requests</th>
                <th className="text-left px-6 py-3 text-sm text-slate-400">Cost</th>
                <th className="text-left px-6 py-3 text-sm text-slate-400">Last used</th>
                <th className="text-left px-6 py-3 text-sm text-slate-400">Status</th>
                <th className="text-right px-6 py-3 text-sm text-slate-400">Actions</th>
              </tr>
            </thead>
            <tbody>
              {keys.map(k => (
                <tr key={k.id} className="border-b border-slate-700/50 hover:bg-slate-700/20">
                  <td className="px-6 py-4 font-medium">
                    {k.name}
                    {k.is_test && (
                      <span className="ml-2 px-1.5 py-0.5 bg-amber-500/20 text-amber-400 rounded text-xs font-medium">Test</span>
                    )}
                    {(k.priority ?? 0) > 0 && (
                      <span className="ml-2 px-1.5 py-0.5 bg-blue-500/20 text-blue-400 rounded text-xs font-medium">Interactive</span>
                    )}
                  </td>
                  <td className="px-6 py-4 text-slate-400 text-sm">
                    {k.user_id ? (users.find(u => u.id === k.user_id)?.name ?? `#${k.user_id}`) : <span className="text-slate-600">—</span>}
                  </td>
                  <td className="px-6 py-4 text-slate-400 font-mono text-sm">{k.key_prefix}…</td>
                  <td className="px-6 py-4 text-sm">
                    <div className="flex flex-col gap-1">
                      <span className="text-slate-400">{k.rate_limit_rpm}/min</span>
                      <div className="w-20 h-1 bg-slate-700 rounded-full overflow-hidden">
                        <div
                          className="h-full bg-blue-500 rounded-full"
                          style={{ width: `${Math.min(100, (k.rate_limit_remaining / k.rate_limit_rpm) * 100)}%` }}
                        />
                      </div>
                    </div>
                  </td>
                  <td className="px-6 py-4 text-slate-400 text-xs">
                    <div className="flex flex-wrap gap-1 max-w-44">
                      {(k.scopes ?? []).map(scope => (
                        <span key={scope} className="px-1.5 py-0.5 bg-slate-700 rounded">{scope}</span>
                      ))}
                    </div>
                  </td>
                  <td className="px-6 py-4 text-slate-500 text-sm">
                    {k.expires_at ? new Date(k.expires_at).toLocaleDateString() : '—'}
                  </td>
                  <td className="px-6 py-4 text-slate-400 text-sm">{k.total_requests.toLocaleString()}</td>
                  <td className="px-6 py-4 text-slate-400 text-sm">${k.total_cost.toFixed(4)}</td>
                  <td className="px-6 py-4 text-slate-500 text-sm">
                    {k.last_used_at ? new Date(k.last_used_at).toLocaleDateString() : '—'}
                  </td>
                  <td className="px-6 py-4">
                    <span className={`inline-flex px-2 py-1 rounded-full text-xs ${
                      !k.is_active ? 'bg-slate-500/20 text-slate-400' :
                      k.expires_at && new Date(k.expires_at) <= new Date() ? 'bg-red-500/20 text-red-400' :
                      'bg-green-500/20 text-green-400'
                    }`}>
                      {!k.is_active ? 'Inactive' : k.expires_at && new Date(k.expires_at) <= new Date() ? 'Expired' : 'Active'}
                    </span>
                  </td>
                  <td className="px-6 py-4">
                    <div className="flex items-center justify-end gap-1">
                      <button
                        onClick={() => updateMutation.mutate({ id: k.id, data: { is_active: !k.is_active } })}
                        title={k.is_active ? 'Deactivate' : 'Activate'}
                        className="p-1.5 text-slate-400 hover:text-yellow-400 rounded"
                      >
                        {k.is_active
                          ? <ToggleRight className="w-4 h-4 text-green-400" />
                          : <ToggleLeft className="w-4 h-4" />}
                      </button>
                      <button onClick={() => setEditing(k)} className="p-1.5 text-slate-400 hover:text-white rounded">
                        <Pencil className="w-4 h-4" />
                      </button>
                      <button onClick={() => setDeleteTarget(k)} className="p-1.5 text-slate-400 hover:text-red-400 rounded">
                        <Trash2 className="w-4 h-4" />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          </div>
        )}
      </div>

      {showCreate && (
        <Modal title="Create API key" onClose={() => setShowCreate(false)}>
          <KeyForm backends={backends} users={users} onSubmit={createMutation.mutate} onClose={() => setShowCreate(false)} />
        </Modal>
      )}

      {editing && (
        <Modal title="Edit API key" onClose={() => setEditing(null)}>
          <KeyForm
            initial={editing}
            backends={backends}
            users={users}
            onSubmit={data => updateMutation.mutate({ id: editing.id, data })}
            onClose={() => setEditing(null)}
          />
        </Modal>
      )}

      {deleteTarget && (
        <ConfirmDialog
          title="Delete API key"
          message={`Delete "${deleteTarget.name}"? This cannot be undone.`}
          onConfirm={() => deleteMutation.mutate(deleteTarget.id)}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
    </div>
  )
}
