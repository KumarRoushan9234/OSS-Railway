import { useSyncExternalStore } from 'react'

// Appearance preferences, per browser. Applied as data attributes and CSS variables on <html>.
export type Theme = 'system' | 'light' | 'dark'
export type Density = 'comfortable' | 'compact'

export interface Prefs {
  theme: Theme
  accent: string
  density: Density
  grid: boolean
}

export const accents: Record<string, { light: string; dark: string }> = {
  green: { light: '#0e8a62', dark: '#34c48f' },
  blue: { light: '#2563eb', dark: '#60a5fa' },
  violet: { light: '#7c3aed', dark: '#a78bfa' },
  orange: { light: '#c2410c', dark: '#fb923c' },
  pink: { light: '#be185d', dark: '#f472b6' },
  gray: { light: '#3f3f46', dark: '#d4d4d8' },
}

const KEY = 'ossr-prefs'
const defaults: Prefs = { theme: 'system', accent: 'green', density: 'comfortable', grid: true }

function read(): Prefs {
  try {
    return { ...defaults, ...JSON.parse(localStorage.getItem(KEY) || '{}') }
  } catch {
    return defaults
  }
}

let prefs = read()
const listeners = new Set<() => void>()
const dark = window.matchMedia('(prefers-color-scheme: dark)')

function apply() {
  const root = document.documentElement
  const mode = prefs.theme === 'system' ? (dark.matches ? 'dark' : 'light') : prefs.theme
  const a = accents[prefs.accent] ?? accents.green
  root.dataset.theme = mode
  root.dataset.density = prefs.density
  root.style.setProperty('--accent', a[mode])
  root.style.setProperty('--accent-text', mode === 'dark' ? '#0b0b0a' : '#ffffff')
  root.style.colorScheme = mode
}

dark.addEventListener('change', () => {
  apply()
  listeners.forEach((l) => l())
})
apply()

export function setPrefs(patch: Partial<Prefs>) {
  prefs = { ...prefs, ...patch }
  try {
    localStorage.setItem(KEY, JSON.stringify(prefs))
  } catch {
    // private mode: keep the change for this visit only
  }
  apply()
  listeners.forEach((l) => l())
}

export function usePrefs(): Prefs {
  return useSyncExternalStore(
    (cb) => {
      listeners.add(cb)
      return () => listeners.delete(cb)
    },
    () => prefs,
  )
}
