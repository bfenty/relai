import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { usersApi, backendsApi } from '../api/client'
import { UserWithStats, APIKey, Backend } from '../types'
import {
  Plus, Pencil, Trash2, ChevronRight, ChevronDown, Users as UsersIcon,
  Key, Copy,
} from 'lucide-react'
import { useState } from 'react'
import APIKeys from './APIKeys'
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

function fromDateTimeLocal(value: string) {
  return value ? new Date(value).toISOString() : undefined
}

// ── User form ─────────────────────────────────────────────────────────────────

function UserForm({ initial, onSubmit, onClose }: {
  initial?: UserWithStats
  onSubmit: (d: { name: string; email: string; is_active: boolean }) => void
  onClose: () => void
}) {
  const [name, setName]         = useState(initial?.name ?? '')
  const [email, setEmail]       = useState(initial?.email ?? '')
  const [isActive, setIsActive] = useState(initial?.is_active ?? true)

  return (
    <form onSubmit={e => { e.preventDefault(); onSubmit({ name, email, is_active: isActive }) }}
      className="space-y-4">
      <div>
        <label className="block text-sm text-slate-400 mb-1">Name</label>
        <input value={name} onChange={e => setName(e.target.value)} required
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">Email</label>
        <input type="email" value={email} onChange={e => setEmail(e.target.value)} required
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
      </div>
      <label className="flex items-center gap-2 cursor-pointer select-none">
        <input type="checkbox" checked={isActive} onChange={e => setIsActive(e.target.checked)}
          className="rounded border-slate-500 bg-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-slate-800" />
        <span className="text-sm text-slate-300">Active</span>
      </label>
      <div className="flex gap-2 justify-end pt-1">
        <button type="button" onClick={onClose} className="px-4 py-2 text-slate-400 hover:text-white text-sm">Cancel</button>
        <button type="submit" className="px-4 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg text-sm">
          {initial ? 'Save changes' : 'Create user'}
        </button>
      </div>
    </form>
  )
}

// ── Key form (inline, for creating a key under a user) ────────────────────────

function UserKeyForm({ backends, onSubmit, onClose }: {
  backends: Backend[]
  onSubmit: (d: Partial<APIKey>) => void
  onClose: () => void
}) {
  const [name, setName]           = useState('')
  const [rateLimit, setRateLimit] = useState(60)
  const [scopes, setScopes]       = useState<string[]>(defaultScopes)
  const [expiresAt, setExpiresAt] = useState('')
  const [allowedModels, setAllowedModels] = useState<string[]>([])
  const [allowedBackends, setAllowedBackends] = useState<number[]>([])

  const toggleScope = (scope: string) => {
    setScopes(prev =>
      prev.includes(scope) ? prev.filter(x => x !== scope) : [...prev, scope]
    )
  }

  return (
    <form onSubmit={e => {
      e.preventDefault()
      onSubmit({ name, rate_limit_rpm: rateLimit, scopes, expires_at: fromDateTimeLocal(expiresAt), allowed_models: allowedModels, allowed_backends: allowedBackends })
    }} className="space-y-3 mt-3 p-4 bg-slate-700/40 rounded-lg border border-slate-600">
      <p className="text-xs font-medium text-slate-400 uppercase tracking-wider">New API key</p>
      <div>
        <label className="block text-xs text-slate-400 mb-1">Name</label>
        <input value={name} onChange={e => setName(e.target.value)} required
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white text-sm focus:outline-none focus:border-blue-500" />
      </div>
      <div>
        <label className="block text-xs text-slate-400 mb-1">Rate limit <span className="text-slate-500">(req/min)</span></label>
        <input type="number" value={rateLimit} onChange={e => setRateLimit(Number(e.target.value))} min={1}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white text-sm focus:outline-none focus:border-blue-500" />
      </div>
      <div>
        <label className="block text-xs text-slate-400 mb-1">Expires <span className="text-slate-500">(optional)</span></label>
        <input type="datetime-local" value={expiresAt} onChange={e => setExpiresAt(e.target.value)}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white text-sm focus:outline-none focus:border-blue-500" />
      </div>
      <div>
        <label className="block text-xs text-slate-400 mb-1">Scopes</label>
        <div className="flex flex-wrap gap-2">
          {SCOPE_OPTIONS.map(scope => (
            <label key={scope.value} className="flex items-center gap-1.5 text-xs text-slate-300 cursor-pointer">
              <input type="checkbox"
                checked={scopes.includes(scope.value)}
                onChange={() => toggleScope(scope.value)}
                className="rounded border-slate-500 bg-slate-600 text-blue-500" />
              {scope.label}
            </label>
          ))}
        </div>
      </div>
      <div>
        <label className="block text-xs text-slate-400 mb-1">Allowed models <span className="text-slate-500">(empty = all)</span></label>
        <TagInput values={allowedModels} onChange={setAllowedModels} placeholder="model-name  (press Enter)" />
      </div>
      {backends.length > 0 && (
        <div>
          <label className="block text-xs text-slate-400 mb-1">Allowed hosts <span className="text-slate-500">(none = all)</span></label>
          <div className="flex flex-wrap gap-2">
            {backends.map(b => (
              <label key={b.id} className="flex items-center gap-1.5 text-xs text-slate-300 cursor-pointer">
                <input type="checkbox"
                  checked={allowedBackends.includes(b.id)}
                  onChange={() => setAllowedBackends(prev =>
                    prev.includes(b.id) ? prev.filter(x => x !== b.id) : [...prev, b.id])}
                  className="rounded border-slate-500 bg-slate-600 text-blue-500" />
                {b.name}
              </label>
            ))}
          </div>
        </div>
      )}
      <div className="flex gap-2 justify-end pt-1">
        <button type="button" onClick={onClose} className="px-3 py-1.5 text-slate-400 hover:text-white text-sm">Cancel</button>
        <button type="submit" className="px-3 py-1.5 bg-blue-600 hover:bg-blue-700 rounded-lg text-sm">Create key</button>
      </div>
    </form>
  )
}

