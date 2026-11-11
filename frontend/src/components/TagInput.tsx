import { useState, KeyboardEvent } from 'react'
import { X } from 'lucide-react'

interface TagInputProps {
  values: string[]
  onChange: (values: string[]) => void
  placeholder?: string
}

export function TagInput({ values, onChange, placeholder = 'Type and press Enter' }: TagInputProps) {
  const [input, setInput] = useState('')

  const add = () => {
    const v = input.trim()
    if (v && !values.includes(v)) {
      onChange([...values, v])
    }
    setInput('')
  }

  const remove = (tag: string) => onChange(values.filter(v => v !== tag))

  const handleKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') { e.preventDefault(); add() }
    if (e.key === 'Backspace' && !input && values.length > 0) {
      remove(values[values.length - 1])
    }
  }

  return (
    <div className="flex flex-wrap gap-1.5 p-2 bg-slate-700 border border-slate-600 rounded-lg min-h-[42px] focus-within:border-blue-500">
      {values.map(tag => (
        <span key={tag} className="inline-flex items-center gap-1 px-2 py-0.5 bg-blue-600/30 text-blue-300 rounded text-sm font-mono">
          {tag}
          <button type="button" onClick={() => remove(tag)} className="hover:text-white">
            <X className="w-3 h-3" />
          </button>
        </span>
      ))}
      <input
        type="text"
        value={input}
        onChange={e => setInput(e.target.value)}
        onKeyDown={handleKey}
        onBlur={add}
        placeholder={values.length === 0 ? placeholder : ''}
        className="flex-1 min-w-24 bg-transparent outline-none text-sm text-white placeholder-slate-500"
      />
    </div>
  )
}
