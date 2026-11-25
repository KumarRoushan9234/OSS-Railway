// OSS Railway desktop UI. Every server call goes through Rust commands (invoke), which add the
// API token from the system keychain; this page never sees a token after it is added.
const { invoke } = window.__TAURI__.core

const $ = (s) => document.querySelector(s)
const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c])
const bytes = (n) => {
  if (!n) return '0 B'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), u.length - 1)
  return (n / 1024 ** i).toFixed(i ? 1 : 0) + ' ' + u[i]
}

let servers = []
let current = null
let timer = 0

async function loadServers() {
  servers = await invoke('list_servers')
  $('#servers').innerHTML = servers
    .map((s) => `<button class="server${s.id === current ? ' active' : ''}" data-id="${esc(s.id)}">${esc(s.name)}</button>`)
    .join('')
  if (!current && servers.length) select(servers[0].id)
  if (!servers.length) renderEmpty()
}

function renderEmpty() {
  $('#main').innerHTML = `<div class="empty"><div class="stack" style="justify-items:center">
    <h1>Add your first server</h1>
    <p class="muted">Any OSS Railway server: a VPS, or a machine at home behind a tunnel.</p>
    <button class="primary" id="empty-add">Add server</button></div></div>`
  $('#empty-add').onclick = openAdd
}

function select(id) {
  current = id
  document.querySelectorAll('.server').forEach((b) => b.classList.toggle('active', b.dataset.id === id))
  clearInterval(timer)
  render()
  timer = setInterval(render, 15000)
}

async function render() {
  const s = servers.find((x) => x.id === current)
  if (!s) return
  const id = s.id
  let info, services, overview
  try {
    ;[info, services, overview] = await Promise.all([invoke('api_get', { id, path: '/api/server' }), invoke('api_get', { id, path: '/api/services' }), invoke('api_get', { id, path: '/api/overview' })])
  } catch (e) {
    $('#main').innerHTML = `<div class="spread"><h1>${esc(s.name)}</h1><button id="remove" class="ghost">Remove</button></div><p class="error">${esc(e)}</p>`
    $('#remove').onclick = () => removeServer(id)
    return
  }
  if (current !== id) return
  const h = info.host
  const pct = (a, b) => (b ? Math.min(100, (a / b) * 100) : 0)
  const open = document.querySelector('.svc[data-open]')?.dataset.svc
  $('#main').innerHTML = `
    <div class="spread">
      <div class="stack" style="gap:4px"><h1>${esc(s.name)}</h1><span class="faint">${esc(s.url)}</span></div>
      <div class="row"><button id="dash" class="primary">Open dashboard</button><button id="remove" class="ghost">Remove</button></div>
    </div>
    <section class="tiles">
      <div class="card tile"><span class="muted">CPU</span><b>${h.cpus} cores</b><span class="faint">${info.allocation ? (Math.round(info.allocation.cpu_limited * 100) / 100) + ' allocated to services' : ''}</span></div>
      <div class="card tile"><span class="muted">Memory</span><b>${bytes(h.mem)}</b><div class="bar"><div style="width:${pct(h.mem, h.mem_total)}%"></div></div><span class="faint">of ${bytes(h.mem_total)}</span></div>
      <div class="card tile"><span class="muted">Disk</span><b>${bytes(h.disk)}</b><div class="bar"><div style="width:${pct(h.disk, h.disk_total)}%"></div></div><span class="faint">of ${bytes(h.disk_total)}</span></div>
    </section>
    <section class="stack" style="gap:8px">
      <h2>Services</h2>
      <div class="card">${services.map((v) => `
        <div class="svc" data-svc="${v.id}">
          <div class="spread">
            <span class="row"><span class="dot ${esc(v.status)}"></span><b>${esc(v.name)}</b><span class="faint">${esc(v.source)} · ${esc(v.status)}</span></span>
            <span class="row">
              ${v.urls[0] ? `<span class="faint mono">${esc(v.urls[0])}</span>` : ''}
              <button data-act="logs">Logs</button>
              <button data-act="analyze">Analyze</button>
              ${v.kind === 'app' ? '<button data-act="deploy">Deploy</button>' : ''}
              <button data-act="restart" class="ghost">Restart</button>
            </span>
          </div>
          <div class="out"></div>
        </div>`).join('') || '<p class="svc muted">No services yet. Open the dashboard to deploy one.</p>'}
      </div>
    </section>
    ${overview.alerts.length ? `<section class="stack" style="gap:8px"><h2>Recent alerts</h2><div class="card">${overview.alerts.map((a) => `<div class="svc"><span>${esc(a.message)}</span></div>`).join('')}</div></section>` : ''}
    <section class="stack" style="gap:8px"><h2>Listening ports</h2><div class="card logs" style="max-height:200px">${info.ports.map((p) => `${String(p.port).padEnd(6)} ${esc(p.address).padEnd(16)} ${esc(p.process || '')} ${esc(p.container || '')}`).join('\n')}</div></section>`
  $('#dash').onclick = () => invoke('open_dashboard', { id }).catch((e) => alert(e))
  $('#remove').onclick = () => removeServer(id)
  if (open) document.querySelector(`.svc[data-svc="${open}"]`)?.setAttribute('data-open', '')
}

