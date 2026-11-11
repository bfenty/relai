import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { routingApi } from '../api/client'
import { RoutingRule } from '../types'
import { Plus, Pencil, Trash2, GripVertical, GitBranch, ToggleLeft, ToggleRight } from 'lucide-react'
import { useState } from 'react'
import {
  DndContext, closestCenter, KeyboardSensor, PointerSensor,
  useSensor, useSensors, DragEndEvent,
} from '@dnd-kit/core'
import {
  arrayMove, SortableContext, sortableKeyboardCoordinates,
  useSortable, verticalListSortingStrategy,
} from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import { Modal } from '../components/Modal'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { EmptyState } from '../components/EmptyState'
import { TagInput } from '../components/TagInput'
import { useToast } from '../components/Toast'

// ── Strategy metadata ─────────────────────────────────────────────────────────

const STRATEGIES = [
  { value: 'round_robin',   label: 'Round Robin',   desc: 'Distribute requests evenly across all active hosts' },
  { value: 'weighted',      label: 'Weighted',      desc: 'Route proportionally based on each host\'s weight' },
  { value: 'model_based',   label: 'Model Based',   desc: 'Route to the host that has a mapping for the requested model' },
  { value: 'cost_based',    label: 'Cost Based',    desc: 'Always prefer the host with the lowest reported cost' },
  { value: 'latency_based', label: 'Latency Based', desc: 'Always prefer the host with the lowest recent latency' },
]

// ── Rule form ─────────────────────────────────────────────────────────────────

function RuleForm({ initial, onSubmit, onClose }: {
  initial?: RoutingRule
  onSubmit: (data: Partial<RoutingRule>) => void
  onClose: () => void
}) {
  const [name, setName]       = useState(initial?.name ?? '')
  const [strategy, setStrategy] = useState(initial?.strategy ?? 'round_robin')
  const [priority, setPriority] = useState(initial?.priority ?? 10)
  const [models, setModels]   = useState<string[]>(
    Array.isArray(initial?.conditions?.models) ? (initial.conditions.models as string[]) : []
  )

  const currentStrategy = STRATEGIES.find(s => s.value === strategy)

  return (
    <form onSubmit={e => {
      e.preventDefault()
      const conditions: Record<string, unknown> = models.length ? { models } : {}
      onSubmit({ name, strategy, priority, conditions })
    }} className="space-y-4">
      <div>
        <label className="block text-sm text-slate-400 mb-1">Name</label>
        <input value={name} onChange={e => setName(e.target.value)} required
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">Strategy</label>
        <select value={strategy} onChange={e => setStrategy(e.target.value)}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500">
          {STRATEGIES.map(s => <option key={s.value} value={s.value}>{s.label}</option>)}
        </select>
        {currentStrategy && (
          <p className="text-xs text-slate-500 mt-1">{currentStrategy.desc}</p>
        )}
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">
          Priority <span className="text-slate-500">(lower number = evaluated first)</span>
        </label>
        <input type="number" value={priority} onChange={e => setPriority(Number(e.target.value))} min={1}
          className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500" />
      </div>
      <div>
        <label className="block text-sm text-slate-400 mb-1">
          Model filter <span className="text-slate-500">(empty = matches all models; supports glob prefix e.g. gpt-*)</span>
        </label>
        <TagInput values={models} onChange={setModels} placeholder="gpt-4o  (press Enter)" />
      </div>
      <div className="flex gap-2 justify-end pt-1">
        <button type="button" onClick={onClose} className="px-4 py-2 text-slate-400 hover:text-white text-sm">Cancel</button>
        <button type="submit" className="px-4 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg text-sm">
          {initial ? 'Save changes' : 'Create rule'}
        </button>
      </div>
    </form>
  )
}

// ── Sortable rule row ─────────────────────────────────────────────────────────

function RuleRow({ rule, onEdit, onDelete, onToggle }: {
  rule: RoutingRule
  onEdit: (r: RoutingRule) => void
  onDelete: (r: RoutingRule) => void
  onToggle: (r: RoutingRule) => void
}) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } =
    useSortable({ id: rule.id })

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
  }

  const strategy = STRATEGIES.find(s => s.value === rule.strategy)
  const modelList = Array.isArray(rule.conditions?.models) ? (rule.conditions.models as string[]) : []

  return (
    <tr ref={setNodeRef} style={style}
      className="border-b border-slate-700/50 hover:bg-slate-700/20">
      <td className="px-3 py-4 w-8">
        <button {...attributes} {...listeners}
          className="cursor-grab active:cursor-grabbing text-slate-600 hover:text-slate-400 touch-none">
          <GripVertical className="w-4 h-4" />
        </button>
      </td>
      <td className="px-4 py-4 font-mono text-slate-400 text-sm">{rule.priority}</td>
      <td className="px-4 py-4 font-medium">{rule.name}</td>
      <td className="px-4 py-4">
        <span className="px-2 py-1 bg-blue-500/20 text-blue-400 rounded text-xs" title={strategy?.desc}>
          {strategy?.label ?? rule.strategy}
        </span>
      </td>
      <td className="px-4 py-4 text-sm">
        {modelList.length > 0 ? (
          <div className="flex flex-wrap gap-1">
            {modelList.map(m => (
              <span key={m} className="px-1.5 py-0.5 bg-slate-700 text-slate-300 rounded text-xs font-mono">{m}</span>
            ))}
          </div>
        ) : (
          <span className="text-slate-600 text-xs">all models</span>
        )}
      </td>
      <td className="px-4 py-4">
        <span className={`inline-flex px-2 py-1 rounded-full text-xs ${
          rule.is_active ? 'bg-green-500/20 text-green-400' : 'bg-slate-500/20 text-slate-400'
        }`}>
          {rule.is_active ? 'Active' : 'Inactive'}
        </span>
      </td>
      <td className="px-4 py-4">
        <div className="flex items-center justify-end gap-1">
          <button onClick={() => onToggle(rule)} title={rule.is_active ? 'Deactivate' : 'Activate'}
            className="p-1.5 text-slate-400 hover:text-yellow-400 rounded">
            {rule.is_active
              ? <ToggleRight className="w-4 h-4 text-green-400" />
              : <ToggleLeft className="w-4 h-4" />}
          </button>
          <button onClick={() => onEdit(rule)} className="p-1.5 text-slate-400 hover:text-white rounded">
            <Pencil className="w-4 h-4" />
          </button>
          <button onClick={() => onDelete(rule)} className="p-1.5 text-slate-400 hover:text-red-400 rounded">
            <Trash2 className="w-4 h-4" />
          </button>
        </div>
      </td>
    </tr>
  )
}

