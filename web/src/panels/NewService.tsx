import { useEffect, useState } from 'react'
import { api, type Service } from '../api'
import { go } from '../hooks'
import { CommandList, matches, Modal, type Command } from '../components/Command'
import { Icon } from '../components/ui'

interface GitHub {
  connected: boolean
  app_slug: string
  installations: unknown[] | null
}
interface Repo {
  full_name: string
  default_branch: string
  private: boolean
}
type Step = null | 'git' | 'image' | 'postgres' | 'mysql' | 'redis' | 'mongo'

const databases = [
  { id: 'postgres', label: 'Postgres' },
  { id: 'mysql', label: 'MySQL' },
  { id: 'redis', label: 'Redis' },
  { id: 'mongo', label: 'MongoDB' },
] as const

const imageLike = /^[a-z0-9][a-z0-9._\-/]*(:[\w.\-]+)?$/i

function slug(s: string) {
  return s.toLowerCase().replace(/^.*\//, '').replace(/\.git$/, '').replace(/:.*$/, '').replace(/[^a-z0-9-]+/g, '-').replace(/^[^a-z]+|-+$/g, '').slice(0, 32)
}

// Submits GitHub's App Manifest form: the user clicks "Create" on github.com, keys come back to the panel.
async function connectGitHub() {
  const { action, manifest } = await api<{ action: string; manifest: string }>('/github/manifest')
  const form = document.createElement('form')
  form.method = 'POST'
  form.action = action
  const input = document.createElement('input')
  input.type = 'hidden'
  input.name = 'manifest'
  input.value = manifest
  form.append(input)
  document.body.append(form)
  form.submit()
}

// One box: type to filter options and GitHub repos, or paste an image or Git URL to deploy it directly.
export function NewService({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [gh, setGh] = useState<GitHub | null>(null)
  const [repos, setRepos] = useState<Repo[] | null>(null)
  const [query, setQuery] = useState('')
  const [step, setStep] = useState<Step>(null)
  const [value, setValue] = useState('')
  const [name, setName] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api<GitHub>('/github').then((g) => {
      setGh(g)
      if (g.connected && g.installations?.length) api<Repo[]>('/github/repos').then(setRepos, (e) => setError(e.message))
    }, () => {})
  }, [])

  async function create(body: Partial<Service>) {
    setBusy(true)
    setError('')
    try {
      const s = await api<Service>('/services', { method: 'POST', body })
      onCreated()
      go(body.source === 'github' || body.source === 'git' ? `/s/${s.id}/variables?first=1` : `/s/${s.id}`)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  function open(next: Step, prefill = '') {
    setStep(next)
    setValue(prefill)
    setName('')
    setError('')
  }

  const q = query.trim()
  const commands: Command[] = []
  if (q.startsWith('https://')) commands.push({ id: 'paste-git', label: `Deploy ${q}`, icon: 'git', hint: 'Git URL', run: () => create({ name: slug(q), source: 'git', repo: q }) })
  else if (q && !q.includes(' ') && (q.includes(':') || q.includes('/')) && imageLike.test(q))
    commands.push({ id: 'paste-image', label: `Deploy ${q}`, icon: 'box', hint: 'Docker image', run: () => create({ name: slug(q.split('/').pop()!), source: 'image', image: q }) })

  if (gh && !gh.connected) commands.push({ id: 'gh', label: 'Connect GitHub', icon: 'github', group: 'Deploy', run: () => connectGitHub().catch((e) => setError(e.message)) })
  else if (gh && !gh.installations?.length)
    commands.push({ id: 'gh', label: 'Choose GitHub repositories', icon: 'github', group: 'Deploy', run: () => (location.href = `https://github.com/apps/${gh.app_slug}/installations/new`) })
  for (const c of [
    { id: 'git', label: 'Public Git URL', icon: 'git' },
    { id: 'image', label: 'Docker image', icon: 'box' },
  ] as const)
    if (matches(c.label, q)) commands.push({ ...c, group: 'Deploy', run: () => open(c.id) })
  for (const d of databases) if (matches(d.label, q) || matches('database', q)) commands.push({ id: d.id, label: d.label, icon: 'database', group: 'Database', run: () => open(d.id, d.id) })
  for (const r of (repos ?? []).filter((r) => matches(r.full_name, q)).slice(0, 50))
    commands.push({
      id: 'repo-' + r.full_name,
      label: r.full_name,
      icon: 'github',
      hint: (r.private ? 'private · ' : '') + r.default_branch,
      group: 'GitHub repositories',
      run: () => create({ name: slug(r.full_name), source: 'github', repo: r.full_name, branch: r.default_branch }),
    })
  if (gh?.installations?.length)
    commands.push({ id: 'gh-add', label: 'Add more repositories on GitHub', icon: 'external', group: 'GitHub repositories', run: () => window.open(`https://github.com/apps/${gh.app_slug}/installations/new`, '_blank', 'noopener') })

  const isDb = step === 'postgres' || step === 'mysql' || step === 'redis' || step === 'mongo'
  const fallback = isDb ? step : slug(step === 'image' ? value.split('/').pop() || '' : value)

  return (
    <Modal onClose={onClose} label="New service">
      {step === null ? (
        <>
          <CommandList commands={commands} query={query} onQuery={setQuery} placeholder="Repo, image or database…" />
          {error && <p className="error item">{error}</p>}
        </>
      ) : (
        <form
          className="modal-form"
          onSubmit={(e) => {
            e.preventDefault()
            const n = name || fallback
            if (step === 'git') create({ name: n, source: 'git', repo: value })
            else if (step === 'image') create({ name: n, source: 'image', image: value })
            else create({ name: n, source: step })
          }}
        >
          <div className="row">
            <button type="button" className="ghost icon" onClick={() => open(null)} aria-label="Back">
              <Icon name="arrow" />
            </button>
            <h2>{step === 'git' ? 'Git URL' : step === 'image' ? 'Docker image' : databases.find((d) => d.id === step)!.label}</h2>
          </div>
          {step === 'git' && (
            <label>
              Public repository URL
              <input autoFocus value={value} onChange={(e) => setValue(e.target.value)} placeholder="https://github.com/owner/repo" required />
            </label>
          )}
          {step === 'image' && (
            <label>
              Image
              <input autoFocus value={value} onChange={(e) => setValue(e.target.value)} placeholder="nginx:alpine" required />
            </label>
          )}
          <label>
            Name
            <input autoFocus={isDb} value={name} onChange={(e) => setName(e.target.value)} placeholder={fallback || 'my-app'} />
          </label>
          {error && <p className="error">{error}</p>}
          <div>
            <button className="primary" disabled={busy}>
              {isDb ? 'Create database' : step === 'image' ? 'Deploy' : 'Create'}
            </button>
          </div>
        </form>
      )}
    </Modal>
  )
}
