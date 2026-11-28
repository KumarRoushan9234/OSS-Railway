import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { ago, sourceLabel, type External, type Service } from '../api'
import { usePrefs } from '../prefs'
import { Icon, kindIcon } from './ui'

// Project canvas: services are cards you can drag; dashed lines show which app uses which
// database (solid when both run). Drag from a card's right-edge handle onto another card to link them.

type XY = { x: number; y: number }
type View = XY & { z: number }

const GAP = 72
const MIN_ZOOM = 0.3
const MAX_ZOOM = 2
const VIEW_KEY = 'ossr-view'

function loadView(): View | null {
  try {
    const v = JSON.parse(localStorage.getItem(VIEW_KEY) || 'null')
    return v && typeof v.z === 'number' ? v : null
  } catch {
    return null
  }
}

function saveView(v: View) {
  try {
    localStorage.setItem(VIEW_KEY, JSON.stringify(v))
  } catch {
    // storage blocked: the view resets next visit
  }
}

// layout keeps saved positions and puts new services (still at 0,0) in a grid below them, apps first.
function layout(services: Service[], w: number, h: number): Map<number, XY> {
  const pos = new Map<number, XY>()
  let bottom = -GAP - h
  for (const s of services) {
    if (s.x || s.y) {
      pos.set(s.id, { x: s.x, y: s.y })
      bottom = Math.max(bottom, s.y)
    }
  }
  const fresh = services.filter((s) => !(s.x || s.y)).sort((a, b) => (a.kind === b.kind ? a.id - b.id : a.kind === 'app' ? -1 : 1))
  const top = bottom + h + GAP
  fresh.forEach((s, i) => pos.set(s.id, { x: (i % 3) * (w + GAP), y: top + Math.floor(i / 3) * (h + GAP) }))
  return pos
}

interface Props {
  services: Service[]
  selected: number | null
  onOpen: (id: number) => void
  onBackground: () => void
  onMove: (id: number, at: XY) => void
  onLink: (from: number, to: number) => void
  onNew: () => void
  externals: External[] // containers already on the server that the panel doesn't manage yet
  onAdoptPick: (c: External) => void
}

