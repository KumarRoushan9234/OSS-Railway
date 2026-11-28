import { useEffect, useState } from 'react'
import { ago, api, bytes, duration, formatEnv, parseEnv, sourceLabel, urlKey, type Deployment, type Point, type Service } from '../api'
import { go, useFlash, usePoll } from '../hooks'
import { Icon, kindIcon, Logs, Sparkline } from '../components/ui'
import { Explain, LogSearch } from '../components/LogSearch'

interface Backup {
  id: number
  file: string
  size: number
  status: string
  created: number
}

const active = ['queued', 'building', 'deploying']

// Side panel for one service, over the canvas. Tabs live in the URL: #/s/12/logs
export function ServicePanel({ service: s, services, tab, first, onChange }: { service: Service; services: Service[]; tab: string; first: boolean; onChange: () => void }) {
  const [error, setError] = useState('')
  const [openLog, setOpenLog] = useState<number | null>(null)
  const [deployments, reloadDeployments] = usePoll(() => api<Deployment[]>(`/services/${s.id}/deployments`), 4000, [s.id])
  const isDb = s.kind === 'database'
  const isExt = s.source === 'external'
  const tabs = isExt ? ['logs', 'metrics', 'settings'] : ['deployments', 'logs', 'variables', 'metrics', ...(s.source === 'postgres' || s.source === 'mysql' ? ['backups'] : []), 'settings']
  // An adopted container has no deployments: open on its logs.
  if (isExt && (tab === 'deployments' || tab === 'variables' || tab === 'backups')) tab = 'logs'

  // Open the log of a running deploy automatically.
  useEffect(() => {
    const running = deployments?.find((d) => active.includes(d.status))
    if (running && openLog === null) setOpenLog(running.id)
  }, [deployments, openLog])

  async function act(fn: () => Promise<unknown>) {
    setError('')
    try {
      await fn()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const deploy = () =>
    act(async () => {
      const { deployment } = await api<{ deployment: number }>(`/services/${s.id}/deploy`, { method: 'POST' })
      setOpenLog(deployment)
      go(`/s/${s.id}/deployments`)
      reloadDeployments()
      onChange()
    })

  return (
    <aside className="card drawer" aria-label={s.name}>
      <div className="drawer-head">
        <div className="spread" style={{ alignItems: 'flex-start' }}>
          <div className="stack" style={{ gap: 4, minWidth: 0 }}>
            <div className="row">
              <span className="faint">
                <Icon name={kindIcon(s.source)} />
              </span>
              <h1 className="ellipsis">{s.name}</h1>
              <span className={`dot ${s.status}`} title={s.status} />
            </div>
            {s.urls.map((u) => (
              <a key={u} className="row ellipsis" style={{ color: 'var(--accent)', fontSize: 13, gap: 4 }} href={u} target="_blank" rel="noreferrer">
                {u.replace(/^https?:\/\//, '')} <Icon name="external" size={12} />
              </a>
            ))}
          </div>
          <div className="row">
            {s.status === 'running' && (
              <button className={isDb || isExt ? undefined : 'hide-sm'} onClick={() => act(() => api(`/services/${s.id}/restart`, { method: 'POST' }).then(onChange))}>
                Restart
              </button>
            )}
            {s.status === 'running' && (isExt || isDb) && (
              <button onClick={() => act(() => api(`/services/${s.id}/stop`, { method: 'POST' }).then(onChange))}>Stop</button>
            )}
            {isExt && s.status !== 'running' && (
              <button className="primary" onClick={() => act(() => api(`/services/${s.id}/start`, { method: 'POST' }).then(onChange))}>
                Start
              </button>
            )}
            {!isExt && !(isDb && s.status === 'running') && (
              <button className="primary" onClick={deploy}>
                {isDb ? 'Start' : 'Deploy'}
              </button>
            )}
            <button className="ghost icon" onClick={() => go('/')} aria-label="Close">
              <Icon name="x" />
            </button>
          </div>
        </div>
        <nav className="tabs">
          {tabs.map((t) => (
            <a key={t} href={`#/s/${s.id}/${t}`} className={tab === t ? 'active' : undefined}>
              {t[0].toUpperCase() + t.slice(1)}
            </a>
          ))}
        </nav>
      </div>
      <div className="drawer-body">
        {error && <p className="error">{error}</p>}
        {tab === 'deployments' && (
          <div className="card list">
            {deployments?.length === 0 && (
              <div style={{ padding: 32, display: 'grid', placeItems: 'center' }}>
                <button className="primary" onClick={deploy}>
                  Deploy now
                </button>
              </div>
            )}
            {deployments?.map((d) => (
              <div key={d.id} style={{ borderTop: d === deployments[0] ? 0 : '1px solid var(--border)' }}>
                <button className="ghost" style={{ width: '100%', height: 44, justifyContent: 'space-between', fontWeight: 400 }} onClick={() => setOpenLog(openLog === d.id ? -1 : d.id)}>
                  <span className="row" style={{ minWidth: 0, flex: 1 }}>
                    <span className={`dot ${d.status}`} />
                    <span style={{ minWidth: 72, textAlign: 'left' }}>{d.status}</span>
                    {d.commit && <code className="faint">{d.commit}</code>}
                    <span className="muted ellipsis hide-sm">{d.message}</span>
                  </span>
                  <span className="faint">
                    {duration(d.created, d.finished)} {ago(d.created)}
                  </span>
                </button>
                {openLog === d.id && (
                  <div className="stack" style={{ padding: '0 12px 12px' }}>
                    <Logs src={`/api/deployments/${d.id}/logs`} />
                    {d.status === 'failed' && <Explain serviceId={s.id} deployment={d.id} />}
                    {d.image && d.status === 'replaced' && (
                      <div>
                        <button
                          onClick={() =>
                            act(async () => {
                              const { deployment } = await api<{ deployment: number }>(`/deployments/${d.id}/rollback`, { method: 'POST' })
                              setOpenLog(deployment)
                              reloadDeployments()
                            })
                          }
                        >
                          Redeploy this version
                        </button>
                      </div>
                    )}
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
        {tab === 'logs' && <ServiceLogs s={s} />}
        {tab === 'variables' && <Variables s={s} services={services} first={first && deployments?.length === 0} onDeploy={deploy} onError={setError} onChange={onChange} />}
        {tab === 'metrics' && <Metrics s={s} />}
        {tab === 'backups' && <Backups s={s} onError={setError} />}
        {tab === 'settings' && <Settings s={s} onError={setError} onChange={onChange} />}
      </div>
    </aside>
  )
}

function Variables({ s, services, first, onDeploy, onError, onChange }: { s: Service; services: Service[]; first: boolean; onDeploy: () => void; onError: (e: string) => void; onChange: () => void }) {
  const [raw, setRaw] = useState<string | null>(null)
  const [saved, flash] = useFlash()
  useEffect(() => {
    api<Record<string, string>>(`/services/${s.id}/env`).then((v) => setRaw(formatEnv(v)), (e) => onError(e.message))
  }, [s.id, onError])
  const refs = s.kind === 'database' ? [] : services.filter((x) => x.kind === 'database' && x.id !== s.id).map((x) => ({ key: urlKey(x.source), ref: `\${{${x.name}.${urlKey(x.source)}}}` }))

  async function save(andDeploy: boolean) {
    onError('')
    try {
      await api(`/services/${s.id}/env`, { method: 'PUT', body: parseEnv(raw ?? '') })
      flash('saved')
      onChange()
      if (andDeploy) onDeploy()
    } catch (e) {
      onError((e as Error).message)
    }
  }

  if (raw === null) return null
  return (
    <div className="stack">
      <textarea rows={14} value={raw} onChange={(e) => setRaw(e.target.value)} spellCheck={false} placeholder="KEY=value" aria-label="Variables" />
      {refs.length > 0 && (
        <div className="row wrap">
          {refs.map((r) => (
            <button key={r.ref} className="ghost" style={{ height: 28 }} onClick={() => setRaw((raw.trim() ? raw.trim() + '\n' : '') + `${r.key}=${r.ref}`)}>
              <code>{r.ref}</code>
            </button>
          ))}
        </div>
      )}
      <div className="row">
        {first ? (
          <button className="primary" onClick={() => save(true)}>
            Save and deploy
          </button>
        ) : (
          <>
            <button className="primary" onClick={() => save(false)}>
              {saved ? 'Saved' : 'Save'}
            </button>
            {s.kind !== 'database' && <button onClick={() => save(true)}>Save and redeploy</button>}
          </>
        )}
      </div>
    </div>
  )
}

function Metrics({ s }: { s: Service }) {
  const [range, setRange] = useState('1h')
  const [points] = usePoll(() => api<Point[]>(`/services/${s.id}/metrics?range=${range}`), 15000, [s.id, range])
  const last = points?.[points.length - 1]
  const charts: [string, (p: Point) => number, (v: number) => string][] = [
    ['CPU', (p) => p.cpu, (v) => v.toFixed(1) + '%'],
    ['Memory', (p) => p.mem, bytes],
    ['Network in', (p) => p.net_rx, (v) => bytes(v) + '/s'],
    ['Network out', (p) => p.net_tx, (v) => bytes(v) + '/s'],
  ]
  return (
    <div className="stack">
      <div className="segmented" style={{ justifySelf: 'start' }}>
        {['1h', '24h', '7d'].map((r) => (
          <button key={r} className={range === r ? 'on' : undefined} onClick={() => setRange(r)}>
            {r}
          </button>
        ))}
      </div>
      {!points || points.length < 2 || !last ? (
        <p className="muted">Collecting data…</p>
      ) : (
        <div className="charts">
          {charts.map(([label, get, fmt]) => (
            <div key={label} className="card chart">
              <div className="spread">
                <span className="muted">{label}</span>
                <span>{fmt(get(last))}</span>
              </div>
              <Sparkline values={points.map(get)} height={110} />
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function Backups({ s, onError }: { s: Service; onError: (e: string) => void }) {
  const [backups, reload] = usePoll(() => api<Backup[]>(`/services/${s.id}/backups`), 30000, [s.id])
  const [busy, setBusy] = useState(false)
  return (
    <div className="stack">
      <div>
        <button
          className="primary"
          disabled={busy}
          onClick={async () => {
            setBusy(true)
            onError('')
            try {
              await api(`/services/${s.id}/backups`, { method: 'POST' })
              reload()
            } catch (e) {
              onError((e as Error).message)
            } finally {
              setBusy(false)
            }
          }}
        >
          {busy ? 'Backing up…' : 'Back up now'}
        </button>
      </div>
      <div className="card list">
        {backups?.length === 0 && <p className="muted item">Daily backups run at 03:00.</p>}
        {backups?.map((b) => (
          <div key={b.id} className="spread item">
            <span className="row ellipsis">
              <span className={`dot ${b.status === 'ok' ? 'ok' : 'failed'}`} />
              <code className="ellipsis">{b.file}</code>
            </span>
            <span className="row">
              <span className="faint hide-sm">
                {bytes(b.size)} · {ago(b.created)}
              </span>
              {b.status === 'ok' && (
                <a className="button" href={`/api/backups/${b.id}/download`}>
                  Download
                </a>
              )}
            </span>
          </div>
        ))}
      </div>
    </div>
  )
}

// Live tail, or search through the last 7 days of stored logs.
function ServiceLogs({ s }: { s: Service }) {
  const [q, setQ] = useState('')
  const [mode, setMode] = useState<'live' | 'search'>('live')
  return (
    <div className="stack" style={{ gap: 8 }}>
      <div className="row">
        <div className="segmented">
          <button className={mode === 'live' ? 'on' : undefined} onClick={() => setMode('live')}>
            Live
          </button>
          <button className={mode === 'search' ? 'on' : undefined} onClick={() => setMode('search')}>
            Search
          </button>
        </div>
        {mode === 'search' && <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="error timeout 500" aria-label="Search logs" autoFocus style={{ flex: 1 }} />}
      </div>
      {mode === 'live' ? <Logs src={`/api/services/${s.id}/logs`} /> : <LogSearch serviceId={s.id} query={q} />}
      <Explain serviceId={s.id} />
    </div>
  )
}

interface Resources {
  usage: { samples: number; cpu_avg: number; cpu_peak: number; mem_avg: number; mem_peak: number }
  disk: string
  suggestion: { cpu: number; memory: number; warnings: string[] }
}

// What the service actually used in the last 24 h, and limits sized from that.
function Usage({ s, onApply }: { s: Service; onApply: (cpu: number, memory: number) => void }) {
  const [r] = usePoll(() => api<Resources>(`/services/${s.id}/resources`), 60000, [s.id])
  if (!r) return null
  const { usage: u, suggestion: sug } = r
  return (
    <div className="stack" style={{ gap: 8, padding: 12, background: 'var(--sunken)', borderRadius: 8, fontSize: 13 }}>
      <div className="row wrap" style={{ gap: 16 }}>
        <span>
          <span className="muted">CPU </span>
          {(u.cpu_avg / 100).toFixed(2)} avg · {(u.cpu_peak / 100).toFixed(2)} peak cores
        </span>
        <span>
          <span className="muted">Memory </span>
          {bytes(u.mem_avg)} avg · {bytes(u.mem_peak)} peak
        </span>
        {r.disk && (
          <span>
            <span className="muted">Disk </span>
            {r.disk.split(' ')[0]}
          </span>
        )}
      </div>
      {sug.warnings.map((w) => (
        <span key={w} style={{ color: 'var(--warn)' }}>
          {w}
        </span>
      ))}
      {sug.memory ? (
        <div className="spread">
          <span>
            Suggested: <b>{sug.cpu} cores</b>, <b>{sug.memory} MB</b>
          </span>
          <button type="button" onClick={() => onApply(sug.cpu, sug.memory)}>
            Use
          </button>
        </div>
      ) : (
        <span className="faint">Suggestions appear after a few minutes of data.</span>
      )}
    </div>
  )
}

function Settings({ s, onError, onChange }: { s: Service; onError: (e: string) => void; onChange: () => void }) {
  const [form, setForm] = useState(() => ({
    branch: s.branch,
    root_dir: s.root_dir,
    port: s.port ? String(s.port) : '',
    domain: s.domain,
    image: s.image,
    autodeploy: s.autodeploy,
    cpu: s.cpu ? String(s.cpu) : '',
    memory: s.memory ? String(s.memory) : '',
  }))
  const [confirm, setConfirm] = useState('')
  const [saved, flash] = useFlash()
  const isDb = s.kind === 'database'
  const isExt = s.source === 'external'
  const isGit = s.source === 'github' || s.source === 'git'
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) => setForm({ ...form, [k]: e.target.type === 'checkbox' ? e.target.checked : e.target.value })

  async function save(e: React.FormEvent) {
    e.preventDefault()
    onError('')
    const body: Record<string, unknown> = { cpu: Number(form.cpu) || 0, memory: Number(form.memory) || 0 }
    if (!isDb && !isExt) Object.assign(body, { port: Number(form.port) || 0, domain: form.domain })
    if (isGit) Object.assign(body, { branch: form.branch, root_dir: form.root_dir })
    if (s.source === 'github') body.autodeploy = form.autodeploy
    if (s.source === 'image') body.image = form.image
    try {
      await api(`/services/${s.id}`, { method: 'PATCH', body })
      flash('saved')
      onChange()
    } catch (err) {
      onError((err as Error).message)
    }
  }

  return (
    <>
      <form className="card panel stack" onSubmit={save}>
        <h2>Source</h2>
        <p className="muted">
          {sourceLabel[s.source]}
          {s.repo ? ' · ' + s.repo : ''}
          {isExt && <> container <code>{s.image}</code></>}
        </p>
        {isExt && (
          <p className="muted" style={{ fontSize: 13 }}>
            This container was created outside OSS Railway. Its image, variables, ports and volumes stay as they are; update it the way it was created (for example <code>docker compose up -d</code>).
          </p>
        )}
        {isGit && (
          <div className="two">
            <label>
              Branch <input value={form.branch} onChange={set('branch')} />
            </label>
            <label>
              Root directory <input value={form.root_dir} onChange={set('root_dir')} placeholder="/" />
            </label>
          </div>
        )}
        {s.source === 'image' && (
          <label>
            Image <input value={form.image} onChange={set('image')} />
          </label>
        )}
        {s.source === 'github' && (
          <label className="check">
            <input type="checkbox" checked={form.autodeploy} onChange={set('autodeploy')} /> Deploy on push
          </label>
        )}
        {!isDb && !isExt && (
          <>
            <h2>Networking</h2>
            <div className="two">
              <label>
                Port <input type="number" min={0} max={65535} value={form.port} onChange={set('port')} placeholder="auto" />
              </label>
              <label>
                Custom domain <input value={form.domain} onChange={set('domain')} placeholder="app.example.com" />
              </label>
            </div>
          </>
        )}
        <h2>Resources</h2>
        <div className="two">
          <label>
            CPU limit (cores) <input type="number" min={0} max={64} step={0.25} value={form.cpu} onChange={set('cpu')} placeholder="No limit" />
          </label>
          <label>
            Memory limit (MB) <input type="number" min={0} step={64} value={form.memory} onChange={set('memory')} placeholder="No limit" />
          </label>
        </div>
        <Usage s={s} onApply={(cpu, memory) => setForm((f) => ({ ...f, cpu: String(cpu), memory: String(memory) }))} />
        <div>
          <button className="primary">{saved ? 'Saved' : 'Save'}</button>
        </div>
      </form>
      {isExt ? (
        <div className="card panel stack">
          <h2>Stop managing {s.name}</h2>
          <p className="muted" style={{ fontSize: 13 }}>
            OSS Railway forgets it. The container keeps running exactly as it is.
          </p>
          <div>
            <button
              onClick={async () => {
                await api(`/services/${s.id}`, { method: 'DELETE' })
                onChange()
                go('/')
              }}
            >
              Stop managing
            </button>
          </div>
        </div>
      ) : (
      <div className="card panel stack">
        <h2 style={{ color: 'var(--danger)' }}>Delete {s.name}</h2>
        <label>
          <span>
            Type <code>{s.name}</code> to confirm
          </span>
          <input value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="off" />
        </label>
        <div>
          <button
            className="danger"
            disabled={confirm !== s.name}
            onClick={async () => {
              await api(`/services/${s.id}`, { method: 'DELETE' })
              onChange()
              go('/')
            }}
          >
            Delete service{isDb ? ' and its data' : ''}
          </button>
        </div>
      </div>
      )}
    </>
  )
}
