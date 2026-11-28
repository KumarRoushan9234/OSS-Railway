import { api, bytes, type External } from '../api'
import { go, usePoll } from '../hooks'
import { Icon } from '../components/ui'

export interface ServerInfo {
  ports: { port: number; address: string; process: string; pid: number; container: string; public: boolean }[]
  containers: { id: string; name: string; image: string; state: string; status: string; ports: string; project: string; service_id: number; cpu: number; mem: number; mem_limit: number; size: string }[]
  volumes: { name: string; size: string; links: string }[]
  host: { cpus: number; mem: number; mem_total: number; disk: number; disk_total: number }
  allocation: Allocation
}

interface Allocation {
  services: { id: number; name: string; cpu: number; memory: number; cpu_used: number; mem_used: number; disk: string }[]
  cpu_limited: number
  memory_limited: number
  unlimited: number
  cpus: number
  memory_total: number
  warnings: string[]
}

// One bar per resource: each service's limit as a segment, the rest is unallocated.
function SplitBar({ label, parts, total, format }: { label: string; parts: { name: string; value: number }[]; total: number; format: (n: number) => string }) {
  const used = parts.reduce((n, p) => n + p.value, 0)
  return (
    <div className="stack" style={{ gap: 6 }}>
      <div className="spread" style={{ fontSize: 13 }}>
        <span className="muted">{label}</span>
        <span>
          {format(used)} <span className="faint">of {format(total)} allocated</span>
        </span>
      </div>
      <div style={{ display: 'flex', height: 10, borderRadius: 5, background: 'var(--sunken)', overflow: 'hidden', gap: 2 }}>
        {parts
          .filter((p) => p.value > 0)
          .map((p, i) => (
            <div
              key={p.name}
              title={`${p.name}: ${format(p.value)}`}
              style={{ width: Math.min(100, (p.value / (total || 1)) * 100) + '%', background: 'var(--accent)', opacity: 1 - (i % 4) * 0.2 }}
            />
          ))}
      </div>
    </div>
  )
}

