interface EmptyStateProps {
  message: string
  hint?: string
  icon?: React.ElementType
}

export function EmptyState({ message, hint, icon: Icon }: EmptyStateProps) {
  return (
    <div className="flex flex-col items-center justify-center py-16 text-center">
      {Icon && <Icon className="w-10 h-10 text-slate-600 mb-3" />}
      <p className="text-slate-400">{message}</p>
      {hint && <p className="text-slate-500 text-sm mt-1">{hint}</p>}
    </div>
  )
}
