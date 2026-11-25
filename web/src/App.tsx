import { useCallback, useEffect, useState } from 'react'
import { ago, api, bytes, linkServices, type Me, type Overview, type Point, type Service } from './api'
import { go, isTyping, useKey, usePoll, useRoute } from './hooks'
import { setPrefs } from './prefs'
import { passkeysSupported, signInWithPasskey } from './passkey'
import { Canvas } from './components/Canvas'
import { CommandList, matches, Modal, type Command } from './components/Command'
import { Icon, kindIcon, Logo, Sparkline } from './components/ui'
import { NewService } from './panels/NewService'
import { ServicePanel } from './panels/ServicePanel'
import { SettingsPanel } from './panels/SettingsPanel'
import { ServerPanel } from './panels/ServerPanel'

export function App() {
  const [me, setMe] = useState<Me | null>(null)
  const load = useCallback(() => {
    api<Me>('/me').then(setMe, () => setMe({ needs_setup: false }))
  }, [])
  useEffect(load, [load])

  if (!me) return null
  if (!me.user) return <Auth setup={me.needs_setup} onDone={load} />
  return <Shell onSignOut={() => api('/logout', { method: 'POST' }).then(load)} />
}

function Auth({ setup, onDone }: { setup: boolean; onDone: () => void }) {
  const [code, setCode] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState(() => (location.hash.includes('recover=expired') ? 'That sign-in link has expired or was already used.' : ''))
  const [busy, setBusy] = useState(false)
  const [second, setSecond] = useState<null | { emailRecovery: boolean }>(null)

  async function run(fn: () => Promise<void>) {
    setBusy(true)
    setError('')
    try {
      await fn()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  function submit(e: React.FormEvent) {
    e.preventDefault()
    run(async () => {
      if (setup) {
        await api('/setup', { method: 'POST', body: { code, email, password } })
        return onDone()
      }
      const res = await api<{ needs_2fa?: boolean; email_recovery?: boolean }>('/login', { method: 'POST', body: { email, password } })
      if (res.needs_2fa) setSecond({ emailRecovery: !!res.email_recovery })
      else onDone()
    })
  }

  if (second) return <SecondFactor emailRecovery={second.emailRecovery} onDone={onDone} onRestart={() => (setSecond(null), setPassword(''))} />

  return (
    <div className="auth">
      <form className="card stack" onSubmit={submit}>
        <div className="row">
          <Logo />
          <h1>{setup ? 'Set up OSS Railway' : 'Sign in'}</h1>
        </div>
        {setup && (
          <label>
            Setup code <input value={code} onChange={(e) => setCode(e.target.value)} placeholder="XXXX-XXXX" autoComplete="off" required autoFocus />
          </label>
        )}
        <label>
          Email <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="username" required autoFocus={!setup} />
        </label>
        <label>
          Password
          <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete={setup ? 'new-password' : 'current-password'} minLength={setup ? 10 : undefined} required />
        </label>
        {error && <p className="error">{error}</p>}
        <button className="primary" style={{ height: 36 }} disabled={busy}>
          {setup ? 'Create account' : 'Sign in'}
        </button>
        {!setup && passkeysSupported() && (
          <button type="button" style={{ height: 36 }} disabled={busy} onClick={() => run(async () => (await signInWithPasskey(), onDone()))}>
            Sign in with a passkey
          </button>
        )}
      </form>
    </div>
  )
}

// Second sign-in step: a code from the authenticator app, or a recovery code, or an emailed link.
function SecondFactor({ emailRecovery, onDone, onRestart }: { emailRecovery: boolean; onDone: () => void; onRestart: () => void }) {
  const [code, setCode] = useState('')
  const [recovery, setRecovery] = useState(false)
  const [error, setError] = useState('')
  const [sentTo, setSentTo] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api('/login/2fa', { method: 'POST', body: { code } })
      onDone()
    } catch (err) {
      const msg = (err as Error).message
      if (msg.includes('sign in again')) onRestart()
      setError(msg)
    } finally {
      setBusy(false)
    }
  }

  async function emailLink() {
    setError('')
    try {
      const r = await api<{ sent_to: string }>('/login/recover', { method: 'POST' })
      setSentTo(r.sent_to)
    } catch (err) {
      setError((err as Error).message)
    }
  }

  return (
    <div className="auth">
      <form className="card stack" onSubmit={submit}>
        <div className="row">
          <Logo />
          <h1>{sentTo ? 'Check your email' : recovery ? 'Recovery code' : 'Authenticator code'}</h1>
        </div>
        {sentTo ? (
          <p className="muted">Sign-in link sent to {sentTo}. It works once, for 15 minutes.</p>
        ) : (
          <>
            <input
              key={String(recovery)}
              value={code}
              onChange={(e) => setCode(e.target.value)}
              placeholder={recovery ? 'xxxxx-xxxxx' : '123 456'}
              inputMode={recovery ? 'text' : 'numeric'}
              autoComplete="one-time-code"
              aria-label={recovery ? 'Recovery code' : 'Authenticator code'}
              style={{ fontFamily: 'var(--mono)', fontSize: 18, height: 44, letterSpacing: 2 }}
              required
              autoFocus
            />
            {error && <p className="error">{error}</p>}
            <button className="primary" style={{ height: 36 }} disabled={busy}>
              Verify
            </button>
            <div className="stack" style={{ gap: 4, justifyItems: 'start' }}>
              <button type="button" className="ghost" style={{ paddingLeft: 0 }} onClick={() => (setRecovery(!recovery), setCode(''), setError(''))}>
                {recovery ? 'Use the authenticator app' : 'Use a recovery code'}
              </button>
              {emailRecovery && (
                <button type="button" className="ghost" style={{ paddingLeft: 0 }} onClick={emailLink}>
                  Lost your phone? Email me a sign-in link
                </button>
              )}
            </div>
          </>
        )}
      </form>
    </div>
  )
}

