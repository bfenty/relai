/** @type {import('tailwindcss').Config} */

// Relai's palette ("Ember"). Every color has one meaning, the same on every page:
//
//   accent  orange  attention: live state, selection, primary actions, the number to
//                   watch, anomalies. Never an ordinary data series.
//   input   blue    prompt / prefill tokens; `cached` is reused prompt (a lighter step)
//   output  violet  generated tokens / decode, including thinking
//   good / warn / crit   health and errors only, always beside an icon or label
//   slate   cool neutral grays: chrome, text, volume, baselines, "other"
//
// Orange, blue and violet are a split-complementary triad (orange 50°, blue 250°,
// violet 305° in OKLCH): the one warm, saturated hue on cool-tinted neutrals reads first.
// Blue and violet are separated by lightness as well as hue so they stay distinct
// with red-green color blindness. Ramps are generated in OKLCH; keep src/theme.ts,
// which charts use, in sync.
const slate = {
  50: '#f7f9fb',
  100: '#edeff1',
  200: '#dce2e9',
  300: '#c9ced6',
  400: '#acb2b9', // secondary text
  500: '#81878d', // tertiary text, icons
  600: '#51565c',
  700: '#2e3238', // borders, hover fills
  800: '#12161b', // panels
  900: '#0b0f14', // page
  950: '#06080d', // chrome (top bar, navigation)
}

const accent = {
  50: '#fef2ec',
  100: '#fee3d5',
  200: '#ffcdb2',
  300: '#feb48a',
  400: '#ffa068', // accent text and links on dark
  500: '#fa7c20', // primary buttons (dark text)
  600: '#d16100',
  700: '#a94e00',
  800: '#833b01',
  900: '#5f2901',
  950: '#411a00',
}

const input = {
  50: '#eef6ff',
  100: '#daecff',
  200: '#beddfe',
  300: '#9cccfe',
  400: '#72b8fe',
  500: '#5aa3ec',
  600: '#418ad1',
  700: '#226fb3',
  800: '#015493',
  900: '#023c6b',
  950: '#082845',
}
const output = {
  50: '#f8f2ff',
  100: '#efe4fe',
  200: '#e3cefe',
  300: '#d7b6fe',
  400: '#c79bfa',
  500: '#b386e4',
  600: '#9a6ec9',
  700: '#7f53ac',
  800: '#65398f',
  900: '#482866',
  950: '#2e1c41',
}
const good = {
  50: '#e0fdef',
  100: '#cef4e2',
  200: '#a7ebcc',
  300: '#74e0b4',
  400: '#42ce9c',
  500: '#22b988',
  600: '#029e72',
  700: '#067f5c',
  800: '#016246',
  900: '#004631',
  950: '#012f1f',
}
const warn = {
  50: '#fff5d8',
  100: '#f8e9bb',
  200: '#f3d782',
  300: '#edc327',
  400: '#d6ae03',
  500: '#be9a07',
  600: '#a18304',
  700: '#826901',
  800: '#645000',
  900: '#483900',
  950: '#302500',
}
const crit = {
  50: '#fff1f2',
  100: '#fee1e4',
  200: '#fec9ce',
  300: '#feaeb7',
  400: '#ff8a9a',
  500: '#fe5f7e',
  600: '#e83964',
  700: '#c7054a',
  800: '#9b0038',
  900: '#710127',
  950: '#4d0218',
}

export default {
  content: ['./index.html', './src/**/*.{js,ts,jsx,tsx}'],
  theme: {
    extend: {
      colors: {
        slate,
        accent,
        input,
        cached: '#80aace',
        output,
        good,
        warn,
        crit,
      },
      fontFamily: {
        sans: ['"IBM Plex Sans"', 'system-ui', '-apple-system', 'Segoe UI', 'sans-serif'],
        mono: ['"IBM Plex Mono"', 'ui-monospace', 'SFMono-Regular', 'Menlo', 'monospace'],
      },
      // Console radii: controls 3px, panels 4px.
      borderRadius: {
        DEFAULT: '3px',
        md: '3px',
        lg: '4px',
        xl: '6px',
        '2xl': '8px',
      },
      boxShadow: {
        panel: 'none',
        popover: '0 8px 24px rgb(0 0 0 / 0.55), 0 0 0 1px rgb(255 255 255 / 0.06)',
      },
    },
  },
  plugins: [],
}
