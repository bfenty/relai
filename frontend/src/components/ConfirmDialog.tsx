import { Modal } from './Modal'

interface ConfirmDialogProps {
  title?: string
  message: string
  confirmLabel?: string
  onConfirm: () => void
  onCancel: () => void
  dangerous?: boolean
}

export function ConfirmDialog({
  title = 'Confirm',
  message,
  confirmLabel = 'Delete',
  onConfirm,
  onCancel,
  dangerous = true,
}: ConfirmDialogProps) {
  return (
    <Modal title={title} onClose={onCancel} size="sm">
      <p className="text-slate-300 text-sm">{message}</p>
      <div className="flex gap-2 justify-end mt-5">
        <button
          onClick={onCancel}
          className="px-4 py-2 text-sm text-slate-400 hover:text-white"
        >
          Cancel
        </button>
        <button
          onClick={onConfirm}
          className={`px-4 py-2 text-sm rounded-lg ${
            dangerous
              ? 'bg-red-600 hover:bg-red-700'
              : 'bg-blue-600 hover:bg-blue-700'
          }`}
        >
          {confirmLabel}
        </button>
      </div>
    </Modal>
  )
}
