import { useState } from 'react'
import { api, type External, type Service } from '../api'
import { go } from '../hooks'
import { Modal } from '../components/Command'

// Asks before the panel takes over an existing container. Adopting changes nothing about it.
export function AdoptDialog({ container: c, all, onClose, onDone }: { container: External; all: External[]; onClose: () => void; onDone: () => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const siblings = c.project ? all.filter((x) => x.project === c.project && !x.service_id) : []

  async function adopt(body: { containers?: string[]; project?: string }) {
    setBusy(true)
    setError('')
    try {
      const adopted = await api<Service[]>('/adopt', { method: 'POST', body })
      onDone()
      onClose()
      if (adopted.length === 1) go(`/s/${adopted[0].id}`)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal onClose={onClose} label={`Adopt ${c.name}`}>
      <div className="modal-form">
        <div className="stack" style={{ gap: 4 }}>
          <h2>Adopt {c.name}?</h2>
          <span className="muted" style={{ fontSize: 13 }}>
            {c.image} · {c.status}
            {c.project && <> · Compose project <b>{c.project}</b></>}
          </span>
        </div>
        <ul className="muted" style={{ margin: 0, paddingLeft: 18, display: 'grid', gap: 4, fontSize: 14 }}>
          <li>Nothing about the container changes. It keeps its image, variables, ports and volumes.</li>
          <li>You get it on the canvas with logs, metrics and crash alerts, plus Restart, Stop, Start and CPU/memory limits.</li>
          <li>OSS Railway never rebuilds or recreates it. Update it the way you created it ({c.project ? 'docker compose' : 'docker run'}).</li>
          <li>"Stop managing" hands it back untouched at any time.</li>
        </ul>
        {error && <p className="error">{error}</p>}
        <div className="row wrap">
          <button className="primary" disabled={busy} onClick={() => adopt({ containers: [c.name] })}>
            Adopt {c.name}
          </button>
          {siblings.length > 1 && (
            <button disabled={busy} onClick={() => adopt({ project: c.project })}>
              Adopt all {siblings.length} in {c.project}
            </button>
          )}
          <button className="ghost" onClick={onClose}>
            Not now
          </button>
        </div>
      </div>
    </Modal>
  )
}
