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
          className="btn-ghost"
        >
          Cancel
        </button>
        <button
          onClick={onConfirm}
          className={dangerous
            ? 'btn border-crit-600 bg-crit-600 text-white hover:border-crit-700 hover:bg-crit-700'
            : 'btn-primary'}
        >
          {confirmLabel}
        </button>
      </div>
    </Modal>
  )
}
