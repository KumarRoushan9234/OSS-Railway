import { useEffect, useState } from 'react'
import { api } from '../api'

interface Hit {
  id: number
  service: string
  ts: number
  line: string
}

// Highlights each searched word in a line.
function Highlight({ text, words }: { text: string; words: string[] }) {
  if (!words.length) return <>{text}</>
  const re = new RegExp('(' + words.map((w) => w.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|') + ')', 'gi')
  return (
    <>
      {text.split(re).map((part, i) =>
        i % 2 ? (
          <mark key={i} style={{ background: 'var(--accent-soft)', color: 'inherit', borderRadius: 2 }}>
            {part}
          </mark>
        ) : (
          part
        ),
      )}
    </>
  )
}

// Search the stored logs (7 days) of one service, or of every service when serviceId is omitted.
export function LogSearch({ serviceId, query }: { serviceId?: number; query: string }) {
  const [hits, setHits] = useState<Hit[] | null>(null)
  const [more, setMore] = useState(false)
  const [error, setError] = useState('')

  const url = (before?: number) => {
    const p = new URLSearchParams({ q: query })
    if (serviceId) p.set('service', String(serviceId))
    if (before) p.set('before', String(before))
    return '/logs/search?' + p.toString()
  }

  useEffect(() => {
    setError('')
    const t = setTimeout(() => {
      api<Hit[]>(url()).then(
        (r) => {
          setHits(r)
          setMore(r.length === 200)
        },
        (e) => setError(e.message),
      )
    }, 250) // wait for typing to pause
    return () => clearTimeout(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, serviceId])

  const words = query.split(/\s+/).filter(Boolean)
  return (
    <div className="logs" style={{ height: 440 }}>
      {error && <span className="error">{error}</span>}
      {hits?.length === 0 && <span className="faint">{query ? 'No matches in the last 7 days' : 'No logs stored yet'}</span>}
      {hits?.map((h) => (
        <div key={h.id}>
          <span className="faint">{new Date(h.ts).toLocaleString()} </span>
          {!serviceId && <span style={{ color: 'var(--accent)' }}>{h.service} </span>}
          <Highlight text={h.line} words={words} />
        </div>
      ))}
      {more && hits && (
        <button
          className="ghost"
          style={{ marginTop: 8 }}
          onClick={() => api<Hit[]>(url(hits[hits.length - 1].id)).then((r) => (setHits(hits.concat(r)), setMore(r.length === 200)))}
        >
          Older
        </button>
      )}
    </div>
  )
}

// Explain asks the configured AI model what went wrong.
export function Explain({ serviceId, deployment }: { serviceId: number; deployment?: number }) {
  const [answer, setAnswer] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  return (
    <div className="stack" style={{ gap: 8 }}>
      <div>
        <button
          disabled={busy}
          onClick={() => {
            setBusy(true)
            setError('')
            setAnswer('')
            api<{ answer: string; model: string }>(`/services/${serviceId}/explain`, { method: 'POST', body: { deployment: deployment ?? 0 } })
              .then((r) => setAnswer(r.answer + '\n\n— ' + r.model), (e) => setError(e.message))
              .finally(() => setBusy(false))
          }}
        >
          {busy ? 'Thinking…' : 'Explain with AI'}
        </button>
      </div>
      {error && (
        <p className="error">
          {error} {error.includes('Settings') && <a href="#/settings">Open Settings</a>}
        </p>
      )}
      {answer && <div style={{ whiteSpace: 'pre-wrap', padding: 12, background: 'var(--accent-soft)', borderRadius: 8, fontSize: 13, lineHeight: 1.6 }}>{answer}</div>}
    </div>
  )
}
