// OSS Railway site. No libraries, no tracking. Everything degrades to readable static HTML.
const $ = (s, r = document) => r.querySelector(s)
const $$ = (s, r = document) => [...r.querySelectorAll(s)]
const reduced = matchMedia('(prefers-reduced-motion: reduce)').matches

// ---- scroll reveal ----
const seen = 'IntersectionObserver' in window && new IntersectionObserver((entries) => {
  for (const e of entries) if (e.isIntersecting) { e.target.classList.add('in'); seen.unobserve(e.target) }
}, { rootMargin: '0px 0px -8% 0px' })
$$('.reveal').forEach((el) => (seen ? seen.observe(el) : el.classList.add('in')))

// ---- mobile menu ----
const menu = $('.menu')
menu?.addEventListener('click', () => {
  const open = $('.links').classList.toggle('open')
  menu.setAttribute('aria-expanded', open)
})
$$('.links a').forEach((a) => a.addEventListener('click', () => $('.links').classList.remove('open')))

// ---- count-up numbers ----
$$('[data-count]').forEach((el) => {
  const target = Number(el.dataset.count)
  const prefix = el.dataset.prefix || ''
  const suffix = el.dataset.suffix || ''
  const show = (n) => (el.textContent = prefix + Math.round(n) + suffix)
  if (reduced || !('IntersectionObserver' in window)) return show(target)
  show(0)
  const io = new IntersectionObserver((es) => {
    if (!es[0].isIntersecting) return
    io.disconnect()
    const start = performance.now()
    const tick = (t) => {
      const p = Math.min(1, (t - start) / 1100)
      show(target * (1 - Math.pow(1 - p, 3)))
      if (p < 1) requestAnimationFrame(tick)
    }
    requestAnimationFrame(tick)
  })
  io.observe(el)
})

// ---- copy buttons ----
async function copy(btn, text) {
  const label = btn.textContent
  try { await navigator.clipboard.writeText(text); btn.textContent = 'Copied' } catch { btn.textContent = 'Press Ctrl+C' }
  setTimeout(() => (btn.textContent = label), 1500)
}

