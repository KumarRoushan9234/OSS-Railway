import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from 'react'

// Hash router: #/, #/new, #/settings, #/s/12/logs?first=1
function subscribeHash(cb: () => void) {
  window.addEventListener('hashchange', cb)
  return () => window.removeEventListener('hashchange', cb)
}

export function useRoute() {
  const hash = useSyncExternalStore(subscribeHash, () => location.hash.slice(1) || '/')
  const [path, q = ''] = hash.split('?')
  return { parts: path.split('/').filter(Boolean), query: new URLSearchParams(q) }
}

export function go(path: string) {
  location.hash = path
}

// usePoll runs load now and every `ms`, and returns [data, reload].
export function usePoll<T>(load: () => Promise<T>, ms: number, deps: unknown[] = []): [T | null, () => void] {
  const [data, setData] = useState<T | null>(null)
  const loadRef = useRef(load)
  loadRef.current = load
  const reload = useCallback(() => {
    loadRef.current().then(setData, () => {})
  }, [])
  useEffect(() => {
    reload()
    const t = setInterval(reload, ms)
    return () => clearInterval(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ms, reload, ...deps])
  return [data, reload]
}

// useFlash shows a short confirmation ("Saved") on a button, then resets.
export function useFlash(): [string, (v: string) => void] {
  const [v, setV] = useState('')
  const timer = useRef<number>(0)
  const flash = useCallback((next: string) => {
    setV(next)
    clearTimeout(timer.current)
    timer.current = window.setTimeout(() => setV(''), 1500)
  }, [])
  return [v, flash]
}

export function useKey(handler: (e: KeyboardEvent) => void) {
  const ref = useRef(handler)
  ref.current = handler
  useEffect(() => {
    const on = (e: KeyboardEvent) => ref.current(e)
    window.addEventListener('keydown', on)
    return () => window.removeEventListener('keydown', on)
  }, [])
}

export function isTyping(e: KeyboardEvent) {
  const t = e.target as HTMLElement
  return t.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(t.tagName)
}
