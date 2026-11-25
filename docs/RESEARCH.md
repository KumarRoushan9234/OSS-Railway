# Self-hosted Railway on your own VPS: research and plan

Date: 2026-11-17

## 1. Verdict

**Yes, you can build this, and it can run reliably.** The pieces it needs already exist as open-source building blocks:

| Need | Proven building block |
|---|---|
| Build an app that has no Dockerfile | **Railpack** (Railway's own builder, open source, BuildKit-based). Nixpacks is now in maintenance mode. Cloud Native Buildpacks (Paketo) as a fallback. |
| Run containers | Docker Engine API (talk to the socket directly) |
| HTTPS + domains | Caddy (automatic TLS) or Traefik |
| Databases with a few clicks | Official Postgres/MySQL/Redis/Mongo images + managed volumes + scheduled `pg_dump`-style backups |
| Metrics | cgroup v2 stats + Docker stats API + `/proc` for the host |
| Crash detection | Docker events stream (`die`, `oom`, `health_status: unhealthy`) |
| GitHub deploys | GitHub App + push webhooks. No GitHub Actions needed. |

**It already exists in part:** Coolify, Dokploy, CapRover, Dokku, Kamal and newer projects like Temps. You would not be first. To win you need to be clearly **lighter, easier for beginners, and better at monitoring and alerts**. Section 4 shows where that room is.

---

## 2. Your main question: what about beginners with no Docker, no Dockerfile and no CI?

### 2a. No Docker on the server
This part is easy. The installer checks the OS (Ubuntu/Debian/RHEL family), installs Docker Engine from Docker's official repo, enables cgroup v2, adds swap if RAM is under 2 GB, opens ports 80/443 in the firewall, and starts the panel. Coolify and Dokploy already do this with a one-line `curl | sh`.

**On `npx`:** a fresh VPS has no Node, so `npx` cannot be the installer on the server itself. Support two ways in:
- On the VPS: `curl -fsSL https://<yourdomain>/install.sh | sh`
- **From the user's laptop (new):** `npx <yourtool> setup root@1.2.3.4`. It connects over SSH, runs the same installer, sets up a firewall and SSH-key login, and opens the dashboard in the browser through an SSH tunnel. Beginners never have to touch the server shell. No existing tool does this well.

### 2b. No Dockerfile
This is the hard part, and it is solvable for most apps. Build pipeline, in priority order:

1. A `Dockerfile` exists → use it.
2. A `docker-compose.yml` exists → deploy it as a stack.
3. Otherwise **Railpack** detects the language and framework (Node, Python, Go, PHP, Java, Ruby, .NET, Deno, Rust, Elixir, Gleam, static HTML, shell) and builds an OCI image with BuildKit. This is exactly what Railway runs in production, so it is well tested.
4. Railpack fails → try Paketo Buildpacks.
5. Still failing → **optional AI step**: read the repo and the build log, *generate* a Dockerfile, show it to the user, and only build after they approve. Commit it back to the repo if they want. This turns a dead end into a one-click fix.

**Where it breaks, and the fix for each case:**

| Failure | How common | Fix |
|---|---|---|
| Build runs out of RAM on a 1–2 GB VPS (Next.js, Rust, Java) | Very common | Auto swap, build memory limit, one build at a time, optional "build on my laptop" (`npx <tool> deploy` builds locally and pushes the image) |
| Wrong port | Common | Read `PORT` from the framework, scan logs for "listening on :NNNN", probe open ports in the container |
| Missing env vars / secrets | Common | Scan the code for `process.env.X` / `os.environ["X"]` / `.env.example` and ask for them **before** the first deploy |
| App writes files to local disk (uploads, SQLite) | Common | Detect it and offer a persistent volume |
| Monorepo | Medium | Ask for the root folder; Railpack supports subpaths |
| Native or system dependencies | Medium | Railpack config `packages`, otherwise AI-generated Dockerfile |
| DB migrations | Medium | "Pre-deploy command" field, prefilled for Prisma, Django, Rails, Laravel |
| App crashes after starting | Any | Health check + keep the old container running until the new one is healthy (zero-downtime), automatic rollback |

**Honest reliability statement:** for mainstream web stacks (Node/Next, Python/Django/FastAPI, Go, PHP/Laravel, static sites), auto-build works most of the time, and Railway's production use is the evidence. For unusual stacks, the Dockerfile fallback with AI help covers the rest. No source publishes success rates, so measure your own: keep a test suite of ~50 real public repos and run them through the pipeline on every release.

### 2c. No GitHub Actions
You don't need Actions. The server builds the code itself: GitHub App → push webhook → panel pulls the commit → builds → deploys. Actions only matters for users who want to build elsewhere to save server RAM, and you can support that as an extra option.

---

## 3. What exists today

| Project | Stack | Strengths | Weaknesses |
|---|---|---|---|
| **Coolify** | PHP/Laravel + Livewire, Postgres, Redis, realtime server, Traefik | Biggest one-click catalog, mature, Apache-2.0 | Heavy: 2 GB min documented; idle reports of 500 MB–1.2 GB RAM and ~5–7% CPU; Traefik CPU complaints; slow lists; weak per-app metrics |
| **Dokploy** | TypeScript/Next.js, Postgres, Redis, Traefik, Swarm | Lighter (~350–600 MB idle reported), multi-node via Swarm, notifications (Email/Slack/Discord/Telegram…), monitoring | Mixed license (Apache-2.0 + DSAL); SSO/audit logs are Enterprise; monitoring retention is short on the free tier |
| **CapRover** | Node, Docker Swarm | Mature, simple | Development has slowed; Compose support is only partial |
| **Dokku** | Bash, buildpacks | Very light (1 GB), stable | CLI-first, no real dashboard in OSS, no Compose |
| **Kamal** | Ruby CLI | No control plane, zero overhead | No dashboard, no DB management, not for beginners |
| **Temps** | Single Rust binary | Includes analytics, error tracking, uptime | New, mostly self-promotion, no independent reviews yet |
| **Easypanel** | Closed source | Polished UI, offers Railpack | Not OSS |

Idle RAM and CPU figures come from third-party blogs with unclear methods. Measure them yourself with `docker stats` before you put numbers in marketing.

### Why Coolify feels laggy (architecture, not bugs)
- Many always-on parts: PHP app, queue workers, Postgres, Redis, websocket server, Traefik proxy, plus scheduled jobs.
- Coolify manages even its own host over SSH, so every action goes through a shell round trip instead of a local API call.
- Livewire UI: every interaction is a server round trip that re-renders HTML.
- Metrics and cleanup run on a schedule, so CPU spikes every few minutes.

---

## 4. How to be clearly better

1. **One static Go binary + embedded SQLite.** No Postgres or Redis for the panel itself. Talk to the Docker socket directly, with no SSH to localhost. Target: **under 60 MB RAM and ~0% CPU at idle**. Measure it in CI and publish the number.
2. **Caddy as the proxy** (or a built-in proxy) for automatic HTTPS with very little config. Fewer moving parts than Traefik + labels.
3. **Server-Sent Events for live logs and metrics**, a static SPA frontend served from the binary. Nothing renders on the server per click.
4. **Beginner flow first:** `npx <tool> setup root@ip` from the laptop → connect GitHub → pick a repo → env vars detected → deployed. Aim for under 5 minutes, with no shell needed.
5. **Build preflight:** before building, show what was detected ("Next.js 15, Node 22, port 3000, needs DATABASE_URL"), and let the user fix it in one place.
6. **Monitoring that is actually good (your differentiator):**
   - Host: CPU, RAM, disk, disk I/O, network, load, inodes, swap. Per container: CPU, RAM, restarts, OOM kills.
   - Store as downsampled time series in SQLite (1s → 1m → 1h), 30+ days by default. No Prometheus needed.
   - HTTP metrics from the proxy: requests, p50/p95 latency, 5xx rate per app.
   - **Alert rules:** container crashed or crash-looping, OOM, health check failing, disk >85%, RAM >90% for 5 min, TLS cert expiring, backup failed, deploy failed. Email (SMTP or a provider) + webhook + Slack/Discord/Telegram.
   - **Scheduled reports:** daily or weekly email with uptime per app, deploys, incidents, resource trends, disk forecast ("disk full in ~12 days"), and backup status.
7. **Databases done properly:** one click for Postgres, MySQL, MariaDB, Redis, MongoDB. Internal-only by default, a connection string injected into linked apps as env vars (like Railway's references), scheduled backups to local disk + S3-compatible storage, and **a tested restore button** (most tools skip this). Optional public access through a TCP proxy, with a warning.
8. **Railway-style canvas:** a project graph of services, databases and volumes with their connections. This is the part of the UX that makes Railway feel good.
9. **Everything stays on the box:** no telemetry by default, no required cloud account, backups go only where the user points them.
10. **Clean license:** plain Apache-2.0 or MIT with no "enterprise" split for SSO and audit logs. That answers a common complaint about Dokploy.

---

## 5. Architecture

```
            ┌──────────────── VPS ────────────────────────────┐
 Browser ──►│ Caddy :80/:443 ──► apps (containers)            │
   SSE      │      │                                          │
            │      └──► panel (Go binary, :internal)          │
            │            ├─ SQLite (state, metrics, logs idx) │
            │            ├─ Docker socket (deploy, events)    │
            │            ├─ BuildKit + Railpack (builds)      │
            │            ├─ Collector (cgroups, /proc)        │
            │            ├─ Alerter + Scheduler (reports)     │
            │            └─ Backup worker → disk / S3         │
            └──────────────────────────────────────────────────┘
 GitHub ── webhook ──► panel      Laptop ── npx CLI ── SSH ──► installer
```

Security basics (required, because access to the Docker socket is root-equivalent):
- Panel bound to localhost behind Caddy, forced HTTPS, 2FA, rate-limited login.
- Secrets encrypted at rest (key in a root-only file), never shown in logs.
- GitHub App with the narrowest scopes; verify webhook signatures.
- Signed release binaries; the installer checks the checksum.
- Build containers run with no Docker socket and with resource limits.

---

## 6. Roadmap

| Phase | Scope | Done when |
|---|---|---|
| **0. Spike (1–2 wks)** | Go binary: deploy an image, Caddy route, live logs | Deploy `nginx` with HTTPS from the UI; idle RAM measured |
| **1. MVP** | Installer (curl + npx-over-SSH), GitHub App, Railpack builds, env vars, domains, health checks, rollback | 20 of 25 test repos deploy without a Dockerfile |
| **2. Databases** | Postgres/MySQL/Redis/Mongo, linking, backups, restore | Restore tested automatically in CI |
| **3. Observability** | Metrics store, dashboards, alert rules, email/webhooks, crash and OOM detection | Kill a container → email arrives within 60 s |
| **4. Reports** | Scheduled daily/weekly reports, disk forecast | Report email shows correct data |
| **5. Polish** | Project canvas, templates catalog, AI Dockerfile fallback, preview deploys per PR | 45 of 50 test repos deploy |
| **6. Later** | Multi-server (agent on extra nodes), teams/RBAC | — |

## 7. Risks
- **Crowded market:** Coolify and Dokploy have large communities. Lead on lightness, onboarding and monitoring, not feature count.
- **Builds on small VPSes** will be the main source of "it doesn't work" reports. Build-on-laptop and swap are essential, not optional.
- **Maintenance burden:** framework detection breaks as frameworks change. Using Railpack upstream instead of writing your own detectors keeps this manageable.
- **Security:** one panel vulnerability exposes the whole server. Plan for a security review before 1.0.

## 8. Problems with each existing tool

| Tool | Main problems for a "buy a VPS, deploy like Railway" user |
|---|---|
| **Kamal** | Not a PaaS: no dashboard, no GitHub integration, no one-click databases. You write `deploy.yml`, need a container registry, and run it from your laptop. Two people deploying at once can collide. Anyone with SSH access and the config file can deploy. Databases as "accessories" are hard to get right, with no backups or restore built in. |
| **CapRover** | Built on Docker Swarm, which Docker has put in maintenance mode (a long-term risk); reports of Swarm issues with newer Docker releases. Development has slowed and its stated scope is narrow. Per-app Nginx templates confuse newcomers. Only partial Compose support. No PR preview environments. Basic monitoring. |
| **Dokku** | No web UI in the open-source version (the UI is Dokku Pro, about $849). Everything goes through CLI and SSH. No Docker Compose. Single server by design (multi-node needs K3s). No user roles. No previews. Beginners struggle with it. |
| **Temps** | Very young (first commit Oct 2025). Advertised as a "single binary", but production setup still needs PostgreSQL (+ Redis in the Compose setup). Very broad scope (analytics, session replay, email, error tracking), which means a lot of surface area to keep stable. License listed differently in different places (MIT/Apache vs AGPL), so check the repo. Almost no independent reviews. |
| **Coolify** | Heavy (2 GB minimum, many always-on parts). Manages even its own host over SSH. Laggy Livewire UI. Traefik CPU complaints. Serious security history: a wave of critical CVEs in Jan 2026 (auth bypass, RCE, exposed root SSH key) with ~50k instances exposed, more advisories through late 2026. Stable v4 only shipped Apr 2026. Weak per-app metrics. |
| **Dokploy** | Lighter than Coolify, but uses Swarm for multi-node (same Swarm risk). Mixed license, with SSO, audit logs and roles behind Enterprise. Short metrics retention on the free tier. |

**Shared gaps across all of them:**
1. Setup always ends with "now go configure DNS, a domain, and SMTP yourself". Nobody gets a beginner from a blank VPS to an HTTPS app in one flow.
2. No tool can explain *why* a build failed and fix it.
3. Monitoring is basic, alerts are an afterthought, and none have scheduled reports.
4. Database backups exist, but **restore is rarely tested**.
5. No server hardening. Most leave SSH passwords, missing OS updates and open ports as they are.
6. Little thought for AI agents (no MCP server or clean CLI and API for agents).

---

## 9. One-command onboarding (the core experience)

**Step 1: one command.** On the VPS:
```
curl -fsSL https://get.<name>.dev | sh
```
or from the laptop: `npx <name> setup root@1.2.3.4`.
The installer, in under 2 minutes:
- checks the OS, CPU, RAM and disk, and refuses clearly on anything unsupported
- installs Docker, adds swap on small machines, enables the firewall (22/80/443 only), turns on unattended security updates
- installs one binary + a systemd service. Caddy is embedded or runs as a managed container. The user never configures it.
- prints: `Open https://1-2-3-4.sslip.io  — setup code: 7F3K-92QD`

Key points:
- **Instant HTTPS with no domain** using an IP-based hostname (sslip.io style), so the first page is already secure.
- **Setup code printed in the terminal.** Only the person who ran the install can claim the admin account. Coolify-style "first visitor becomes admin" pages are a known risk.

**Step 2: create the admin account** (password + optional 2FA).

**Step 3: connect GitHub in one click.** Use GitHub's *App Manifest* flow: the panel sends the user to GitHub with a prefilled app (name, webhook URL, permissions), the user clicks "Create", and GitHub sends back the keys. No copy-pasting secrets. GitLab, Gitea and public Git URLs come later.

**Step 4 (optional): domain.** The user types `example.com`. The panel shows the exact DNS record to add (`A *.example.com → 1.2.3.4`), checks it live, and switches every app to `app.example.com` with automatic HTTPS. A wildcard record means every new app and PR preview gets a URL with no extra steps.

**Step 5: deploy anything from the same screen:**
- GitHub repo (with or without a Dockerfile; see section 2b)
- Docker image (`ghcr.io/...`)
- docker-compose file (paste it or point to it in the repo)
- template (Postgres, Redis, n8n, Plausible, Umami…)
- **plain-language instruction** (optional AI): "deploy my repo `shop-api` with Postgres and Redis on api.example.com". It produces a plan the user reviews, then applies it.

**Step 6: alerts.** Add an email (SMTP or a provider API key) and press "Send test", plus optional Slack/Discord/Telegram/webhook. Default alert rules are on from the start.

---

## 10. Features to offer beyond the others

**Setup and server**
- Server health score + hardening checklist with "Fix" buttons: SSH keys only, disable root password login, fail2ban, updates, open-port scan.
- `<name> doctor`: one command that checks Docker, disk, DNS, certificates, proxy and GitHub webhook delivery, and explains each failure in plain words.
- Self-update with automatic rollback if the new version fails its health check.
- Backup of the panel itself (one SQLite file + secrets) to S3, and "restore this panel onto a new VPS" (disaster recovery and server moves).

**Deploys**
- Build explainer: when a build fails, show the cause in one line ("Out of memory during `next build`") and offer a fix (add swap, build on laptop, generate Dockerfile).
- Zero-downtime deploys, health checks, one-click rollback to any previous image.
- PR preview environments with a comment on the PR containing the URL; deleted when the PR closes.
- Cron jobs and background workers as first-class services.
- Import from Railway / Heroku / Render / Coolify: read `Procfile`, `railway.json`, `render.yaml` and `docker-compose.yml`.
- Web terminal into a container, plus log search.

**Databases**
- One click: Postgres, MySQL, MariaDB, Redis, MongoDB, ClickHouse later.
- Auto-link to apps as env vars (`${{postgres.DATABASE_URL}}` like Railway).
- Scheduled backups to disk and S3, **automatic weekly restore test** into a throwaway container, with the result shown in reports.
- Simple query viewer and size/connection metrics.

**Observability (main differentiator)**
- Host + per-container metrics with 30+ days of history, request rate, latency and error rate from the proxy.
- Alerts: crash, crash loop, out of memory, health check failing, high CPU/RAM/disk, expiring certificate, failed backup, failed deploy, uptime check down.
- Daily/weekly email report: uptime, deploys, incidents, top resource users, disk-full forecast, backup and restore-test status.
- Simple status page per project (optional public).

**For developers and AI agents**
- Full REST API + CLI (`<name> deploy`, `<name> logs`, `<name> env set`) + **an MCP server**, so Claude or other agents can deploy, read logs and roll back safely, with scoped tokens.
- Config as code: optional `<name>.toml` in the repo that the UI reads and writes.

**Teams (free, no Enterprise tier)**
- Roles (owner, deployer, viewer), audit log, SSO through OIDC.

---

## Sources
- [Kamal (GitHub)](https://github.com/basecamp/kamal) · [Kamal vs PaaS (Judoscale)](https://judoscale.com/blog/kamal-vs-paas) · [Migrating from Kamal (DeployHQ, vendor)](https://www.deployhq.com/support/migrating-to-deployhq/migrating-from-kamal-to-deployhq)
- [CapRover (GitHub)](https://github.com/caprover/caprover) · [Upsun vs CapRover 2026 (vendor)](https://upsun.com/blog/upsun-vs-caprover-2026/) · [Where Docker Swarm still fits in 2026](https://bleevht.substack.com/p/where-docker-swarm-still-fits-in) · [Atlas survey: CapRover](https://laoujin.github.io/Atlas/research/2026-06-08-self-hosted-paas-for-homelab-pr-previews-decision-framework-2026/caprover/)
- [Dokku (GitHub)](https://github.com/dokku/dokku) · [Dokku Pro multiple servers](https://pro.dokku.com/docs/features/multiple-servers/) · [Atlas survey: Dokku](https://laoujin.github.io/Atlas/research/2026-06-08-self-hosted-paas-for-homelab-pr-previews-decision-framework-2026/dokku/)
- [Temps (GitHub)](https://github.com/gotempsh/temps) · [Temps on ecosyste.ms](https://awesome.ecosyste.ms/projects/github.com%2Fgotempsh%2Ftemps) · [Temps platform setup](https://skills.sh/gotempsh/temps/temps-platform-setup) · [AlternativeTo: Temps](https://alternativeto.net/software/temps-sh/about)
- [Coolify discloses 11 critical flaws (The Hacker News)](https://thehackernews.com/2026/01/coolify-discloses-11-critical-flaws.html) · [Coolify CVE overview (wz-it)](https://wz-it.com/en/blog/coolify-cve-security-vulnerabilities-update-2025-2026/) · [Coolify PHP advisory 2026](https://coolify.io/docs/knowledge-base/php-security-advisory-2026)
- [Self-hosted PaaS comparison 2026 (DEV)](https://dev.to/_6638a39c349d7e9c85ee20/self-hosted-paas-comparison-2026-coolify-vs-dokploy-vs-caprover-vs-kamal-vs-dokku-m6e)
- [wz-it comparison](https://wz-it.com/en/blog/self-hosted-paas-comparison-coolify-dokploy-caprover/)
- [Coolify compare page (vendor)](https://coolify.io/compare/coolify-vs-dokploy-vs-caprover-vs-dokku-vs-kamal)
- [Coolify: server crash during build](https://coolify.io/docs/troubleshoot/server/crash-during-build)
- [Coolify: resource limits](https://coolify.io/docs/applications/configuration/resource-limits)
- ["Is Coolify that cool?" (Traefik CPU)](https://academy-ebo.pages.dev/blog/is-coolify-that-cool)
- [Coolify resource planning](https://massivegrid.com/blog/coolify-resource-planning-multiple-apps)
- [Coolify vs Dokploy (virtua.cloud)](https://www.virtua.cloud/learn/en/concepts/coolify-vs-dokploy-self-hosted-paas)
- [Coolify vs Dokploy (Hostinger)](https://www.hostinger.com/tutorials/coolify-vs-dokploy)
- [Dokploy monitoring docs](https://docs.dokploy.com/docs/core/monitoring)
- [Dokploy GitHub](https://github.com/dokploy/dokploy)
- [Introducing Railpack (Railway blog)](https://blog.railway.com/p/introducing-railpack)
- [Railpack architecture](https://railpack.com/architecture/overview.md)
- [Railpack BuildKit frontend](https://railpack.com/platforms/buildkit-frontend.md)
- [Railway: moving on from Nix](https://dev.to/sarah-railway/why-were-moving-on-from-nix-3cb1)
- [Nixpacks vs Railpack (bitdoze)](https://bitdoze.com/nixpacks-vs-railpack)
- [Easypanel builders](https://easypanel.io/docs/builders.mdx)
- [Temps Coolify alternatives (vendor)](https://temps.sh/blog/5-best-coolify-alternatives-self-hosted-paas-2026)
