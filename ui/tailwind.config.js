/** @type {import('tailwindcss').Config} */

// The inspector shares the website's theme (site/styles.css): warm paper and
// stone neutrals, one moss accent. Rather than renaming classes across every
// component, the two palettes the UI already uses are redefined: `slate` is
// the stone scale (paper, lines, ink) and `sky`, the link and selection
// colour, is the moss accent. Status colours (emerald, red, amber, blue,
// violet) keep Tailwind's defaults so pass, fail and running still read at a
// glance.
const stone = {
  50: '#efece4',
  100: '#e7e3d9',
  200: '#e2ddd2',
  300: '#d3cdc0',
  400: '#a39d90',
  500: '#797467',
  600: '#5d5950',
  700: '#4a463e',
  800: '#2b2a25',
  900: '#1c1a16',
  950: '#121210',
};

const moss = {
  50: '#eef7f3',
  100: '#dfefe8',
  200: '#b6dbcb',
  300: '#9adcc8',
  400: '#6fd0b3',
  500: '#2fa585',
  600: '#0c7a62',
  700: '#08594a',
  800: '#07473b',
  900: '#0b3a31',
  950: '#16302a',
};

export default {
  darkMode: 'class',
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        // The page ground in light mode; slate-50 sits one step darker for
        // panels and hovers, and white is a raised surface, as on the site.
        paper: '#f6f4ee',
        slate: stone,
        sky: moss,
      },
      fontFamily: {
        sans: ['ui-sans-serif', 'system-ui', '-apple-system', '"Segoe UI"', 'Roboto', '"Helvetica Neue"', 'Arial', 'sans-serif'],
        mono: ['ui-monospace', '"SF Mono"', 'SFMono-Regular', '"JetBrains Mono"', 'Menlo', 'Consolas', '"Liberation Mono"', 'monospace'],
      },
      borderRadius: {
        DEFAULT: '6px',
        md: '8px',
        lg: '12px',
      },
    },
  },
  plugins: [],
};
