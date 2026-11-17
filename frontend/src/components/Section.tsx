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
    <section className="panel mb-4 overflow-hidden">
      <button
        onClick={toggle}
        aria-expanded={open}
        className={`flex w-full items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-slate-700/25 ${open ? 'border-b border-slate-700/80' : ''}`}
      >
        <ChevronDown
          className={`h-4 w-4 shrink-0 text-slate-500 transition-transform ${open ? '' : '-rotate-90'}`}
        />
        {Icon && <Icon className={`h-4 w-4 shrink-0 ${iconColor}`} />}
        <span className="panel-title">{title}</span>
        {subtitle && <span className="hidden truncate text-xs text-slate-500 sm:inline">{subtitle}</span>}
        <span className="ml-auto flex shrink-0 items-center gap-2">{badge}</span>
      </button>
      {open && <div className="p-4">{children}</div>}
    </section>
  )
}