function AllocationView({ a }: { a: Allocation }) {
  const cores = (n: number) => (Math.round(n * 100) / 100).toString() + ' cores'
  return (
    <section className="card panel stack">
      <h2>Allocation</h2>
      <SplitBar label="CPU" parts={a.services.map((s) => ({ name: s.name, value: s.cpu }))} total={a.cpus} format={cores} />
      <SplitBar label="Memory" parts={a.services.map((s) => ({ name: s.name, value: s.memory * 1048576 }))} total={a.memory_total} format={bytes} />
      {a.warnings.map((w) => (
        <span key={w} style={{ color: 'var(--warn)', fontSize: 13 }}>
          {w}
        </span>
      ))}
      {a.unlimited > 0 && (
        <span className="muted" style={{ fontSize: 13 }}>
          {a.unlimited} {a.unlimited === 1 ? 'service has' : 'services have'} no CPU or memory limit and can use the whole server. Set limits in each service's Settings → Resources.
        </span>
      )}
      {a.services.length > 0 && (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>Service</th>
                <th>CPU limit · now</th>
                <th>Memory limit · now</th>
                <th className="hide-sm">Disk</th>
              </tr>
            </thead>
            <tbody>
              {a.services.map((s) => (
                <tr key={s.id} className="clickable" onClick={() => go(`/s/${s.id}/settings`)}>
                  <td>{s.name}</td>
                  <td>
                    {s.cpu ? cores(s.cpu) : <span className="faint">none</span>} · {s.cpu_used.toFixed(2)}
                  </td>
                  <td>
                    {s.memory ? s.memory + ' MB' : <span className="faint">none</span>} · {bytes(s.mem_used)}
                  </td>
                  <td className="muted hide-sm">{s.disk?.split(' ')[0] || '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

function Meter({ label, used, total, format }: { label: string; used: number; total: number; format: (n: number) => string }) {
  const pct = total ? Math.min(100, (used / total) * 100) : 0
  return (
    <div className="stack" style={{ gap: 6 }}>
      <div className="spread" style={{ fontSize: 13 }}>
        <span className="muted">{label}</span>
        <span>
          {format(used)} <span className="faint">/ {format(total)}</span>
        </span>
      </div>
      <div style={{ height: 6, borderRadius: 3, background: 'var(--sunken)', overflow: 'hidden' }} role="meter" aria-label={label} aria-valuenow={Math.round(pct)}>
        <div style={{ width: pct + '%', height: '100%', background: pct > 85 ? 'var(--danger)' : 'var(--accent)' }} />
      </div>
    </div>
  )
}

// Everything on the machine: listening ports, every container (deployed here or not), volumes.
export function ServerPanel({ onAdopt }: { onAdopt: (c: External) => void }) {
  const [info] = usePoll(() => api<ServerInfo>('/server'), 10000)

  return (
    <aside className="card drawer" aria-label="Server">
      <div className="drawer-head" style={{ paddingBottom: 16, borderBottom: '1px solid var(--border)' }}>
        <div className="spread">
          <h1>Server</h1>
          <button className="ghost icon" onClick={() => go('/')} aria-label="Close">
            <Icon name="x" />
          </button>
        </div>
      </div>
      <div className="drawer-body">
        {!info ? (
          <p className="muted">Reading the server…</p>
        ) : (
          <>
            <section className="card panel stack">
              <div className="spread">
                <h2>Resources</h2>
                <span className="muted" style={{ fontSize: 13 }}>
                  {info.host.cpus} CPU cores
                </span>
              </div>
              <Meter label="Memory" used={info.host.mem} total={info.host.mem_total} format={bytes} />
              <Meter label="Disk" used={info.host.disk} total={info.host.disk_total} format={bytes} />
            </section>

            {info.allocation && <AllocationView a={info.allocation} />}

            <section className="stack" style={{ gap: 8 }}>
              <h2>Listening ports</h2>
              <div className="card table-wrap">
                <table className="table">
                  <thead>
                    <tr>
                      <th>Port</th>
                      <th>Address</th>
                      <th>Process</th>
                      <th>Container</th>
                    </tr>
                  </thead>
                  <tbody>
                    {info.ports.length === 0 && (
                      <tr>
                        <td colSpan={4} className="muted">
                          No listening ports found
                        </td>
                      </tr>
                    )}
                    {info.ports.map((p) => (
                      <tr key={p.address + p.port}>
                        <td>
                          <b>{p.port}</b>
                        </td>
                        <td>
                          <span className="row" style={{ gap: 6 }}>
                            <code>{p.address}</code>
                            {p.public && <span className="tag">public</span>}
                          </span>
                        </td>
                        <td className="muted">{p.process ? `${p.process} (${p.pid})` : '—'}</td>
                        <td className="muted">{p.container || '—'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>

            <section className="stack" style={{ gap: 8 }}>
              <h2>Containers</h2>
              <div className="card table-wrap">
                <table className="table">
                  <thead>
                    <tr>
                      <th>Name</th>
                      <th>CPU</th>
                      <th>Memory</th>
                      <th className="hide-sm">Disk</th>
                    </tr>
                  </thead>
                  <tbody>
                    {info.containers.map((c) => (
                      <tr key={c.id} className={c.service_id ? 'clickable' : undefined} onClick={c.service_id ? () => go(`/s/${c.service_id}`) : undefined}>
                        <td>
                          <span className="row" style={{ gap: 8, minWidth: 0 }}>
                            <span className={`dot ${c.state === 'running' ? 'ok' : c.state === 'exited' ? 'failed' : ''}`} title={c.status} />
                            <span className="stack" style={{ gap: 0, minWidth: 0 }}>
                              <span className="ellipsis">{c.name}</span>
                              <span className="faint ellipsis" style={{ fontSize: 12 }}>
                                {c.image}
                              </span>
                            </span>
                            {!c.service_id && (c.name === 'ossr-panel' || c.name === 'ossr-caddy' || c.name === 'ossr-tunnel') && <span className="tag">system</span>}
                            {!c.service_id && !c.name.startsWith('ossr-') && (
                              <button
                                className="tag"
                                style={{ cursor: 'pointer', background: 'var(--accent-soft)' }}
                                onClick={(e) => {
                                  e.stopPropagation()
                                  onAdopt({ name: c.name, image: c.image, state: c.state, status: c.status, ports: c.ports, project: c.project, service_id: 0 })
                                }}
                              >
                                Adopt
                              </button>
                            )}
                          </span>
                        </td>
                        <td>{c.state === 'running' ? c.cpu.toFixed(1) + '%' : '—'}</td>
                        <td>{c.state === 'running' ? bytes(c.mem) : '—'}</td>
                        <td className="muted hide-sm">{c.size?.split(' ')[0] || '—'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>

            {info.volumes.length > 0 && (
              <section className="stack" style={{ gap: 8 }}>
                <h2>Volumes</h2>
                <div className="card list">
                  {info.volumes.map((v) => (
                    <div key={v.name} className="spread item">
                      <code className="ellipsis">{v.name}</code>
                      <span className="faint">{v.size}</span>
                    </div>
                  ))}
                </div>
              </section>
            )}
          </>
        )}
      </div>
    </aside>
  )
}