function Shell({ onSignOut }: { onSignOut: () => void }) {
  const { parts, query } = useRoute()
  const [services, reloadServices] = usePoll(() => api<Service[]>('/services'), 5000)
  const [overview] = usePoll(() => api<Overview>('/overview'), 15000)
  const [series] = usePoll(() => api<Point[]>('/metrics/host?range=1h'), 15000)
  const [palette, setPalette] = useState(false)
  const [alertsOpen, setAlertsOpen] = useState(false)
  const [toast, setToast] = useState<{ text: string; action?: { label: string; run: () => void } } | null>(null)

  useEffect(() => {
    if (!toast) return
    const t = setTimeout(() => setToast(null), 6000)
    return () => clearTimeout(t)
  }, [toast])

  useKey((e) => {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
      e.preventDefault()
      setPalette((p) => !p)
    } else if (isTyping(e) || palette || parts[0] === 'new') {
      return
    } else if (e.key === 'n') {
      e.preventDefault()
      go('/new')
    } else if (e.key === 'Escape' && parts.length) {
      go('/')
    }
  })

  const selectedId = parts[0] === 's' ? Number(parts[1]) : null
  const selected = services?.find((s) => s.id === selectedId) ?? null

  async function move(id: number, at: { x: number; y: number }) {
    try {
      await api(`/services/${id}`, { method: 'PATCH', body: at })
      reloadServices()
    } catch (e) {
      setToast({ text: (e as Error).message })
    }
  }

  async function link(fromId: number, toId: number) {
    const a = services?.find((s) => s.id === fromId)
    const b = services?.find((s) => s.id === toId)
    if (!a || !b) return
    const [app, db] = a.kind === 'database' ? [b, a] : [a, b]
    if (app.kind !== 'app' || db.kind !== 'database') {
      setToast({ text: 'Link an app to a database' })
      return
    }
    try {
      const key = await linkServices(app, db)
      reloadServices()
      setToast({
        text: `${app.name} uses ${db.name} as ${key}`,
        action: {
          label: 'Redeploy',
          run: () => api(`/services/${app.id}/deploy`, { method: 'POST' }).then(() => go(`/s/${app.id}/deployments`)),
        },
      })
    } catch (e) {
      setToast({ text: (e as Error).message })
    }
  }

  const close = () => setPalette(false)
  const commands: Command[] = [
    { id: 'new', label: 'New service', icon: 'plus', hint: 'N', group: 'Actions', run: () => (close(), go('/new')) },
    { id: 'settings', label: 'Settings', icon: 'settings', group: 'Actions', run: () => (close(), go('/settings')) },
    { id: 'server', label: 'Server: ports and containers', icon: 'server', group: 'Actions', run: () => (close(), go('/server')) },
    { id: 'security', label: 'Two-factor sign-in and API tokens', icon: 'shield', group: 'Actions', run: () => (close(), go('/settings')) },
    ...(['light', 'dark', 'system'] as const).map((t) => ({ id: 'theme-' + t, label: `Theme: ${t}`, icon: 'sun', group: 'Actions', run: () => (setPrefs({ theme: t }), close()) })),
    { id: 'signout', label: 'Sign out', icon: 'logout', group: 'Actions', run: onSignOut },
    ...(services ?? []).map((s) => ({ id: 's' + s.id, label: s.name, icon: kindIcon(s.source), hint: s.status, group: 'Services', run: () => (close(), go(`/s/${s.id}`)) })),
  ]
  const [paletteQuery, setPaletteQuery] = useState('')

  const h = overview?.host
  const alerts = overview?.alerts ?? []

  return (
    <>
      <header className="topbar">
        <a href="#/" className="brand">
          <Logo />
          <span className="hide-sm">OSS Railway</span>
        </a>
        <div className="row">
          <button className="ghost hide-sm" onClick={() => setPalette(true)} style={{ color: 'var(--muted)' }}>
            <Icon name="search" /> Search <span className="kbd">Ctrl K</span>
          </button>
          <div style={{ position: 'relative' }}>
            <button className="ghost icon" onClick={() => setAlertsOpen((o) => !o)} aria-label={`Alerts (${alerts.length})`} aria-expanded={alertsOpen}>
              <Icon name="bell" />
              {alerts.length > 0 && <span className="dot failed" style={{ position: 'absolute', top: 6, right: 6, width: 6, height: 6 }} />}
            </button>
            {alertsOpen && (
              <div className="card" style={{ position: 'absolute', right: 0, top: 40, width: 'min(360px, calc(100vw - 32px))', maxHeight: 400, overflow: 'auto', boxShadow: 'var(--shadow-lg)' }}>
                {alerts.length === 0 && <p className="muted item">No alerts</p>}
                {alerts.map((a) => (
                  <div key={a.id} className="item stack" style={{ gap: 2 }}>
                    <span>{a.message}</span>
                    <span className="faint" style={{ fontSize: 12 }}>
                      {ago(a.ts)}
                    </span>
                  </div>
                ))}
              </div>
            )}
          </div>
          <a className="button ghost icon" href="#/server" aria-label="Server: ports and containers" title="Server">
            <Icon name="server" />
          </a>
          <a className={`button ghost icon${parts[0] === 'settings' ? ' active' : ''}`} href="#/settings" aria-label="Settings">
            <Icon name="settings" />
          </a>
          <button className="primary" onClick={() => go('/new')}>
            <Icon name="plus" /> New
          </button>
        </div>
      </header>

      {services && (
        <Canvas
          services={services}
          selected={selectedId}
          onOpen={(id) => go(`/s/${id}`)}
          onBackground={() => {
            setAlertsOpen(false)
            if (parts.length) go('/')
          }}
          onMove={move}
          onLink={link}
          onNew={() => go('/new')}
        />
      )}

      {h?.t ? (
        <div className="card stats" aria-label="Server">
          {[
            { label: 'CPU', value: h.cpu.toFixed(0) + '%', values: (series ?? []).map((p) => p.cpu), max: 100 },
            { label: 'Memory', value: bytes(h.mem), values: (series ?? []).map((p) => p.mem), max: h.mem_limit },
            { label: 'Disk', value: bytes(h.disk), values: (series ?? []).map((p) => p.disk), max: h.disk_total },
          ].map((t) => (
            <div key={t.label} className="stat">
              <span className="muted">{t.label}</span>
              <b>{t.value}</b>
              <Sparkline values={t.values} max={t.max} height={18} />
            </div>
          ))}
        </div>
      ) : null}

      {selected && <ServicePanel key={selected.id} service={selected} services={services ?? []} tab={parts[2] || 'deployments'} first={query.has('first')} onChange={reloadServices} />}
      {parts[0] === 'server' && <ServerPanel />}
      {parts[0] === 'settings' && <SettingsPanel security={query.has('security')} />}
      {parts[0] === 'new' && <NewService onClose={() => go('/')} onCreated={reloadServices} />}
      {palette && (
        <Modal onClose={close} label="Search">
          <CommandList
            commands={commands.filter((c) => matches(c.label, paletteQuery))}
            query={paletteQuery}
            onQuery={setPaletteQuery}
            placeholder="Search services and actions…"
          />
        </Modal>
      )}
      {toast && (
        <div className="card toast row" role="status">
          <span>{toast.text}</span>
          {toast.action && (
            <button
              className="primary"
              onClick={() => {
                toast.action!.run()
                setToast(null)
              }}
            >
              {toast.action.label}
            </button>
          )}
          <button className="ghost icon" onClick={() => setToast(null)} aria-label="Dismiss">
            <Icon name="x" />
          </button>
        </div>
      )}
    </>
  )
}
