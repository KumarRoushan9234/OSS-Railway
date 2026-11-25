import { useCallback, useEffect, useState } from 'react'
import qrcode from 'qrcode-generator'
import { ago, api } from '../api'
import { addPasskey, passkeysSupported } from '../passkey'

// Black-on-white QR (scanners need the contrast) drawn as one SVG path.
function QR({ text }: { text: string }) {
  const qr = qrcode(0, 'M')
  qr.addData(text)
  qr.make()
  const n = qr.getModuleCount()
  let d = ''
  for (let y = 0; y < n; y++) for (let x = 0; x < n; x++) if (qr.isDark(y, x)) d += `M${x + 2},${y + 2}h1v1h-1z`
  return (
    <svg viewBox={`0 0 ${n + 4} ${n + 4}`} width="176" height="176" role="img" aria-label="QR code for your authenticator app" style={{ background: '#fff', borderRadius: 8 }}>
      <path d={d} fill="#000" shapeRendering="crispEdges" />
    </svg>
  )
}

function Codes({ codes }: { codes: string[] }) {
  const [copied, setCopied] = useState(false)
  return (
    <div className="stack" style={{ gap: 8 }}>
      <p className="muted" style={{ fontSize: 13 }}>
        Save these recovery codes somewhere safe. Each works once if you lose your phone. They won't be shown again.
      </p>
      <pre style={{ margin: 0, padding: 12, background: 'var(--sunken)', borderRadius: 6, columns: 2, fontSize: 14, lineHeight: 1.8 }}>{codes.join('\n')}</pre>
      <div className="row">
        <button
          type="button"
          onClick={() =>
            navigator.clipboard.writeText(codes.join('\n')).then(
              () => setCopied(true),
              () => {},
            )
          }
        >
          {copied ? 'Copied' : 'Copy'}
        </button>
        <a className="button" download="oss-railway-recovery-codes.txt" href={'data:text/plain;charset=utf-8,' + encodeURIComponent(codes.join('\n') + '\n')}>
          Download
        </a>
      </div>
    </div>
  )
}