export function Canvas({ services, selected, onOpen, onBackground, onMove, onLink, onNew, externals, onAdoptPick }: Props) {
  const prefs = usePrefs()
  const [w, h] = prefs.density === 'compact' ? [208, 84] : [248, 104]
  const el = useRef<HTMLDivElement>(null)
  const [view, setView] = useState<View>(() => loadView() ?? { x: 0, y: 0, z: 1 })
  const [moved, setMoved] = useState<Map<number, XY>>(new Map()) // positions not yet confirmed by the server
  const [drag, setDrag] = useState<number | null>(null)
  const [link, setLink] = useState<{ from: number; at: XY; over: number | null } | null>(null)
  const [panning, setPanning] = useState(false)

  const base = useMemo(() => layout(services, w, h), [services, w, h])
  const pos = useCallback((id: number) => moved.get(id) ?? base.get(id) ?? { x: 0, y: 0 }, [moved, base])
  const byName = useMemo(() => new Map(services.map((s) => [s.name, s])), [services])

  // Not-yet-adopted containers: dashed cards below the managed ones, one row group per Compose project.
  const ghosts = useMemo(() => {
    const todo = externals.filter((c) => !c.service_id)
    let y = services.length ? Math.max(...services.map((s) => (base.get(s.id) ?? { y: 0 }).y)) + h + GAP * 1.5 : 0
    const out: { c: External; at: XY; label?: string }[] = []
    const groups = new Map<string, External[]>()
    for (const c of todo) groups.set(c.project, [...(groups.get(c.project) ?? []), c])
    for (const [project, list] of groups) {
      list.forEach((c, i) => out.push({ c, at: { x: (i % 4) * (w + GAP), y: y + 34 + Math.floor(i / 4) * (h + GAP / 2) }, label: i === 0 ? project || 'Other containers' : undefined }))
      y += 34 + Math.ceil(list.length / 4) * (h + GAP / 2) + GAP / 2
    }
    return out
  }, [externals, services, base, w, h])
  const points = useCallback((): XY[] => [...services.map((s) => pos(s.id)), ...ghosts.map((g) => g.at)], [services, pos, ghosts])

  // Drop local overrides once the server returns the saved position.
  useEffect(() => {
    setMoved((m) => {
      if (!m.size) return m
      const next = new Map(m)
      for (const s of services) {
        const p = next.get(s.id)
        if (p && Math.abs(p.x - s.x) < 1 && Math.abs(p.y - s.y) < 1) next.delete(s.id)
      }
      return next.size === m.size ? m : next
    })
  }, [services])

  useEffect(() => saveView(view), [view])

  const fit = useCallback(() => {
    const box = el.current?.getBoundingClientRect()
    const ps = points()
    if (!box || !ps.length) return
    const minX = Math.min(...ps.map((p) => p.x)), maxX = Math.max(...ps.map((p) => p.x)) + w
    const minY = Math.min(...ps.map((p) => p.y)), maxY = Math.max(...ps.map((p) => p.y)) + h
    const pad = box.width < 720 ? 24 : 96
    const z = Math.min(1, Math.max(MIN_ZOOM, Math.min((box.width - pad * 2) / (maxX - minX), (box.height - pad * 2 - 64) / (maxY - minY))))
    setView({ z, x: (box.width - (maxX - minX) * z) / 2 - minX * z, y: (box.height - (maxY - minY) * z) / 2 - minY * z - 24 })
  }, [points, w, h])

  // Frame all services on first load, and whenever a resize or a saved view leaves none on screen.
  const fitRef = useRef(fit)
  fitRef.current = fit
  const viewRef = useRef(view)
  viewRef.current = view
  const noneVisible = useCallback(() => {
    const box = el.current?.getBoundingClientRect()
    if (!box) return false
    const v = viewRef.current
    return !points().some((p) => {
      const x = p.x * v.z + v.x, y = p.y * v.z + v.y
      return x + w * v.z > 0 && x < box.width && y + h * v.z > 0 && y < box.height
    })
  }, [points, w, h])
  const checked = useRef(false)
  useLayoutEffect(() => {
    if (!checked.current && (services.length || ghosts.length)) {
      checked.current = true
      if (noneVisible()) fit()
    }
  }, [services.length, ghosts.length, noneVisible, fit])
  // The list of existing containers loads after the services: re-frame once if those cards land off-screen.
  const ghostsFramed = useRef(false)
  useEffect(() => {
    if (ghostsFramed.current || !ghosts.length) return
    ghostsFramed.current = true
    const box = el.current?.getBoundingClientRect()
    const v = viewRef.current
    const offscreen = box && ghosts.some((g) => g.at.y * v.z + v.y + h * v.z > box.height || g.at.x * v.z + v.x + w * v.z > box.width)
    if (offscreen) fitRef.current()
  }, [ghosts, w, h])
  useEffect(() => {
    const onResize = () => noneVisible() && fitRef.current()
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [noneVisible])

  const toWorld = useCallback(
    (clientX: number, clientY: number): XY => {
      const r = el.current!.getBoundingClientRect()
      return { x: (clientX - r.left - view.x) / view.z, y: (clientY - r.top - view.y) / view.z }
    },
    [view],
  )

  const zoomAt = useCallback((factor: number, cx?: number, cy?: number) => {
    const r = el.current!.getBoundingClientRect()
    const px = cx === undefined ? r.width / 2 : cx - r.left
    const py = cy === undefined ? r.height / 2 : cy - r.top
    setView((v) => {
      const z = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, v.z * factor))
      return { z, x: px - ((px - v.x) * z) / v.z, y: py - ((py - v.y) * z) / v.z }
    })
  }, [])

  // Wheel pans; Ctrl/Cmd + wheel (and trackpad pinch) zooms. Needs a non-passive listener.
  useEffect(() => {
    const node = el.current!
    const onWheel = (e: WheelEvent) => {
      e.preventDefault()
      if (e.ctrlKey || e.metaKey) zoomAt(Math.exp(-e.deltaY * 0.01), e.clientX, e.clientY)
      else setView((v) => ({ ...v, x: v.x - e.deltaX, y: v.y - e.deltaY }))
    }
    node.addEventListener('wheel', onWheel, { passive: false })
    return () => node.removeEventListener('wheel', onWheel)
  }, [zoomAt])

  // One pointer gesture at a time: pan the background, drag a card, or draw a link.
  function track(e: React.PointerEvent, move: (dx: number, dy: number, ev: PointerEvent) => void, up: (movedFar: boolean, ev: PointerEvent) => void) {
    const sx = e.clientX, sy = e.clientY
    let far = false
    const onMove = (ev: PointerEvent) => {
      if (!far && Math.hypot(ev.clientX - sx, ev.clientY - sy) > 4) far = true
      if (far) move(ev.clientX - sx, ev.clientY - sy, ev)
    }
    const onUp = (ev: PointerEvent) => {
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onUp)
      window.removeEventListener('pointercancel', onUp)
      up(far, ev)
    }
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
    window.addEventListener('pointercancel', onUp)
  }

  function startPan(e: React.PointerEvent) {
    if (e.button !== 0 || (e.target as HTMLElement).closest('[data-node], [data-ghost]')) return
    const start = view
    setPanning(true)
    track(
      e,
      (dx, dy) => setView({ ...start, x: start.x + dx, y: start.y + dy }),
      (far) => {
        setPanning(false)
        if (!far) onBackground()
      },
    )
  }

  function startDrag(e: React.PointerEvent, s: Service) {
    if (e.button !== 0) return
    e.stopPropagation()
    const start = pos(s.id)
    let last = start
    track(
      e,
      (dx, dy) => {
        setDrag(s.id)
        last = { x: Math.round(start.x + dx / view.z), y: Math.round(start.y + dy / view.z) }
        setMoved((m) => new Map(m).set(s.id, last))
      },
      (far) => {
        setDrag(null)
        if (!far) return onOpen(s.id)
        // Pin every auto-placed card too, so the grid doesn't reflow around the one just moved.
        for (const o of services) if (o.id !== s.id && !o.x && !o.y && !moved.has(o.id)) onMove(o.id, pos(o.id))
        onMove(s.id, last.x || last.y ? last : { x: 1, y: 1 })
      },
    )
  }

  function startLink(e: React.PointerEvent, s: Service) {
    e.stopPropagation()
    const target = (ev: PointerEvent) => {
      const n = (document.elementFromPoint(ev.clientX, ev.clientY) as HTMLElement | null)?.closest<HTMLElement>('[data-node]')
      const id = n ? Number(n.dataset.node) : null
      return id === s.id ? null : id
    }
    setLink({ from: s.id, at: toWorld(e.clientX, e.clientY), over: null })
    track(
      e,
      (_dx, _dy, ev) => setLink({ from: s.id, at: toWorld(ev.clientX, ev.clientY), over: target(ev) }),
      (_far, ev) => {
        setLink(null)
        const to = target(ev)
        if (to !== null) onLink(s.id, to)
      },
    )
  }

  function edge(a: XY, b: XY) {
    // Cards mostly above each other: join bottom to top instead of side to side.
    if (Math.abs(b.x - a.x) < w * 0.75) {
      const down = b.y >= a.y
      const y1 = down ? a.y + h : a.y, y2 = down ? b.y : b.y + h
      const c = Math.max(30, Math.abs(y2 - y1) / 2) * (down ? 1 : -1)
      return `M${a.x + w / 2},${y1} C${a.x + w / 2},${y1 + c} ${b.x + w / 2},${y2 - c} ${b.x + w / 2},${y2}`
    }
    const toRight = b.x + w / 2 >= a.x + w / 2
    const x1 = toRight ? a.x + w : a.x, y1 = a.y + h / 2
    const x2 = toRight ? b.x : b.x + w, y2 = b.y + h / 2
    const c = Math.max(40, Math.abs(x2 - x1) / 2) * (toRight ? 1 : -1)
    return `M${x1},${y1} C${x1 + c},${y1} ${x2 - c},${y2} ${x2},${y2}`
  }

  const gridSize = 20 * view.z
  return (
    <>
      <div
        ref={el}
        className={`canvas${prefs.grid ? ' grid' : ''}${panning ? ' panning' : ''}${link ? ' linking' : ''}`}
        style={prefs.grid ? { backgroundSize: `${gridSize}px ${gridSize}px`, backgroundPosition: `${view.x}px ${view.y}px` } : undefined}
        onPointerDown={startPan}
      >
        <div className="world" style={{ transform: `translate(${view.x}px, ${view.y}px) scale(${view.z})` }}>
          <svg className="edges" width="1" height="1">
            {services.flatMap((s) =>
              s.uses.map((name) => {
                const t = byName.get(name)
                if (!t) return null
                const live = s.status === 'running' && t.status === 'running'
                return <path key={s.id + '-' + t.id} className={`edge${live ? ' live' : ''}`} d={edge(pos(t.id), pos(s.id))} />
              }),
            )}
            {link && (
              <path
                className="edge live"
                d={(() => {
                  const a = pos(link.from)
                  return `M${a.x + w},${a.y + h / 2} L${link.at.x},${link.at.y}`
                })()}
              />
            )}
          </svg>
          {services.map((s) => {
            const p = pos(s.id)
            const detail = s.repo ? s.repo.replace(/^https:\/\/(www\.)?github\.com\//, '') : s.image
            return (
              <div
                key={s.id}
                data-node={s.id}
                className={`card node${selected === s.id ? ' selected' : ''}${drag === s.id ? ' dragging' : ''}${link?.over === s.id ? ' drop' : ''}`}
                style={{ left: p.x, top: p.y }}
                onPointerDown={(e) => startDrag(e, s)}
                onKeyDown={(e) => e.key === 'Enter' && onOpen(s.id)}
                tabIndex={0}
                role="button"
                aria-label={`${s.name}, ${s.status}`}
              >
                <div className="row">
                  <span className="kind">
                    <Icon name={kindIcon(s.source)} />
                  </span>
                  <h3 className="ellipsis" style={{ flex: 1 }}>
                    {s.name}
                  </h3>
                  <span className={`dot ${s.status}`} title={s.status} />
                </div>
                <div className="meta">
                  <div className="muted ellipsis">
                    {sourceLabel[s.source]}
                    {detail ? ' · ' + detail : ''}
                  </div>
                  {prefs.density === 'comfortable' && (
                    <div className="faint ellipsis">
                      {s.urls[0]?.replace(/^https?:\/\//, '') || s.status}
                      {s.since ? ' · ' + ago(s.since) : ''}
                    </div>
                  )}
                </div>
                <span className="port" onPointerDown={(e) => startLink(e, s)} title="Drag onto another service to link" />
              </div>
            )
          })}
          {ghosts.map(({ c, at, label }) => (
            <div key={c.name}>
              {label && (
                <div className="ghost-label" style={{ left: at.x, top: at.y - 30 }}>
                  {label === 'Other containers' ? label : 'Compose · ' + label} <span className="faint">· already on this server</span>
                </div>
              )}
              <button
                data-ghost={c.name}
                className="card node ghost"
                style={{ left: at.x, top: at.y }}
                onClick={() => onAdoptPick(c)}
                aria-label={`${c.name}, not managed yet. Adopt`}
              >
                <div className="row">
                  <span className="kind">
                    <Icon name="box" />
                  </span>
                  <h3 className="ellipsis" style={{ flex: 1, textAlign: 'left' }}>
                    {c.name}
                  </h3>
                  <span className={`dot ${c.state === 'running' ? 'running' : 'failed'}`} title={c.status} />
                </div>
                <div className="meta" style={{ textAlign: 'left' }}>
                  <div className="muted ellipsis">{c.image}</div>
                  {prefs.density === 'comfortable' && <div className="faint ellipsis">Not managed · click to adopt</div>}
                </div>
              </button>
            </div>
          ))}
        </div>
        {services.length === 0 && ghosts.length === 0 && (
          <div className="empty-canvas">
            <div className="stack" style={{ justifyItems: 'center', gap: 12 }}>
              <button className="card" onClick={onNew}>
                <Icon name="plus" size={24} />
                Deploy a repo, image or database
              </button>
              <a className="button ghost" href="#/server" style={{ pointerEvents: 'auto' }}>
                <Icon name="server" /> See what's already running on this server
              </a>
            </div>
          </div>
        )}
      </div>
      <div className="card zoom">
        <button className="ghost icon" onClick={() => zoomAt(1 / 1.2)} aria-label="Zoom out">
          <Icon name="minus" />
        </button>
        <button className="ghost" onClick={() => setView((v) => ({ ...v, z: 1 }))} aria-label="Reset zoom" style={{ minWidth: 52 }}>
          {Math.round(view.z * 100)}%
        </button>
        <button className="ghost icon" onClick={() => zoomAt(1.2)} aria-label="Zoom in">
          <Icon name="plus" />
        </button>
        <button className="ghost icon" onClick={fit} aria-label="Fit all services">
          <Icon name="fit" />
        </button>
      </div>
    </>
  )
}
