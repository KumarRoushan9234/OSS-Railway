import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Icon } from './ui'

export interface Command {
  id: string
  label: string
  icon?: string
  hint?: string
  group?: string
  run: () => void
}

// Modal is a centered dialog over a dimmed page; Escape or a click outside closes it.
export function Modal({ onClose, children, label }: { onClose: () => void; children: ReactNode; label: string }) {
  useEffect(() => {
    const on = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', on)
    return () => window.removeEventListener('keydown', on)
  }, [onClose])
  return (
    <div className="backdrop" onPointerDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="card modal" role="dialog" aria-modal="true" aria-label={label}>
        {children}
      </div>
    </div>
  )
}

// CommandList: a search box over a keyboard-navigable list (arrows + Enter).
export function CommandList({
  commands,
  query,
  onQuery,
  placeholder,
  empty,
}: {
  commands: Command[]
  query: string
  onQuery: (q: string) => void
  placeholder: string
  empty?: ReactNode
}) {
  const [active, setActive] = useState(0)
  const listRef = useRef<HTMLDivElement>(null)

  useEffect(() => setActive(0), [query])
  useEffect(() => {
    listRef.current?.querySelector('.option.active')?.scrollIntoView({ block: 'nearest' })
  }, [active])

  function onKey(e: React.KeyboardEvent) {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setActive((a) => Math.min(a + 1, commands.length - 1))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setActive((a) => Math.max(a - 1, 0))
    } else if (e.key === 'Enter' && commands[active]) {
      e.preventDefault()
      commands[active].run()
    }
  }

  let lastGroup: string | undefined
  return (
    <>
      <input className="modal-search" autoFocus placeholder={placeholder} value={query} onChange={(e) => onQuery(e.target.value)} onKeyDown={onKey} />
      <div className="modal-body" ref={listRef} role="listbox">
        {commands.length === 0 && (empty ?? <p className="faint item">No matches</p>)}
        {commands.map((c, i) => {
          const header = c.group && c.group !== lastGroup ? c.group : null
          lastGroup = c.group
          return (
            <div key={c.id}>
              {header && <div className="group">{header}</div>}
              <button className={`option${i === active ? ' active' : ''}`} role="option" aria-selected={i === active} onMouseMove={() => setActive(i)} onClick={c.run}>
                {c.icon && (
                  <span className="kind">
                    <Icon name={c.icon} />
                  </span>
                )}
                <span className="ellipsis">{c.label}</span>
                {c.hint && <span className="hint">{c.hint}</span>}
              </button>
            </div>
          )
        })}
      </div>
    </>
  )
}

export function matches(label: string, query: string) {
  const q = query.trim().toLowerCase()
  return !q || label.toLowerCase().includes(q)
}
