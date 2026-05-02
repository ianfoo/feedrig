/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        bg: '#0e0f12',
        panel: '#1a1c22',
        panel2: '#22252d',
        fg: '#e7e9ee',
        fgdim: '#9aa0ad',
        border: '#2a2d36',
        accent: '#6ea8ff',
        ok: '#4ade80',
        warn: '#facc15',
        danger: '#f87171',
      },
    },
  },
  plugins: [],
}