export function TwoFactor({ highlight }: { highlight: boolean }) {
  const [status, setStatus] = useState<{ enabled: boolean; recovery_codes_left: number; email_recovery: boolean } | null>(null)
  const [setup, setSetup] = useState<{ secret: string; uri: string } | null>(null)
  const [codes, setCodes] = useState<string[] | null>(null)
  const [code, setCode] = useState('')
  const [password, setPassword] = useState('')
  const [mode, setMode] = useState<'' | 'disable' | 'regen'>('')
  const [error, setError] = useState('')

  const load = useCallback(() => {
    api<typeof status>('/2fa').then(setStatus, (e) => setError(e.message))
  }, [])
  useEffect(load, [load])

  async function run(fn: () => Promise<void>) {
    setError('')
    try {
      await fn()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  if (!status) return null
  return (
    <section className="card panel stack" style={highlight ? { borderColor: 'var(--accent)' } : undefined}>
      <div className="spread">
        <h2>Two-factor sign-in</h2>
        <span className="row muted" style={{ fontSize: 13 }}>
          <span className={`dot ${status.enabled ? 'ok' : ''}`} />
          {status.enabled ? 'On' : 'Off'}
        </span>
      </div>
      {highlight && !status.enabled && <p className="muted">You signed in with an email link, so two-factor sign-in was turned off. Set it up again on your new phone.</p>}
      {error && <p className="error">{error}</p>}

      {codes ? (
        <>
          <Codes codes={codes} />
          <div>
            <button className="primary" onClick={() => (setCodes(null), load())}>
              Done
            </button>
          </div>
        </>
      ) : !status.enabled && !setup ? (
        <div>
          <button className="primary" onClick={() => run(async () => setSetup(await api('/2fa/setup', { method: 'POST' })))}>
            Set up authenticator app
          </button>
        </div>
      ) : !status.enabled && setup ? (
        <form
          className="stack"
          onSubmit={(e) => {
            e.preventDefault()
            run(async () => {
              const r = await api<{ recovery_codes: string[] }>('/2fa/enable', { method: 'POST', body: { code } })
              setSetup(null)
              setCode('')
              setCodes(r.recovery_codes)
            })
          }}
        >
          <div className="row wrap" style={{ gap: 24, alignItems: 'flex-start' }}>
            <QR text={setup.uri} />
            <div className="stack" style={{ gap: 8, flex: 1, minWidth: 200 }}>
              <p className="muted" style={{ fontSize: 13 }}>
                Scan with Google Authenticator, 1Password, Authy or any TOTP app. Or enter this key:
              </p>
              <code style={{ wordBreak: 'break-all', fontSize: 13 }}>{setup.secret.match(/.{1,4}/g)!.join(' ')}</code>
              <label>
                Code from the app
                <input value={code} onChange={(e) => setCode(e.target.value)} inputMode="numeric" autoComplete="one-time-code" placeholder="123 456" required autoFocus />
              </label>
              <div className="row">
                <button className="primary">Turn on</button>
                <button type="button" className="ghost" onClick={() => setSetup(null)}>
                  Cancel
                </button>
              </div>
            </div>
          </div>
        </form>
      ) : (
        <>
          <p className="muted" style={{ fontSize: 13 }}>
            {status.recovery_codes_left} recovery codes left. {status.email_recovery ? 'A sign-in link can also be emailed to you.' : 'Set up email alerts to also allow a sign-in link by email.'}
          </p>
          {mode ? (
            <form
              className="stack"
              onSubmit={(e) => {
                e.preventDefault()
                run(async () => {
                  if (mode === 'disable') {
                    await api('/2fa/disable', { method: 'POST', body: { password, code } })
                    load()
                  } else {
                    const r = await api<{ recovery_codes: string[] }>('/2fa/recovery-codes', { method: 'POST', body: { code } })
                    setCodes(r.recovery_codes)
                  }
                  setMode('')
                  setCode('')
                  setPassword('')
                })
              }}
            >
              <div className="two">
                {mode === 'disable' && (
                  <label>
                    Password <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" required />
                  </label>
                )}
                <label>
                  Authenticator code <input value={code} onChange={(e) => setCode(e.target.value)} inputMode="numeric" autoComplete="one-time-code" required autoFocus />
                </label>
              </div>
              <div className="row">
                <button className={mode === 'disable' ? 'danger' : 'primary'}>{mode === 'disable' ? 'Turn off' : 'New codes'}</button>
                <button type="button" className="ghost" onClick={() => setMode('')}>
                  Cancel
                </button>
              </div>
            </form>
          ) : (
            <div className="row wrap">
              <button onClick={() => setMode('regen')}>New recovery codes</button>
              <button className="danger" onClick={() => setMode('disable')}>
                Turn off
              </button>
            </div>
          )}
        </>
      )}
    </section>
  )
}

interface Token {
  id: number
  name: string
  scope: 'read' | 'full'
  created: number
  last_used: number
  expires: number
}

export function Tokens() {
  const [tokens, setTokens] = useState<Token[]>([])
  const [name, setName] = useState('')
  const [scope, setScope] = useState<'read' | 'full'>('read')
  const [days, setDays] = useState('90')
  const [created, setCreated] = useState<{ token: string; mcp_url: string } | null>(null)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState('')

  const load = useCallback(() => {
    api<Token[]>('/tokens').then(setTokens, (e) => setError(e.message))
  }, [])
  useEffect(load, [load])

  const copy = (key: string, text: string) =>
    navigator.clipboard.writeText(text).then(
      () => setCopied(key),
      () => {},
    )

  const claudeCmd = created ? `claude mcp add --transport http oss-railway ${created.mcp_url} --header "Authorization: Bearer ${created.token}"` : ''

  return (
    <section className="card panel stack">
      <h2>API tokens and AI agents</h2>
      <p className="muted" style={{ fontSize: 13 }}>
        Tokens let scripts and AI agents (Claude Code, Claude Desktop, Cursor) use this server through the API or the MCP endpoint. Read-only tokens can list services, read logs and see the server; full tokens can also deploy and restart.
      </p>
      {error && <p className="error">{error}</p>}
      {created && (
        <div className="stack" style={{ gap: 8, padding: 12, background: 'var(--accent-soft)', borderRadius: 8 }}>
          <span style={{ fontSize: 13 }}>Copy this token now; it won't be shown again.</span>
          <div className="row">
            <code className="ellipsis" style={{ flex: 1, fontSize: 13 }}>
              {created.token}
            </code>
            <button onClick={() => copy('token', created.token)}>{copied === 'token' ? 'Copied' : 'Copy'}</button>
          </div>
          <span style={{ fontSize: 13 }}>Add to Claude Code:</span>
          <div className="row" style={{ alignItems: 'flex-start' }}>
            <code style={{ flex: 1, fontSize: 12, wordBreak: 'break-all' }}>{claudeCmd}</code>
            <button onClick={() => copy('cmd', claudeCmd)}>{copied === 'cmd' ? 'Copied' : 'Copy'}</button>
          </div>
          <div>
            <button className="ghost" onClick={() => setCreated(null)}>
              Done
            </button>
          </div>
        </div>
      )}
      <form
        className="row wrap"
        onSubmit={(e) => {
          e.preventDefault()
          setError('')
          api<{ token: string; mcp_url: string }>('/tokens', { method: 'POST', body: { name, scope, expires_days: Number(days) } }).then(
            (r) => {
              setCreated(r)
              setName('')
              setCopied('')
              load()
            },
            (err) => setError(err.message),
          )
        }}
      >
        <input value={name} onChange={(e) => setName(e.target.value)} placeholder="Name, e.g. claude-laptop" aria-label="Token name" required maxLength={64} style={{ flex: 1, minWidth: 180 }} />
        <div className="segmented">
          {(['read', 'full'] as const).map((s) => (
            <button key={s} type="button" className={scope === s ? 'on' : undefined} onClick={() => setScope(s)}>
              {s === 'read' ? 'Read-only' : 'Full access'}
            </button>
          ))}
        </div>
        <select value={days} onChange={(e) => setDays(e.target.value)} aria-label="Expires" style={{ width: 'auto' }}>
          <option value="7">7 days</option>
          <option value="30">30 days</option>
          <option value="90">90 days</option>
          <option value="365">1 year</option>
          <option value="0">Never expires</option>
        </select>
        <button className="primary">Create</button>
      </form>
      {tokens.length > 0 && (
        <div className="card list">
          {tokens.map((t) => (
            <div key={t.id} className="spread item">
              <span className="stack" style={{ gap: 0, minWidth: 0 }}>
                <span className="ellipsis">{t.name}</span>
                <span className="faint" style={{ fontSize: 12 }}>
                  {t.scope === 'full' ? 'Full access' : 'Read-only'} · {t.last_used ? 'used ' + ago(t.last_used) : 'never used'} ·{' '}
                  {!t.expires ? 'no expiry' : t.expires * 1000 < Date.now() ? 'expired' : 'expires ' + new Date(t.expires * 1000).toLocaleDateString()}
                </span>
              </span>
              <button className="danger" onClick={() => api(`/tokens/${t.id}`, { method: 'DELETE' }).then(load, (e) => setError(e.message))}>
                Revoke
              </button>
            </div>
          ))}
        </div>
      )}
    </section>
  )
}

export function Account() {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [code, setCode] = useState('')
  const [twoFA, setTwoFA] = useState(false)
  const [sessions, setSessions] = useState(0)
  const [msg, setMsg] = useState('')
  const [error, setError] = useState('')

  const load = useCallback(() => {
    api<{ active: number }>('/sessions').then((r) => setSessions(r.active), () => {})
    api<{ enabled: boolean }>('/2fa').then((r) => setTwoFA(r.enabled), () => {})
  }, [])
  useEffect(load, [load])

  return (
    <section className="card panel stack">
      <h2>Account</h2>
      {error && <p className="error">{error}</p>}
      {msg && <p style={{ color: 'var(--accent)', fontSize: 13 }}>{msg}</p>}
      <form
        className="stack"
        onSubmit={(e) => {
          e.preventDefault()
          setError('')
          setMsg('')
          api('/password', { method: 'POST', body: { current, new: next, code } }).then(
            () => {
              setCurrent('')
              setNext('')
              setCode('')
              setMsg('Password changed. Other browsers were signed out.')
              load()
            },
            (err) => setError(err.message),
          )
        }}
      >
        <div className="two">
          <label>
            Current password <input type="password" value={current} onChange={(e) => setCurrent(e.target.value)} autoComplete="current-password" required />
          </label>
          <label>
            New password <input type="password" value={next} onChange={(e) => setNext(e.target.value)} autoComplete="new-password" minLength={10} maxLength={72} required />
          </label>
        </div>
        {twoFA && (
          <label>
            Authenticator code <input value={code} onChange={(e) => setCode(e.target.value)} inputMode="numeric" autoComplete="one-time-code" required />
          </label>
        )}
        <div>
          <button className="primary">Change password</button>
        </div>
      </form>
      <div className="spread">
        <span className="muted" style={{ fontSize: 13 }}>
          Signed in on {sessions} {sessions === 1 ? 'browser' : 'browsers'}
        </span>
        {sessions > 1 && (
          <button
            onClick={() =>
              api('/sessions', { method: 'DELETE' }).then(
                () => {
                  setMsg('Signed out everywhere else.')
                  load()
                },
                (err) => setError(err.message),
              )
            }
          >
            Sign out everywhere else
          </button>
        )}
      </div>
    </section>
  )
}

interface Passkey {
  id: string
  name: string
  created: number
  last_used: number
}

export function Passkeys() {
  const [keys, setKeys] = useState<Passkey[]>([])
  const [name, setName] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const load = useCallback(() => {
    api<Passkey[]>('/passkeys').then(setKeys, (e) => setError(e.message))
  }, [])
  useEffect(load, [load])

  return (
    <section className="card panel stack">
      <h2>Passkeys</h2>
      <p className="muted" style={{ fontSize: 13 }}>
        Sign in with Windows Hello, Touch ID, your phone or a security key instead of a password and code.
      </p>
      {!passkeysSupported() && <p className="muted" style={{ fontSize: 13 }}>Passkeys need HTTPS (or localhost) and a domain name.</p>}
      {error && <p className="error">{error}</p>}
      {passkeysSupported() && (
        <form
          className="row wrap"
          onSubmit={(e) => {
            e.preventDefault()
            setError('')
            setBusy(true)
            addPasskey(name)
              .then(
                () => {
                  setName('')
                  load()
                },
                (err) => setError(err.message),
              )
              .finally(() => setBusy(false))
          }}
        >
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="Name, e.g. Work laptop" aria-label="Passkey name" required maxLength={64} style={{ flex: 1, minWidth: 180 }} />
          <button className="primary" disabled={busy}>
            Add passkey
          </button>
        </form>
      )}
      {keys.length > 0 && (
        <div className="card list">
          {keys.map((k) => (
            <div key={k.id} className="spread item">
              <span className="stack" style={{ gap: 0 }}>
                <span>{k.name}</span>
                <span className="faint" style={{ fontSize: 12 }}>
                  Added {ago(k.created)} · {k.last_used ? 'used ' + ago(k.last_used) : 'never used'}
                </span>
              </span>
              <button className="danger" onClick={() => api(`/passkeys/${encodeURIComponent(k.id)}`, { method: 'DELETE' }).then(load, (e) => setError(e.message))}>
                Remove
              </button>
            </div>
          ))}
        </div>
      )}
    </section>
  )
}

interface AuditEntry {
  id: number
  ts: number
  actor: string
  ip: string
  action: string
  target: string
  status: number
}

// Readable labels for "METHOD /api/path" entries.
function describe(e: AuditEntry) {
  const [method, path] = e.action.split(' ')
  const p = path?.replace(/^\/api\//, '') ?? e.action
  const t = e.target ? ' ' + e.target : ''
  const ok = e.status < 400
  const rules: [RegExp, string][] = [
    [/^login$/, ok ? 'Signed in with password' : 'Failed sign-in'],
    [/^login\/2fa$/, ok ? 'Passed 2FA' : 'Wrong 2FA code'],
    [/^login\/recover/, 'Requested email sign-in link'],
    [/^passkeys\/login\/finish$/, ok ? 'Signed in with passkey' : 'Failed passkey sign-in'],
    [/^passkeys\/login\/begin$/, 'Started passkey sign-in'],
    [/^passkeys\/register\/finish$/, 'Added a passkey'],
    [/^passkeys\/register\/begin$/, 'Started adding a passkey'],
    [/^passkeys\//, 'Removed a passkey'],
    [/^setup$/, 'Created the account'],
    [/^logout$/, 'Signed out'],
    [/^password$/, 'Changed password'],
    [/^sessions$/, 'Signed out other browsers'],
    [/^2fa\/enable$/, 'Turned on 2FA'],
    [/^2fa\/disable$/, 'Turned off 2FA'],
    [/^2fa\/setup$/, 'Started 2FA setup'],
    [/^2fa\/recovery-codes$/, 'New recovery codes'],
    [/^tokens/, method === 'DELETE' ? 'Revoked an API token' : 'Created an API token'],
    [/^ai\/test$/, 'Tested AI endpoint'],
    [/^settings/, 'Changed settings'],
    [/^tunnel/, method === 'DELETE' ? 'Stopped remote access' : 'Started remote access'],
    [/^services\/\d+\/deploy$/, 'Deployed' + t],
    [/^services\/\d+\/restart$/, 'Restarted' + t],
    [/^services\/\d+\/env$/, 'Changed variables of' + t],
    [/^services\/\d+\/backups$/, 'Backed up' + t],
    [/^services\/\d+\/explain$/, 'Asked AI about' + t],
    [/^services\/\d+$/, (method === 'DELETE' ? 'Deleted' : 'Changed settings of') + t],
    [/^services$/, 'Created a service'],
    [/^deployments\/\d+\/rollback$/, 'Rolled back'],
  ]
  if (e.action.startsWith('MCP ')) return 'AI agent: ' + e.action.slice(4) + t
  for (const [re, label] of rules) if (re.test(p)) return label
  return e.action
}

export function Activity() {
  const [rows, setRows] = useState<AuditEntry[]>([])
  const [more, setMore] = useState(true)
  const load = useCallback((before?: number) => {
    api<AuditEntry[]>('/audit' + (before ? '?before=' + before : '')).then(
      (r) => {
        setRows((prev) => (before ? prev.concat(r) : r))
        setMore(r.length === 100)
      },
      () => {},
    )
  }, [])
  useEffect(() => load(), [load])

  return (
    <section className="stack" style={{ gap: 8 }}>
      <h2>Activity</h2>
      <div className="card table-wrap">
        <table className="table">
          <tbody>
            {rows.length === 0 && (
              <tr>
                <td className="muted">Nothing yet</td>
              </tr>
            )}
            {rows.map((e) => (
              <tr key={e.id}>
                <td>
                  <span className="row" style={{ gap: 6 }}>
                    <span className={`dot ${e.status >= 400 ? 'failed' : 'ok'}`} />
                    {describe(e)}
                  </span>
                </td>
                <td className="muted hide-sm">{e.actor}</td>
                <td className="faint hide-sm">
                  <code>{e.ip}</code>
                </td>
                <td className="faint" style={{ whiteSpace: 'nowrap' }}>
                  {ago(e.ts)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {more && rows.length > 0 && (
        <div>
          <button onClick={() => load(rows[rows.length - 1].id)}>Older</button>
        </div>
      )}
    </section>
  )
}