// ---- install terminal: tabs + typing ----
const installs = {
  vps: {
    cmd: 'curl -fsSL https://raw.githubusercontent.com/KumarRoushan9234/OSS-Railway/main/install.sh | sh',
    out: ['==> Checking system', '==> System: ubuntu amd64', '==> Docker already installed', '==> Pulling ghcr.io/kumarroushan9234/oss-railway:latest', '==> Starting panel', '', 'OSS Railway is running.', '  Open:        https://203-0-113-7.sslip.io', '  Setup code:  XXXX-XXXX'],
  },
  laptop: {
    cmd: 'npx oss-railway setup root@203.0.113.7',
    out: ['==> Connecting over SSH', '==> Running the installer on 203.0.113.7', '==> Pulling ghcr.io/kumarroushan9234/oss-railway:latest', '==> Starting panel', '', 'OSS Railway is running.', '  Open:        https://203-0-113-7.sslip.io', '  Setup code:  XXXX-XXXX'],
  },
  docker: {
    cmd: 'docker run -d --name ossr-panel --network ossr -v /var/run/docker.sock:/var/run/docker.sock -v ossr-data:/data ghcr.io/kumarroushan9234/oss-railway',
    out: ['(see "Try it locally" in the README for the full command)', '', 'Open http://localhost, enter the setup code,', 'create your account.'],
  },
}
const termEl = $('#term-out')
let typing = 0
function typeInstall(key) {
  if (!termEl) return
  const { cmd, out } = installs[key]
  const run = ++typing
  const esc = (s) => s.replace(/[&<>]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' })[c])
  const paint = (typed, lines) =>
    (termEl.innerHTML = `<span class="p">$</span> ${esc(typed)}${typed.length < cmd.length ? '<span class="caret"></span>' : ''}\n` +
      lines.map((l) => (l.startsWith('==>') ? `<span class="c">${esc(l)}</span>` : /running|Open|Setup/.test(l) ? `<span class="ok">${esc(l)}</span>` : esc(l))).join('\n'))
  $('#copy-install')?.setAttribute('data-text', cmd)
  if (reduced) return paint(cmd, out)
  let i = 0
  const typeNext = () => {
    if (run !== typing) return
    paint(cmd.slice(0, i), [])
    if (i++ < cmd.length) return setTimeout(typeNext, 14)
    let n = 0
    const lineNext = () => {
      if (run !== typing) return
      paint(cmd, out.slice(0, ++n))
      if (n < out.length) setTimeout(lineNext, 280)
    }
    setTimeout(lineNext, 400)
  }
  typeNext()
}
let picked = false // the visitor chose a tab: don't let the auto-start override it
$$('.tab[data-install]').forEach((t) =>
  t.addEventListener('click', () => {
    picked = true
    $$('.tab[data-install]').forEach((x) => x.setAttribute('aria-selected', x === t))
    typeInstall(t.dataset.install)
  }),
)
if (termEl) {
  const io = 'IntersectionObserver' in window ? new IntersectionObserver((es) => { if (es[0].isIntersecting) { io.disconnect(); if (!picked) typeInstall('vps') } }, { threshold: 0.4 }) : null
  io ? io.observe(termEl) : typeInstall('vps')
}
$$('[data-copy-text], #copy-install').forEach((b) => b.addEventListener('click', () => copy(b, b.dataset.text || b.dataset.copyText)))

// ---- "four ways" tabs ----
const ways = {
  github: {
    title: 'Push to deploy',
    code: '# Settings → Connect GitHub (once)\n# New → pick your repo → Save and deploy\n\ngit push origin main\n# the panel pulls, builds, health-checks,\n# then switches traffic. Old version keeps\n# serving until the new one is ready.',
    get: ['Builds with Railpack: no Dockerfile needed', 'Private repos supported', 'One-click rollback to the last 3 builds', 'Build and runtime logs, searchable for 7 days'],
  },
  image: {
    title: 'Any Docker image',
    code: '# New → type or paste an image\nnginx:alpine\nghcr.io/you/app:1.4\n\n# Settings → Resources\nCPU limit    0.5 cores\nMemory limit 256 MB',
    get: ['HTTPS address in seconds, no DNS setup', 'CPU and memory limits applied live', 'Suggested limits from real usage', 'Restart policy and crash alerts'],
  },
  db: {
    title: 'A database, linked',
    code: '# New → Postgres  (or MySQL, Redis, MongoDB)\n# Drag a line from the database to your app\n\nDATABASE_URL=${{db.DATABASE_URL}}\nREDIS_URL=${{cache.REDIS_URL}}',
    get: ['Passwords generated and stored encrypted', 'Daily Postgres and MySQL backups', 'Volume kept when you redeploy', 'Reachable only on the private network'],
  },
  agent: {
    title: 'Let an AI agent in',
    code: '# Settings → API tokens → Create (read-only)\nclaude mcp add --transport http oss-railway \\\n  https://your-panel/api/mcp \\\n  --header "Authorization: Bearer ossr_…"\n\n> why did web crash last night?',
    get: ['Tools: list services, status, logs, server overview', 'Full-access tokens can also deploy and restart', 'Read-only tokens can never change anything', 'Every agent action lands in the audit log'],
  },
}
function showWay(key) {
  const w = ways[key]
  if (!w || !$('#way-code')) return
  $('#way-code').textContent = w.code
  $('#way-title').textContent = w.title
  $('#way-get').innerHTML = w.get.map((g) => `<li>${g}</li>`).join('')
  $$('.tab[data-way]').forEach((t) => t.setAttribute('aria-selected', t.dataset.way === key))
}
$$('.tab[data-way]').forEach((t) => t.addEventListener('click', () => showWay(t.dataset.way)))
showWay('github')

// ---- quiz ----
const answers = {}
const advice = {
  where: {
    vps: ['Run the install command on the server. HTTPS and a free <code>sslip.io</code> address come with it.', ['Caddy HTTPS']],
    home: ['Install on the machine, then Settings → <b>Remote access</b> to open it through a Cloudflare Tunnel. No open ports needed.', ['Cloudflare Tunnel']],
    none: ['Rent any Linux VPS with 1 GB RAM or more, then run the install command on it.', ['Caddy HTTPS']],
  },
  what: {
    repo: ['Settings → <b>Connect GitHub</b>, pick the repo, <b>Save and deploy</b>. Every push redeploys.', ['GitHub App', 'Push to deploy']],
    image: ['<b>New</b>, paste the image name, done. Add variables if it needs them.', ['Docker image']],
    db: ['<b>New</b> → Postgres, MySQL, Redis or MongoDB, then drag a line to your app to link it.', ['Databases', 'Backups']],
    nodock: ['<b>New</b> → paste the Git URL. Railpack detects the language and builds it for you.', ['Railpack']],
  },
  who: {
    me: ['Turn on <b>two-factor</b> or add a <b>passkey</b> in Settings.', ['2FA', 'Passkeys']],
    team: ['There is one admin account today. Share read-only <b>API tokens</b>, and watch the <b>Activity</b> log. Team accounts are on the roadmap.', ['API tokens', 'Audit log']],
    ai: ['Create an API token, then <code>claude mcp add</code> from Settings. Agents can read logs and, with a full token, deploy.', ['MCP', 'API tokens']],
  },
}
function renderQuiz() {
  const box = $('#quiz-result')
  if (!box) return
  const order = ['where', 'what', 'who']
  const picked = order.filter((k) => answers[k])
  if (!picked.length) return
  const steps = picked.map((k) => advice[k][answers[k]][0])
  const tags = [...new Set(picked.flatMap((k) => advice[k][answers[k]][1]))]
  if (picked.length === 3) steps.push('Add an email or Slack webhook in Settings so a crash reaches you.') && tags.push('Alerts')
  $('#quiz-steps').innerHTML = steps.map((s) => `<li>${s}</li>`).join('')
  $('#quiz-tags').innerHTML = tags.map((t) => `<span class="tag">${t}</span>`).join('')
  $('#quiz-title').textContent = picked.length === 3 ? 'Your path' : 'Keep going…'
}
$$('.opt').forEach((o) =>
  o.addEventListener('click', () => {
    const q = o.dataset.q
    answers[q] = o.dataset.v
    $$(`.opt[data-q="${q}"]`).forEach((x) => x.setAttribute('aria-pressed', x === o))
    renderQuiz()
  }),
)

// ---- feature directory filter ----
const chips = $$('.chip')
chips.forEach((c) =>
  c.addEventListener('click', () => {
    chips.forEach((x) => x.setAttribute('aria-pressed', x === c))
    const cat = c.dataset.cat
    $$('table.dir tbody tr').forEach((r) => (r.hidden = cat !== 'all' && r.dataset.cat !== cat))
  }),
)

// ---- chart backgrounds: any element with data-chart="line|multi|bars|scatter" gets a plot behind its content ----
function rng(seed) {
  let a = (seed * 2654435761) >>> 0
  return () => {
    a = (a + 0x6d2b79f5) | 0
    let t = Math.imul(a ^ (a >>> 15), 1 | a)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}
function chartSvg(kind, seed, stretch) {
  const W = 1200, H = 600, N = 26, r = rng(seed)
  const f = (n) => n.toFixed(1)
  const grid = []
  for (let i = 1; i < 6; i++) grid.push(`<line class="g${i % 2 ? ' d' : ''}" x1="0" x2="${W}" y1="${f((H * i) / 6)}" y2="${f((H * i) / 6)}"/>`)
  if (kind !== 'bars') for (let i = 1; i < 12; i++) grid.push(`<line class="g d" x1="${f((W * i) / 12)}" x2="${f((W * i) / 12)}" y1="0" y2="${H}"/>`)
  const labels = ['00:00', '04:00', '08:00', '12:00', '16:00', '20:00'].map((t, i) => `<text x="${40 + (i * W) / 6}" y="${H - 14}">${t}</text>`).join('')
  const series = (base, slope, noise) => Array.from({ length: N }, (_, i) => [(W * i) / (N - 1), H * (base - (slope * i) / (N - 1) + (r() - 0.5) * noise)])
  const path = (pts) => 'M' + pts.map((p) => f(p[0]) + ' ' + f(p[1])).join(' L')
  let body = ''
  if (kind === 'line' || kind === 'multi') {
    const a = series(0.74, 0.42, 0.12)
    body += `<path class="ar" d="${path(a)} L${W} ${H} L0 ${H} Z"/><path class="ln" pathLength="1" d="${path(a)}"/>`
    if (kind === 'multi') body += `<path class="ln b" pathLength="1" d="${path(series(0.55, -0.08, 0.16))}"/>`
    body += a.filter((_, i) => i % 3 === 1).map((p, i) => `<circle class="pt" style="--i:${i}" cx="${f(p[0])}" cy="${f(p[1])}" r="7"/>`).join('')
  } else if (kind === 'bars') {
    const n = 22, bw = (W / n) * 0.62
    body = Array.from({ length: n }, (_, i) => {
      const h = H * (0.12 + 0.6 * ((i / n) * 0.7 + r() * 0.5))
      return `<rect class="bar" style="--i:${i}" x="${f((W / n) * i + (W / n - bw) / 2)}" y="${f(H - h)}" width="${f(bw)}" height="${f(h)}" rx="6"/>`
    }).join('')
  } else {
    const pts = Array.from({ length: 44 }, (_, i) => { const x = (W * i) / 43; return [x, H * (0.78 - 0.5 * (i / 43) + (r() - 0.5) * 0.34)] })
    body = `<path class="ln" pathLength="1" d="${path([pts[0], pts[pts.length - 1]].map((p, i) => [p[0], H * (i ? 0.3 : 0.76)]))}"/>` + pts.map((p, i) => `<circle class="pt" style="--i:${i % 14}" cx="${f(p[0])}" cy="${f(p[1])}" r="${3 + Math.round(r() * 6)}"/>`).join('')
  }
  return `<svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="${stretch ? 'none' : 'xMidYMid slice'}" aria-hidden="true" focusable="false">${grid.join('')}${kind === 'bars' ? '' : labels}${body}</svg>`
}
const chartIO = 'IntersectionObserver' in window && new IntersectionObserver((es) => {
  for (const e of es) if (e.isIntersecting) { e.target.classList.add('in'); chartIO.unobserve(e.target) }
}, { threshold: 0.15 })
$$('[data-chart]').forEach((el) => {
  const bg = document.createElement('div')
  bg.className = 'chart-bg'
  bg.setAttribute('aria-hidden', 'true')
  bg.innerHTML = chartSvg(el.dataset.chart, Number(el.dataset.seed || 1), el.dataset.fit === 'stretch')
  el.prepend(bg)
  chartIO ? chartIO.observe(bg) : bg.classList.add('in')
})

// ---- docs: highlight the section being read ----
const toc = $$('.toc a')
if (toc.length && 'IntersectionObserver' in window) {
  const map = new Map(toc.map((a) => [a.getAttribute('href').slice(1), a]))
  const io = new IntersectionObserver((es) => {
    for (const e of es) if (e.isIntersecting) { toc.forEach((a) => a.classList.remove('on')); map.get(e.target.id)?.classList.add('on') }
  }, { rootMargin: '-20% 0px -70% 0px' })
  $$('.doc article').forEach((a) => io.observe(a))
}
