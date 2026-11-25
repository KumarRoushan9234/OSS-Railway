export type Status = 'running' | 'stopped' | 'queued' | 'building' | 'deploying' | 'failed' | string

export interface Service {
  id: number
  name: string
  source: 'github' | 'git' | 'image' | 'postgres' | 'mysql' | 'redis' | 'mongo'
  repo: string
  branch: string
  image: string
  root_dir: string
  port: number
  domain: string
  autodeploy: boolean
  cpu: number
  memory: number
  x: number
  y: number
  created: number
  kind: 'app' | 'database'
  status: Status
  urls: string[]
  commit: string
  since: number
  uses: string[]
}

export interface Deployment {
  id: number
  status: Status
  commit: string
  message: string
  image: string
  created: number
  finished: number
}

export interface Point {
  ts: number
  cpu: number
  mem: number
  mem_limit: number
  net_rx: number
  net_tx: number
  disk: number
  disk_total: number
}

export interface Alert {
  id: number
  ts: number
  kind: string
  message: string
}

export interface Overview {
  host: Point & { t?: number }
  alerts: Alert[]
  github: boolean
  email: boolean
}

export interface Me {
  user?: { email: string }
  needs_setup: boolean
}

// Every non-GET request carries X-OSSR: the server rejects requests without it (CSRF guard).
export async function api<T = unknown>(path: string, opts: { method?: string; body?: unknown } = {}): Promise<T> {
  const { method = 'GET', body } = opts
  const headers: Record<string, string> = { 'X-OSSR': '1' }
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  const res = await fetch('/api' + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: 'same-origin',
  })
  if (res.status === 204) return null as T
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error(data.error || res.statusText)
  return data as T
}

export function bytes(n: number): string {
  if (!n) return '0 B'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), u.length - 1)
  return (n / 1024 ** i).toFixed(i ? 1 : 0) + ' ' + u[i]
}

export function ago(ts: number): string {
  if (!ts) return ''
  const s = Math.floor(Date.now() / 1000 - ts)
  if (s < 60) return 'just now'
  if (s < 3600) return Math.floor(s / 60) + 'm ago'
  if (s < 86400) return Math.floor(s / 3600) + 'h ago'
  return Math.floor(s / 86400) + 'd ago'
}

export function duration(a: number, b: number): string {
  if (!a || !b) return ''
  const s = b - a
  return s < 60 ? s + 's' : Math.floor(s / 60) + 'm ' + (s % 60) + 's'
}

export const sourceLabel: Record<Service['source'], string> = {
  github: 'GitHub',
  git: 'Git',
  image: 'Image',
  postgres: 'Postgres',
  mysql: 'MySQL',
  redis: 'Redis',
  mongo: 'MongoDB',
}

// The variable an app gets when it is linked to a database.
export function urlKey(source: Service['source']): string {
  return source === 'redis' ? 'REDIS_URL' : source === 'mongo' ? 'MONGO_URL' : 'DATABASE_URL'
}

export function parseEnv(text: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const line of text.split('\n')) {
    const t = line.trim()
    if (!t || t.startsWith('#')) continue
    const i = t.indexOf('=')
    if (i < 1) throw new Error(`Line "${t.slice(0, 40)}" needs KEY=value`)
    let v = t.slice(i + 1)
    if (/^(['"]).*\1$/.test(v)) v = v.slice(1, -1)
    out[t.slice(0, i).trim().replace(/^export\s+/, '')] = v
  }
  return out
}

export function formatEnv(vars: Record<string, string>): string {
  return Object.entries(vars)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([k, v]) => `${k}=${v}`)
    .join('\n')
}

// linkServices gives the app a ${{db.URL}} variable pointing at the database. Returns the key used.
export async function linkServices(app: Service, db: Service): Promise<string> {
  const vars = await api<Record<string, string>>(`/services/${app.id}/env`)
  const ref = `\${{${db.name}.${urlKey(db.source)}}}`
  if (Object.values(vars).includes(ref)) return Object.keys(vars).find((k) => vars[k] === ref)!
  let key = urlKey(db.source)
  if (vars[key] !== undefined) key = db.name.toUpperCase().replace(/-/g, '_') + '_' + key
  await api(`/services/${app.id}/env`, { method: 'PUT', body: { ...vars, [key]: ref } })
  return key
}
