/**
 * Compression mode picker, shared by model mappings and routers.
 *
 * Compression here is prefix-stable by construction: each message is compressed in
 * isolation, so a conversation's earlier turns are forwarded byte-identically as it
 * grows and the upstream keeps hitting its KV cache. That is what makes it safe to
 * enable on long conversations, where a busted prefix costs far more than the tokens
 * saved.
 */
export type CompressionMode = '' | 'lossless' | 'aggressive'

const OPTIONS: { value: CompressionMode; label: string; hint: string }[] = [
  { value: '', label: 'Off', hint: 'Forward message content unchanged.' },
  {
    value: 'lossless',
    label: 'Lossless',
    hint: 'Compact JSON whitespace. Same values, same key order — nothing is lost.',
  },
  {
    value: 'aggressive',
    label: 'Aggressive',
    hint: 'Also cap long JSON arrays, noting how many items were omitted. Lossy: a request needing the omitted items cannot see them.',
  },
]

export default function CompressionSelect({
  value,
  onChange,
  label = 'Compression',
  inherit,
}: {
  value: string
  onChange: (v: CompressionMode) => void
  label?: string
  /** Shown when "Off" means "fall back to the router's setting" rather than "no compression". */
  inherit?: string
}) {
  const mode = (value || '') as CompressionMode
  const active = OPTIONS.find(o => o.value === mode) ?? OPTIONS[0]

  return (
    <div>
      <label className="block text-sm text-slate-400 mb-1">
        {label}{' '}
        <span className="text-slate-500">(prefix-stable — safe for long conversations)</span>
      </label>
      <select
        value={mode}
        onChange={e => onChange(e.target.value as CompressionMode)}
        className="w-full px-3 py-2 bg-slate-700 border border-slate-600 rounded-lg text-white focus:outline-none focus:border-blue-500"
      >
        {OPTIONS.map(o => (
          <option key={o.value} value={o.value}>{o.label}</option>
        ))}
      </select>
      <p className="text-xs text-slate-500 mt-1">
        {mode === '' && inherit ? inherit : active.hint}
      </p>
    </div>
  )
}
