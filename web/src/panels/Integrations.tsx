import { useCallback, useEffect, useState } from 'react'
import { api } from '../api'

type Settings = Record<string, string | boolean>

const presets = [
  { label: 'Ollama on this computer', url: 'http://host.docker.internal:11434/v1', model: 'qwen2.5:7b' },
  { label: 'OpenAI', url: 'https://api.openai.com/v1', model: 'gpt-5-mini' },
]

// AI analysis: any OpenAI-compatible chat endpoint. Off until a URL and model are set.
export function AISettings() {
  const [s, setS] = useState<Settings | null>(null)
  const [key, setKey] = useState('')
  const [status, setStatus] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api<Settings>('/settings').then(setS, (e) => setError(e.message))
  }, [])
  if (!s) return null
  const str = (k: string) => String(s[k] ?? '')

  async function save(test: boolean) {
    setError('')
    setStatus('')
    setBusy(true)
    try {
      const body: Record<string, string> = { ai_url: str('ai_url'), ai_model: str('ai_model') }
      if (key) body.ai_key = key
      setS(await api<Settings>('/settings', { method: 'PUT', body }))
      setKey('')
      if (test) {
        const r = await api<{ reply: string; seconds: number }>('/ai/test', { method: 'POST' })
        setStatus(`Answered "${r.reply.slice(0, 40)}" in ${r.seconds.toFixed(1)} s`)
      } else setStatus('Saved')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <form
      className="card panel stack"
      onSubmit={(e) => {
        e.preventDefault()
        save(true)
      }}
    >
      <h2>AI analysis</h2>
      <p className="muted" style={{ fontSize: 13 }}>
        Adds "Explain" to failed deploys and logs. Use Ollama to keep logs on your own machines; a hosted API receives the logs you ask about.
      </p>
      <div className="row wrap">
        {presets.map((p) => (
          <button key={p.label} type="button" className="ghost" style={{ border: '1px solid var(--border)' }} onClick={() => setS({ ...s, ai_url: p.url, ai_model: p.model })}>
            {p.label}
          </button>
        ))}
      </div>
      <div className="two">
        <label>
          Endpoint <input value={str('ai_url')} onChange={(e) => setS({ ...s, ai_url: e.target.value })} placeholder="http://host.docker.internal:11434/v1" />
        </label>
        <label>
          Model <input value={str('ai_model')} onChange={(e) => setS({ ...s, ai_model: e.target.value })} placeholder="qwen2.5:7b" />
        </label>
      </div>
      <label>
        API key <input type="password" value={key} onChange={(e) => setKey(e.target.value)} placeholder={s.ai_key_set ? '••••••••' : 'Not needed for Ollama'} autoComplete="off" />
      </label>
      {error && <p className="error">{error}</p>}
      {status && <p style={{ color: 'var(--accent)', fontSize: 13 }}>{status}</p>}
      <div className="row">
        <button className="primary" disabled={busy}>
          {busy ? 'Testing…' : 'Save and test'}
        </button>
        <button type="button" disabled={busy} onClick={() => save(false)}>
          Save
        </button>
      </div>
    </form>
  )
}

interface Tunnel {
  running: boolean
  mode: string
  url: string
}

// Remote access through a Cloudflare Tunnel: no public IP or open ports needed.
export function RemoteAccess() {
  const [t, setT] = useState<Tunnel | null>(null)
  const [token, setToken] = useState('')
  const [host, setHost] = useState('')
  const [own, setOwn] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(() => {
    api<Tunnel>('/tunnel').then(setT, (e) => setError(e.message))
  }, [])
  useEffect(load, [load])

  async function start(body: object) {
    setBusy(true)
    setError('')
    try {
      setT(await api<Tunnel>('/tunnel', { method: 'POST', body }))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  if (!t) return null
  return (
    <section className="card panel stack">
      <div className="spread">
        <h2>Remote access</h2>
        <span className="row muted" style={{ fontSize: 13 }}>
          <span className={`dot ${t.running ? 'ok' : ''}`} />
          {t.running ? 'On' : 'Off'}
        </span>
      </div>
      <p className="muted" style={{ fontSize: 13 }}>
        Reach this dashboard from anywhere through a Cloudflare Tunnel, even with no public IP or open ports. Anyone can open the address, so keep 2FA or a passkey on.
      </p>
      {error && <p className="error">{error}</p>}
      {t.running ? (
        <>
          {t.url ? (
            <a href={t.url} target="_blank" rel="noreferrer" style={{ color: 'var(--accent)', wordBreak: 'break-all' }}>
              {t.url}
            </a>
          ) : (
            <span className="muted">Connecting…</span>
          )}
          <div>
            <button className="danger" onClick={() => api('/tunnel', { method: 'DELETE' }).then(load, (e) => setError(e.message))}>
              Turn off
            </button>
          </div>
        </>
      ) : own ? (
        <form
          className="stack"
          onSubmit={(e) => {
            e.preventDefault()
            start({ mode: 'token', token, host })
          }}
        >
          <label>
            Tunnel token <input type="password" value={token} onChange={(e) => setToken(e.target.value)} placeholder="From Cloudflare → Zero Trust → Tunnels" autoComplete="off" required />
          </label>
          <label>
            Its public hostname <input value={host} onChange={(e) => setHost(e.target.value)} placeholder="panel.example.com" />
          </label>
          <div className="row">
            <button className="primary" disabled={busy}>
              Connect
            </button>
            <button type="button" className="ghost" onClick={() => setOwn(false)}>
              Back
            </button>
          </div>
        </form>
      ) : (
        <div className="row wrap">
          <button className="primary" disabled={busy} onClick={() => start({ mode: 'quick' })}>
            {busy ? 'Starting…' : 'Get a temporary address'}
          </button>
          <button onClick={() => setOwn(true)}>Use my Cloudflare tunnel</button>
        </div>
      )}
    </section>
  )
}