// ── Page ──────────────────────────────────────────────────────────────────────

export default function Routing() {
  const { toast } = useToast()
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate]     = useState(false)
  const [editing, setEditing]           = useState<RoutingRule | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<RoutingRule | null>(null)
  const [localOrder, setLocalOrder]     = useState<RoutingRule[] | null>(null)

  const sensors = useSensors(
    useSensor(PointerSensor),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  )

  const { data: rules = [], isLoading } = useQuery({
    queryKey: ['routing-rules'],
    queryFn: routingApi.list,
  })

  const displayed = localOrder ?? [...rules].sort((a, b) => a.priority - b.priority)

  const createMutation = useMutation({
    mutationFn: routingApi.create,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['routing-rules'] })
      toast('Rule created', 'success')
      setShowCreate(false)
    },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: number; data: Partial<RoutingRule> }) => routingApi.update(id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['routing-rules'] })
      setLocalOrder(null)
      setEditing(null)
    },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  const deleteMutation = useMutation({
    mutationFn: routingApi.delete,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['routing-rules'] })
      toast('Rule deleted', 'success')
      setDeleteTarget(null)
    },
    onError: (e: Error) => toast(e.message, 'error'),
  })

  const handleDragEnd = (event: DragEndEvent) => {
    const { active, over } = event
    if (!over || active.id === over.id) return

    const oldIndex = displayed.findIndex(r => r.id === active.id)
    const newIndex = displayed.findIndex(r => r.id === over.id)
    const reordered = arrayMove(displayed, oldIndex, newIndex)

    // Assign sequential priorities based on new order
    const updated = reordered.map((r, i) => ({ ...r, priority: i + 1 }))
    setLocalOrder(updated)

    // Persist each changed priority
    updated.forEach(r => {
      if (r.priority !== displayed[displayed.findIndex(x => x.id === r.id)].priority) {
        updateMutation.mutate({ id: r.id, data: { priority: r.priority } })
      }
    })
  }

  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-3 mb-6">
        <div>
          <h2 className="text-2xl font-bold">Routing Rules</h2>
          <p className="text-slate-400 text-sm mt-1">Rules are evaluated in priority order. Drag to reorder. Falls back to round-robin when no rule matches.</p>
        </div>
        <button onClick={() => setShowCreate(true)}
          className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg text-sm">
          <Plus className="w-4 h-4" /> Add rule
        </button>
      </div>

      <div className="bg-slate-800 rounded-lg border border-slate-700 overflow-hidden">
        {isLoading ? (
          <div className="flex items-center justify-center h-48">
            <div className="animate-spin w-7 h-7 border-2 border-blue-400 border-t-transparent rounded-full" />
          </div>
        ) : displayed.length === 0 ? (
          <EmptyState icon={GitBranch} message="No routing rules" hint="All requests will use round-robin by default." />
        ) : (
          <div className="overflow-x-auto">
          <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
            <SortableContext items={displayed.map(r => r.id)} strategy={verticalListSortingStrategy}>
              <table className="w-full min-w-[600px]">
                <thead>
                  <tr className="border-b border-slate-700">
                    <th className="w-8 px-3 py-3" />
                    <th className="text-left px-4 py-3 text-sm text-slate-400">Priority</th>
                    <th className="text-left px-4 py-3 text-sm text-slate-400">Name</th>
                    <th className="text-left px-4 py-3 text-sm text-slate-400">Strategy</th>
                    <th className="text-left px-4 py-3 text-sm text-slate-400">Models</th>
                    <th className="text-left px-4 py-3 text-sm text-slate-400">Status</th>
                    <th className="text-right px-4 py-3 text-sm text-slate-400">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {displayed.map(rule => (
                    <RuleRow
                      key={rule.id}
                      rule={rule}
                      onEdit={setEditing}
                      onDelete={setDeleteTarget}
                      onToggle={r => updateMutation.mutate({ id: r.id, data: { is_active: !r.is_active } })}
                    />
                  ))}
                </tbody>
              </table>
            </SortableContext>
          </DndContext>
          </div>
        )}
      </div>

      {showCreate && (
        <Modal title="Create routing rule" onClose={() => setShowCreate(false)}>
          <RuleForm onSubmit={createMutation.mutate} onClose={() => setShowCreate(false)} />
        </Modal>
      )}

      {editing && (
        <Modal title="Edit routing rule" onClose={() => setEditing(null)}>
          <RuleForm
            initial={editing}
            onSubmit={data => updateMutation.mutate({ id: editing.id, data })}
            onClose={() => setEditing(null)}
          />
        </Modal>
      )}

      {deleteTarget && (
        <ConfirmDialog
          title="Delete rule"
          message={`Delete "${deleteTarget.name}"?`}
          onConfirm={() => deleteMutation.mutate(deleteTarget.id)}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
    </div>
  )
}