$('#main').addEventListener('click', async (e) => {
  const btn = e.target.closest('button[data-act]')
  if (!btn) return
  const row = btn.closest('.svc')
  const service = Number(row.dataset.svc)
  const out = row.querySelector('.out')
  const act = btn.dataset.act
  btn.disabled = true
  out.innerHTML = '<span class="faint">Working…</span>'
  try {
    if (act === 'logs') {
      const hits = await invoke('api_get', { id: current, path: `/api/logs/search?service=${service}` })
      out.innerHTML = `<div class="logs">${hits.reverse().map((h) => `<span class="faint">${new Date(h.ts).toLocaleTimeString()}</span> ${esc(h.line)}`).join('\n') || 'No logs stored yet.'}</div>`
    } else if (act === 'analyze') {
      out.innerHTML = '<span class="faint">Asking the AI model on this computer…</span>'
      out.innerHTML = `<div class="answer">${esc(await invoke('analyze', { id: current, service }))}</div>`
    } else {
      await invoke('service_action', { id: current, service, action: act })
      out.innerHTML = `<span class="muted">${act === 'deploy' ? 'Deploy started.' : 'Restarted.'}</span>`
    }
  } catch (err) {
    out.innerHTML = `<p class="error">${esc(err)}</p>`
  } finally {
    btn.disabled = false
  }
})

async function removeServer(id) {
  if (!confirm('Remove this server from the app? Its token is deleted from the keychain. Nothing changes on the server.')) return
  await invoke('remove_server', { id })
  current = null
  loadServers()
}

function openAdd() {
  $('#add-form').reset()
  $('#add-error').textContent = ''
  $('#add-dialog').showModal()
}

$('#add').onclick = openAdd
$('#servers').addEventListener('click', (e) => {
  const b = e.target.closest('.server')
  if (b) select(b.dataset.id)
})

$('#add-form').addEventListener('submit', async (e) => {
  if (e.submitter?.value !== 'add') return
  e.preventDefault()
  const f = new FormData(e.target)
  const btn = e.submitter
  btn.disabled = true
  $('#add-error').textContent = ''
  try {
    const s = await invoke('add_server', { name: f.get('name'), url: f.get('url'), token: f.get('token') })
    $('#add-dialog').close()
    current = null
    await loadServers()
    select(s.id)
  } catch (err) {
    $('#add-error').textContent = err
  } finally {
    btn.disabled = false
  }
})

$('#settings').onclick = async () => {
  const s = await invoke('get_settings')
  const f = $('#settings-form')
  f.ai_url.value = s.ai_url
  f.ai_model.value = s.ai_model
  $('#settings-error').textContent = ''
  $('#settings-dialog').showModal()
}

$('#settings-form').addEventListener('submit', async (e) => {
  if (e.submitter?.value !== 'save') return
  e.preventDefault()
  const f = new FormData(e.target)
  try {
    await invoke('set_settings', { settings: { ai_url: f.get('ai_url'), ai_model: f.get('ai_model') } })
    $('#settings-dialog').close()
  } catch (err) {
    $('#settings-error').textContent = err
  }
})

loadServers()
