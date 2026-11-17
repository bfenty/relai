// Colors for code that can't use Tailwind classes (Recharts props, inline styles).
// Mirrors tailwind.config.js, which documents what each color means. In short:
//
//   attention  orange: live state, the number to watch, anomalies, cache lost
//   input      prompt / prefill tokens      cached  reused prompt (lighter input step)
//   output     generated tokens / decode    status  health and errors, with an icon or label
//   volume / neutral   counts, baselines, "fresh", "other"
//
// Entities (hosts, models, keys, routers) take ENTITY colors, which avoid all of the
// above so a series never borrows a meaning it doesn't have.

export const ui = {
  page: '#0b0f14',
  panel: '#12161b',
  panelRaised: '#1a1e24',
  border: '#2e3238',
  grid: '#21262b',
  text: '#edeff1',
  textSecondary: '#acb2b9',
  textTertiary: '#81878d',
  accent: '#fa7c20',
  accentText: '#ffa068',
}

export const sem = {
  attention: '#fa7c20',
  input: '#226fb3',
  cached: '#80aace',
  output: '#a87fd5',
  volume: '#acb2b9',
  neutral: '#81878d',
}

export const status = {
  good: '#47c496',
  warn: '#f0c543',
  bad: '#e83964',
}

// Entity colors, in an order validated for red-green color blindness on the panel
// surface (adjacent pairs ΔE ≥ 8.9, OKLab). Five is the cap: further entities fold
// into ENTITY_OTHER rather than getting a generated hue.
export const ENTITY = [
  '#20a89b', // teal
  '#b34b71', // rose
  '#5f62a8', // indigo
  '#76a35c', // sage
  '#ab639c', // mauve
]
export const ENTITY_OTHER = '#51565c'

/** Color for the i-th entity in a list sorted by a stable key; past the cap, "other". */
export function entityAt(index: number): string {
  return index < ENTITY.length ? ENTITY[index] : ENTITY_OTHER
}

// Shared Recharts styling so every chart reads as one system.
const monoFont = '"IBM Plex Mono", ui-monospace, monospace'
export const axisTick = { fill: ui.textTertiary, fontSize: 11, fontFamily: monoFont }
export const axisLine = { stroke: ui.border }
/** Spread onto <XAxis>/<YAxis>. */
export const AXIS = { stroke: ui.border, tick: axisTick, tickLine: false }
export const GRID = ui.grid
export const tooltipStyle = {
  contentStyle: {
    backgroundColor: ui.panelRaised,
    border: `1px solid ${ui.border}`,
    borderRadius: 4,
    fontSize: 12,
    boxShadow: '0 8px 24px rgb(0 0 0 / 0.55)',
  },
  labelStyle: { color: ui.text, fontWeight: 600, marginBottom: 4 },
  itemStyle: { color: ui.text, padding: 0 },
  cursor: { fill: 'rgb(255 255 255 / 0.04)' },
}
export const legendStyle = { fontSize: 11, color: ui.textSecondary }