// ── Key value banner ──────────────────────────────────────────────────────────

function NewKeyBanner({ value, onDismiss }: { value: string; onDismiss: () => void }) {
  const { toast } = useToast()
  const copy = () => { navigator.clipboard.writeText(value); toast('Copied', 'success') }
  return (
    <div className="mt-3 p-3 bg-green-900/30 border border-green-700/50 rounded-lg">
      <p className="text-xs text-green-400 mb-1.5 font-medium">Key created — save it now, it won't be shown again.</p>
      <div className="flex items-center gap-2">
        <code className="flex-1 px-2 py-1 bg-slate-900 rounded text-xs font-mono text-green-300 truncate">{value}</code>
        <button onClick={copy} className="p-1.5 text-slate-400 hover:text-white" title="Copy"><Copy className="w-3.5 h-3.5" /></button>
        <button onClick={onDismiss} className="text-xs text-slate-500 hover:text-white px-1">Dismiss</button>
      </div>
    </div>
  )
}

// ── User keys panel (expanded) ────────────────────────────────────────────────

function UserKeys({ user }: { user: UserWithStats }) {
  const { toast } = useToast()
  const queryClient = useQueryClient()
  const [showAdd, setShowAdd]     = useState(false)
  const [newKeyVal, setNewKeyVal] = useState<string | null>(null)

  const { data: keys = [] } = useQuery<APIKey[]>({
    queryKey: ['user-keys', user.id],
    queryFn: () => usersApi.getKeys(user.id),
  })

  const { data: backends = [] } = useQuery<Backend[]>({
    queryKey: ['backends'],
    queryFn: backendsApi.list,
  })

  const createKey = useMutation({
    mutationFn: (data: Partial<APIKey>) => usersApi.createKey(user.id, data),
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: ['user-keys', user.id] })
      queryClient.invalidateQueries({ queryKey: ['users'] })
      queryClient.invalidateQueries({ queryKey: ['api-keys'] })
      setNewKeyVal(res.value)
      setShowAdd(false)
      toast('Key created', 'success')
    },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  return (
    <div className="px-6 pb-4">
      <div className="ml-6 border-l-2 border-slate-700 pl-4">
        <div className="flex items-center justify-between mb-2">
          <span className="text-xs font-medium text-slate-400 uppercase tracking-wider">API Keys</span>
          <button onClick={() => { setShowAdd(true); setNewKeyVal(null) }}
            className="flex items-center gap-1 px-2 py-1 text-xs bg-slate-700 hover:bg-slate-600 rounded text-slate-300">
            <Plus className="w-3 h-3" /> Add key
          </button>
        </div>

        {keys.length === 0 && !showAdd && (
          <p className="text-sm text-slate-500 py-2">No keys yet.</p>
        )}

        {keys.length > 0 && (
          <table className="w-full text-sm mb-2">
            <thead>
              <tr>
                <th className="text-left py-1.5 pr-4 text-slate-500 text-xs font-medium">Name</th>
                <th className="text-left py-1.5 pr-4 text-slate-500 text-xs font-medium">Prefix</th>
                <th className="text-left py-1.5 pr-4 text-slate-500 text-xs font-medium">Requests</th>
                <th className="text-left py-1.5 pr-4 text-slate-500 text-xs font-medium">Cost</th>
                <th className="text-left py-1.5 pr-4 text-slate-500 text-xs font-medium">Last used</th>
                <th className="text-left py-1.5 text-slate-500 text-xs font-medium">Status</th>
              </tr>
            </thead>
            <tbody>
              {keys.map(k => (
                <tr key={k.id} className="border-t border-slate-700/40">
                  <td className="py-2 pr-4 font-medium text-slate-200">{k.name}</td>
                  <td className="py-2 pr-4 font-mono text-slate-400 text-xs">{k.key_prefix}…</td>
                  <td className="py-2 pr-4 text-slate-400">{k.total_requests.toLocaleString()}</td>
                  <td className="py-2 pr-4 text-slate-400">${k.total_cost.toFixed(4)}</td>
                  <td className="py-2 pr-4 text-slate-500 text-xs">
                    {k.last_used_at ? new Date(k.last_used_at).toLocaleDateString() : '—'}
                  </td>
                  <td className="py-2">
                    <span className={`inline-flex px-1.5 py-0.5 rounded text-xs ${
                      k.is_active ? 'bg-green-500/20 text-green-400' : 'bg-slate-500/20 text-slate-400'
                    }`}>{k.is_active ? 'active' : 'inactive'}</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}

        {newKeyVal && <NewKeyBanner value={newKeyVal} onDismiss={() => setNewKeyVal(null)} />}
        {showAdd && (
          <UserKeyForm
            backends={backends}
            onSubmit={createKey.mutate}
            onClose={() => setShowAdd(false)}
          />
        )}
      </div>
    </div>
  )
}

// ── User row ──────────────────────────────────────────────────────────────────

function UserRow({ user, onEdit, onDelete }: {
  user: UserWithStats
  onEdit: () => void
  onDelete: () => void
}) {
  const [expanded, setExpanded] = useState(false)
  return (
    <>
      <tr
        className="border-b border-slate-700/50 hover:bg-slate-700/20 cursor-pointer"
        onClick={() => setExpanded(e => !e)}
      >
        <td className="px-3 py-4 w-8">
          {expanded
            ? <ChevronDown className="w-4 h-4 text-slate-400" />
            : <ChevronRight className="w-4 h-4 text-slate-400" />}
        </td>
        <td className="px-4 py-4 font-medium">{user.name}</td>
        <td className="px-4 py-4 text-slate-400 text-sm">{user.email}</td>
        <td className="px-4 py-4 text-slate-400 text-sm">
          <span className="flex items-center gap-1">
            <Key className="w-3 h-3" />{user.key_count}
          </span>
        </td>
        <td className="px-4 py-4 text-slate-400 text-sm">{user.total_requests.toLocaleString()}</td>
        <td className="px-4 py-4 text-slate-400 text-sm">${user.total_cost.toFixed(4)}</td>
        <td className="px-4 py-4">
          <span className={`inline-flex px-2 py-1 rounded-full text-xs ${
            user.is_active ? 'bg-green-500/20 text-green-400' : 'bg-slate-500/20 text-slate-400'
          }`}>
            {user.is_active ? 'Active' : 'Inactive'}
          </span>
        </td>
        <td className="px-4 py-4" onClick={e => e.stopPropagation()}>
          <div className="flex items-center justify-end gap-1">
            <button onClick={onEdit} className="p-1.5 text-slate-400 hover:text-white rounded">
              <Pencil className="w-4 h-4" />
            </button>
            <button onClick={onDelete} className="p-1.5 text-slate-400 hover:text-red-400 rounded">
              <Trash2 className="w-4 h-4" />
            </button>
          </div>
        </td>
      </tr>
      {expanded && (
        <tr className="bg-slate-900/40 border-b border-slate-700/50">
          <td colSpan={8} className="py-2">
            <UserKeys user={user} />
          </td>
        </tr>
      )}
    </>
  )
}

// ── Page ──────────────────────────────────────────────────────────────────────

function TabBar({ active, onChange }: { active: string; onChange: (t: string) => void }) {
  const tabs = [{ id: 'users', label: 'Users' }, { id: 'apikeys', label: 'API Keys' }]
  return (
    <div className="flex gap-0 border-b border-slate-700 mb-6">
      {tabs.map(t => (
        <button key={t.id} onClick={() => onChange(t.id)}
          className={`px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
            active === t.id
              ? 'border-blue-400 text-blue-400'
              : 'border-transparent text-slate-400 hover:text-white'
          }`}>
          {t.label}
        </button>
      ))}
    </div>
  )
}

export default function Users() {
  const { toast } = useToast()
  const queryClient = useQueryClient()
  const [tab, setTab]                   = useState<'users' | 'apikeys'>('users')
  const [showCreate, setShowCreate]     = useState(false)
  const [editing, setEditing]           = useState<UserWithStats | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<UserWithStats | null>(null)

  const { data: users = [], isLoading } = useQuery<UserWithStats[]>({
    queryKey: ['users'],
    queryFn: usersApi.list,
  })

  const createMutation = useMutation({
    mutationFn: usersApi.create,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] })
      toast('User created', 'success')
      setShowCreate(false)
    },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: number; data: Partial<UserWithStats> }) => usersApi.update(id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] })
      toast('User updated', 'success')
      setEditing(null)
    },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  const deleteMutation = useMutation({
    mutationFn: usersApi.delete,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] })
      toast('User deleted', 'success')
      setDeleteTarget(null)
    },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  // Aggregate totals for summary strip
  const totalRequests = users.reduce((s, u) => s + u.total_requests, 0)
  const totalCost     = users.reduce((s, u) => s + u.total_cost, 0)

  return (
    <div>
      <TabBar active={tab} onChange={t => setTab(t as 'users' | 'apikeys')} />
      {tab === 'apikeys' ? <APIKeys /> : null}
      {tab === 'users' && <>
      <div className="flex flex-wrap items-center justify-between gap-3 mb-6">
        <div>
          <h2 className="text-2xl font-bold">Users</h2>
          <p className="text-slate-400 text-sm mt-1">Users own API keys. Usage is tracked per-key and rolled up here.</p>
        </div>
        <button
          onClick={() => setShowCreate(true)}
          className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg text-sm"
        >
          <Plus className="w-4 h-4" /> New user
        </button>
      </div>

      {/* Summary strip */}
      {users.length > 0 && (
        <div className="grid grid-cols-3 gap-3 mb-5">
          {[
            { label: 'Users', value: String(users.length) },
            { label: 'Total requests', value: totalRequests.toLocaleString() },
            { label: 'Total cost', value: `$${totalCost.toFixed(4)}` },
          ].map(s => (
            <div key={s.label} className="bg-slate-800 rounded-lg p-4 border border-slate-700">
              <p className="text-xs text-slate-400">{s.label}</p>
              <p className="text-xl font-bold mt-0.5">{s.value}</p>
            </div>
          ))}
        </div>
      )}

      <div className="bg-slate-800 rounded-lg border border-slate-700 overflow-hidden">
        {isLoading ? (
          <div className="flex items-center justify-center h-48">
            <div className="animate-spin w-7 h-7 border-2 border-blue-400 border-t-transparent rounded-full" />
          </div>
        ) : users.length === 0 ? (
          <EmptyState icon={UsersIcon} message="No users yet" hint="Create a user to start tracking usage per person." />
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[700px]">
              <thead>
                <tr className="border-b border-slate-700">
                  <th className="w-8 px-3 py-3" />
                  <th className="text-left px-4 py-3 text-sm text-slate-400">Name</th>
                  <th className="text-left px-4 py-3 text-sm text-slate-400">Email</th>
                  <th className="text-left px-4 py-3 text-sm text-slate-400">Keys</th>
                  <th className="text-left px-4 py-3 text-sm text-slate-400">Requests</th>
                  <th className="text-left px-4 py-3 text-sm text-slate-400">Cost</th>
                  <th className="text-left px-4 py-3 text-sm text-slate-400">Status</th>
                  <th className="text-right px-4 py-3 text-sm text-slate-400">Actions</th>
                </tr>
              </thead>
              <tbody>
                {users.map(u => (
                  <UserRow
                    key={u.id}
                    user={u}
                    onEdit={() => setEditing(u)}
                    onDelete={() => setDeleteTarget(u)}
                  />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {showCreate && (
        <Modal title="New user" onClose={() => setShowCreate(false)}>
          <UserForm onSubmit={createMutation.mutate} onClose={() => setShowCreate(false)} />
        </Modal>
      )}

      {editing && (
        <Modal title="Edit user" onClose={() => setEditing(null)}>
          <UserForm
            initial={editing}
            onSubmit={data => updateMutation.mutate({ id: editing.id, data })}
            onClose={() => setEditing(null)}
          />
        </Modal>
      )}

      {deleteTarget && (
        <ConfirmDialog
          title="Delete user"
          message={`Delete "${deleteTarget.name}"? Their API keys will become unowned but won't be deleted.`}
          onConfirm={() => deleteMutation.mutate(deleteTarget.id)}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
      </>}
    </div>
  )
}
