import { useEffect, useRef, useState } from 'react'
import type { Service } from '../api'

// One icon set: 24px grid, 2px stroke, round caps.
const paths: Record<string, string> = {
  plus: 'M12 5v14M5 12h14',
  minus: 'M5 12h14',
  x: 'M6 6l12 12M18 6L6 18',
  fit: 'M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5',
  settings: 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z',
  bell: 'M18 8a6 6 0 0 0-12 0c0 7-3 9-3 9h18s-3-2-3-9M13.7 21a2 2 0 0 1-3.4 0',
  search: 'M11 19a8 8 0 1 0 0-16 8 8 0 0 0 0 16zM21 21l-4.3-4.3',
  github: 'M9 19c-5 1.5-5-2.5-7-3m14 6v-3.9a3.4 3.4 0 0 0-.9-2.6c3.1-.3 6.4-1.5 6.4-7A5.4 5.4 0 0 0 20 4.8 5 5 0 0 0 19.9 1S18.7.7 16 2.5a13.4 13.4 0 0 0-7 0C6.3.7 5.1 1 5.1 1A5 5 0 0 0 5 4.8a5.4 5.4 0 0 0-1.5 3.7c0 5.4 3.3 6.6 6.4 7A3.4 3.4 0 0 0 9 18.1V22',
  git: 'M6 3v12M18 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM6 21a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM18 9a9 9 0 0 1-9 9',
  box: 'M21 16V8a2 2 0 0 0-1-1.7l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.7l7 4a2 2 0 0 0 2 0l7-4a2 2 0 0 0 1-1.7zM3.3 7L12 12l8.7-5M12 22V12',
  database: 'M12 8c5 0 9-1.3 9-3s-4-3-9-3-9 1.3-9 3 4 3 9 3zM21 12c0 1.7-4 3-9 3s-9-1.3-9-3M3 5v14c0 1.7 4 3 9 3s9-1.3 9-3V5',
  external: 'M15 3h6v6M10 14L21 3M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6',
  logout: 'M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9',
  sun: 'M12 17a5 5 0 1 0 0-10 5 5 0 0 0 0 10zM12 1v2M12 21v2M4.2 4.2l1.4 1.4M18.4 18.4l1.4 1.4M1 12h2M21 12h2M4.2 19.8l1.4-1.4M18.4 5.6l1.4-1.4',
  arrow: 'M5 12h14M12 5l7 7-7 7',
  server: 'M3 4h18v7H3zM3 13h18v7H3zM7 7.5h.01M7 16.5h.01',
  shield: 'M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z',
}

export function Icon({ name, size = 16 }: { name: keyof typeof paths | string; size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d={paths[name]} />
    </svg>
  )
}

export function kindIcon(source: Service['source']) {
  return source === 'github' ? 'github' : source === 'git' ? 'git' : source === 'image' ? 'box' : 'database'
}

export function Logo() {
  return (
    <svg width="20" height="20" viewBox="0 0 32 32" aria-hidden="true">
      <rect width="32" height="32" rx="8" fill="var(--accent)" />
      <path d="M9 22h14M9 16h14M9 10h14" stroke="var(--accent-text)" strokeWidth="3" strokeLinecap="round" />
    </svg>
  )
}

// Minimal line chart: values scaled to the box, max fixed or auto.
export function Sparkline({ values, max = 0, height = 40 }: { values: number[]; max?: number; height?: number }) {
  let points = ''
  if (values.length >= 2) {
    const top = max || Math.max(...values, 1)
    points = values.map((v, i) => `${(i / (values.length - 1)) * 100},${100 - Math.min(v / top, 1) * 100}`).join(' ')
  }
  return (
    <svg viewBox="0 0 100 100" preserveAspectRatio="none" style={{ height, width: '100%', display: 'block', overflow: 'visible' }} aria-hidden="true">
      {points && (
        <>
          <polygon points={`0,100 ${points} 100,100`} fill="var(--accent-soft)" />
          <polyline points={points} fill="none" stroke="var(--accent)" strokeWidth="1.5" vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
        </>
      )}
    </svg>
  )
}

// Streams lines from a Server-Sent Events endpoint and keeps the view pinned to the bottom.
// Lines are batched per animation frame so a fast build log does not re-render per line.
const MAX_LINES = 5000

export function Logs({ src }: { src: string }) {
  const [lines, setLines] = useState<string[]>([])
  const box = useRef<HTMLDivElement>(null)
  const pinned = useRef(true)

  useEffect(() => {
    setLines([])
    let buffer: string[] = []
    let frame = 0
    const flush = () => {
      frame = 0
      const add = buffer
      buffer = []
      setLines((prev) => {
        const next = prev.concat(add)
        return next.length > MAX_LINES ? next.slice(next.length - MAX_LINES) : next
      })
    }
    const es = new EventSource(src)
    es.onmessage = (e) => {
      buffer.push(JSON.parse(e.data))
      if (!frame) frame = requestAnimationFrame(flush)
    }
    es.addEventListener('done', () => es.close())
    es.onerror = () => es.close() // no auto-reconnect: it would replay the tail and duplicate lines
    return () => {
      es.close()
      cancelAnimationFrame(frame)
    }
  }, [src])

  useEffect(() => {
    if (pinned.current && box.current) box.current.scrollTop = box.current.scrollHeight
  }, [lines])

  return (
    <div
      className="logs"
      ref={box}
      onScroll={(e) => {
        const b = e.currentTarget
        pinned.current = b.scrollHeight - b.scrollTop - b.clientHeight < 40
      }}
    >
      {lines.length === 0 ? (
        <span className="faint">Waiting for output…</span>
      ) : (
        lines.map((l, i) => (
          <div key={i} className={l.startsWith('==>') ? 'step' : undefined}>
            {l}
          </div>
        ))
      )}
    </div>
  )
}
