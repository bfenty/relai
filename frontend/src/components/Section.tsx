import { useState, type ReactNode } from 'react'
import { ChevronDown } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'

/**
 * A collapsible panel for grouping the analytics page by topic.
 *
 * The open/closed state is remembered per section id, so a reader who only cares
 * about tokens is not made to re-collapse cache diagnostics on every visit. Storage
 * can throw or come back empty in a private window, so every access is guarded and
 * falls back to the caller's default.
 */
export default function Section({
  id, title, subtitle, icon: Icon, iconColor = 'text-slate-400',
  defaultOpen = true, badge, children,
}: {
  id: string
  title: string
  subtitle?: string
  icon?: LucideIcon
  iconColor?: string
  defaultOpen?: boolean
  badge?: ReactNode
  children: ReactNode
}) {
  const storageKey = `relai.section.${id}`
  const [open, setOpen] = useState<boolean>(() => {
    try {
      const v = localStorage.getItem(storageKey)
      return v === null ? defaultOpen : v === '1'
    } catch {
      return defaultOpen
    }
  })

  const toggle = () => {
    setOpen(prev => {
      const next = !prev
      try { localStorage.setItem(storageKey, next ? '1' : '0') } catch { /* not essential */ }
      return next
    })
  }

  return (
    <section className="bg-slate-800/50 rounded-lg border border-slate-700 mb-4 overflow-hidden">
      <button
        onClick={toggle}
        aria-expanded={open}
        className="w-full flex items-center gap-3 px-5 py-3.5 text-left hover:bg-slate-700/30 transition-colors"
      >
        <ChevronDown
          className={`w-4 h-4 text-slate-500 shrink-0 transition-transform ${open ? '' : '-rotate-90'}`}
        />
        {Icon && <Icon className={`w-4 h-4 shrink-0 ${iconColor}`} />}
        <span className="font-semibold text-sm">{title}</span>
        {subtitle && (
          <span className="text-xs text-slate-500 truncate hidden sm:inline">— {subtitle}</span>
        )}
        <span className="ml-auto flex items-center gap-2 shrink-0">{badge}</span>
      </button>
      {open && <div className="px-5 pb-5 pt-1">{children}</div>}
    </section>
  )
}
