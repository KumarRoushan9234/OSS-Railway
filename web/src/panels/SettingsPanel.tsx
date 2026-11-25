import { useEffect, useState } from 'react'
import { api } from '../api'
import { go, useFlash } from '../hooks'
import { accents, setPrefs, usePrefs, type Density, type Theme } from '../prefs'
import { Icon } from '../components/ui'
import { Account, Activity, Passkeys, Tokens, TwoFactor } from './Security'
import { AISettings, RemoteAccess } from './Integrations'

type ServerSettings = Record<string, string | boolean>

export function SettingsPanel({ security = false }: { security?: boolean }) {
  const prefs = usePrefs()
  const [s, setS] = useState<ServerSettings | null>(null)
  const [gh, setGh] = useState<{ connected: boolean; app_slug: string } | null>(null)
  const [smtpPass, setSmtpPass] = useState('')
  const [webhook, setWebhook] = useState('')
  const [error, setError] = useState('')
  const [saved, flash] = useFlash()
  const [report, setReport] = useState<{ subject: string; body: string } | null>(null)

  useEffect(() => {
    Promise.all([api<ServerSettings>('/settings'), api<{ connected: boolean; app_slug: string }>('/github')]).then(
      ([a, b]) => {
        setS(a)
        setGh(b)
      },
      (e) => setError(e.message),
    )
  }, [])

  const str = (k: string) => String(s?.[k] ?? '')
  const bind = (k: string) => ({ value: str(k), onChange: (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setS({ ...s, [k]: e.target.value }) })

  async function save(section: string, fields: string[], extra: Record<string, string> = {}) {
    setError('')
    try {
      const body: Record<string, string> = { ...extra }
      for (const f of fields) body[f] = str(f)
      setS(await api<ServerSettings>('/settings', { method: 'PUT', body }))
      setSmtpPass('')
      setWebhook('')
      flash(section)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const dashed = s?.public_ip ? String(s.public_ip).replaceAll('.', '-') + '.sslip.io' : ''

  return (
    <aside className="card drawer" aria-label="Settings">
      <div className="drawer-head" style={{ paddingBottom: 16, borderBottom: '1px solid var(--border)' }}>
        <div className="spread">
          <h1>Settings</h1>
          <button className="ghost icon" onClick={() => go('/')} aria-label="Close">
            <Icon name="x" />
          </button>
        </div>
      </div>
      <div className="drawer-body">
        {error && <p className="error">{error}</p>}

        <section className="card panel stack">
          <h2>Appearance</h2>
          <div className="spread">
            <span className="muted">Theme</span>
            <div className="segmented">
              {(['system', 'light', 'dark'] as Theme[]).map((t) => (
                <button key={t} className={prefs.theme === t ? 'on' : undefined} onClick={() => setPrefs({ theme: t })}>
                  {t[0].toUpperCase() + t.slice(1)}
                </button>
              ))}
            </div>
          </div>
          <div className="spread">
            <span className="muted">Accent</span>
            <div className="row">
              {Object.entries(accents).map(([name, c]) => (
                <button
                  key={name}
                  className={`swatch${prefs.accent === name ? ' on' : ''}`}
                  style={{ background: document.documentElement.dataset.theme === 'dark' ? c.dark : c.light, color: c.light }}
                  onClick={() => setPrefs({ accent: name })}
                  aria-label={name}
                  aria-pressed={prefs.accent === name}
                />
              ))}
            </div>
          </div>
          <div className="spread">
            <span className="muted">Cards</span>
            <div className="segmented">
              {(['comfortable', 'compact'] as Density[]).map((d) => (
                <button key={d} className={prefs.density === d ? 'on' : undefined} onClick={() => setPrefs({ density: d })}>
                  {d[0].toUpperCase() + d.slice(1)}
                </button>
              ))}
            </div>
          </div>
          <label className="check">
            <input type="checkbox" checked={prefs.grid} onChange={(e) => setPrefs({ grid: e.target.checked })} /> Dot grid on canvas
          </label>
        </section>

        <Account />
        <TwoFactor highlight={security} />
        <Passkeys />
        <Tokens />
        <AISettings />
        <RemoteAccess />

        {s && (
          <>
            <form
              className="card panel stack"
              onSubmit={(e) => {
                e.preventDefault()
                save('domains', ['base_domain', 'panel_domain', 'acme_email'])
              }}
            >
              <h2>Domains</h2>
              <label>
                Apps domain <input {...bind('base_domain')} placeholder={dashed || 'example.com'} />
              </label>
              {str('base_domain') && (
                <p className="muted" style={{ fontSize: 13 }}>
                  DNS: <code>A *.{str('base_domain')} → {str('public_ip') || 'server IP'}</code>
                </p>
              )}
              <label>
                Dashboard domain <input {...bind('panel_domain')} placeholder={dashed || 'panel.example.com'} />
              </label>
              <label>
                Email for HTTPS certificates <input type="email" {...bind('acme_email')} placeholder="you@example.com" />
              </label>
              <div>
                <button className="primary">{saved === 'domains' ? 'Saved' : 'Save'}</button>
              </div>
            </form>

            <form
              className="card panel stack"
              onSubmit={(e) => {
                e.preventDefault()
                const extra: Record<string, string> = {}
                if (smtpPass) extra.smtp_pass = smtpPass
                if (webhook) extra.webhook_url = webhook
                save('alerts', ['smtp_host', 'smtp_port', 'smtp_user', 'alert_from', 'alert_to'], extra)
              }}
            >
              <h2>Alerts</h2>
              <label>
                Send alerts to <input type="email" multiple {...bind('alert_to')} placeholder="you@example.com" />
              </label>
              <div className="two">
                <label>
                  SMTP host <input {...bind('smtp_host')} placeholder="smtp.resend.com" />
                </label>
                <label>
                  Port <input {...bind('smtp_port')} placeholder="587" inputMode="numeric" />
                </label>
              </div>
              <div className="two">
                <label>
                  SMTP user <input {...bind('smtp_user')} autoComplete="off" />
                </label>
                <label>
                  SMTP password
                  <input type="password" value={smtpPass} onChange={(e) => setSmtpPass(e.target.value)} placeholder={s.smtp_pass_set ? '••••••••' : ''} autoComplete="new-password" />
                </label>
              </div>
              <label>
                From <input {...bind('alert_from')} placeholder="alerts@example.com" />
              </label>
              <label>
                Slack or Discord webhook
                <input value={webhook} onChange={(e) => setWebhook(e.target.value)} placeholder={s.webhook_url_set ? '••••••••' : 'https://hooks.slack.com/…'} autoComplete="off" />
              </label>
              <div className="row">
                <button className="primary">{saved === 'alerts' ? 'Saved' : 'Save'}</button>
                <button
                  type="button"
                  onClick={() =>
                    api('/settings/test-alert', { method: 'POST' }).then(
                      () => flash('test'),
                      (e) => setError(e.message),
                    )
                  }
                >
                  {saved === 'test' ? 'Sent' : 'Send test'}
                </button>
              </div>
            </form>

            <section className="card panel stack">
              <div className="spread">
                <h2>Daily report</h2>
                <select style={{ width: 'auto' }} {...bind('daily_report')} onChange={(e) => {
                  setS({ ...s, daily_report: e.target.value })
                  api<ServerSettings>('/settings', { method: 'PUT', body: { daily_report: e.target.value } }).then(setS, (err) => setError(err.message))
                }}>
                  <option value="">On, 09:00</option>
                  <option value="off">Off</option>
                </select>
              </div>
              <div>
                <button onClick={() => api<{ subject: string; body: string }>('/settings/test-report', { method: 'POST' }).then(setReport, (e) => setError(e.message))}>Preview</button>
              </div>
              {report && (
                <pre style={{ background: 'var(--sunken)', padding: 12, borderRadius: 6, overflow: 'auto', margin: 0 }}>
                  {report.subject}
                  {'\n\n'}
                  {report.body}
                </pre>
              )}
            </section>

            <section className="card panel stack">
              <h2>GitHub</h2>
              {gh?.connected ? (
                <div className="spread">
                  <span className="row">
                    <span className="dot live" />
                    {gh.app_slug}
                  </span>
                  <a className="button" href={`https://github.com/settings/apps/${gh.app_slug}`} target="_blank" rel="noreferrer">
                    Manage on GitHub
                  </a>
                </div>
              ) : (
                <div>
                  <a className="button primary" href="#/new">
                    Connect GitHub
                  </a>
                </div>
              )}
            </section>
          </>
        )}
        <Activity />
      </div>
    </aside>
  )
}
