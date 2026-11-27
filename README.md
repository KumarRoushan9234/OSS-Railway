# OSS Railway

Deploy apps and databases on your own server, the way you would on [Railway](https://railway.com). Open source, self-hosted, one command to install.

> **Work in progress.** I'm still building this. It runs well on my machine and passed my tests, but it hasn't had a long run on real servers yet. Expect rough edges, and don't put anything critical on it. See [what's tested and what isn't](docs/ROADMAP.md).

**Website and docs:** https://kumarroushan9234.github.io/OSS-Railway/

## Install

On a Linux server (Ubuntu, Debian, RHEL, Fedora, Rocky, Alma; x86-64 or ARM64) as root:

```bash
curl -fsSL https://raw.githubusercontent.com/KumarRoushan9234/OSS-Railway/main/install.sh | sh
```

Or from your own computer over SSH:

```bash
npx oss-railway setup root@YOUR_SERVER_IP
```

It installs Docker if needed, starts the panel and Caddy, and prints a dashboard address and a one-time setup code. Open the address, enter the code, create your account, and deploy.

The installer only adds rules to a firewall that is already on, and never touches containers it didn't create.

## What you get

- **Deploy from GitHub** (push to deploy, private repos too), any public Git URL, or a Docker image
- **No Dockerfile needed**: [Railpack](https://railpack.com) detects Node, Python, Go, PHP, Ruby, Java, Rust, .NET, Elixir and more
- **Databases in one click**: Postgres, MySQL, Redis, MongoDB, linked to apps by dragging a line or with `${{db.DATABASE_URL}}`
- **Automatic HTTPS** through Caddy, with a free `name.<ip>.sslip.io` address until you add your own domain
- **Safe deploys**: health-checked switchover, one-click rollback
- **Canvas dashboard**: services as cards you drag around, side panels, `Ctrl+K` search, themes
- **Server view**: every listening port with its process and container, every container on the machine (even ones you started by hand), volumes and disk use
- **CPU and memory limits** per service, applied live, with suggestions from real usage and a warning when limits add up to more than the server has
- **Logs and metrics**: live build and runtime logs, searchable for 7 days; CPU, memory, network and disk per service
- **Alerts** by email, Slack or Discord for crashes, out-of-memory kills, failed deploys, failed backups, full disks; a daily report; daily Postgres and MySQL backups
- **Sign-in security**: two-factor with any authenticator app, recovery codes, an emailed link if you lose your phone, passkeys, password change, audit log of every change
- **AI agents**: API tokens and an MCP endpoint (`/api/mcp`) so Claude and other agents can read logs, check status and deploy
- **AI log analysis**: "Explain with AI" on failed deploys, using Ollama on your own machine or any OpenAI-compatible API
- **Remote access** through a Cloudflare Tunnel: no public IP or open ports needed
- **Desktop app** (Windows, macOS, Linux) to manage many servers from your computer

Everything stays on your server: one Go binary, one SQLite file, no telemetry.

## Status

| Part | State |
|---|---|
| Deploys, databases, logs, metrics, alerts, backups | Working, tested locally |
| GitHub connection and push-to-deploy | Working, tested end to end |
| Two-factor, passkeys, tokens, audit log | Working, tested locally; passkeys not yet on a real device |
| AI analysis (Ollama) and remote access (Cloudflare Tunnel) | Working, tested locally |
| Desktop app | Works; installers are unsigned |
| One-line install, HTTPS with Let's Encrypt, update by re-running | Working: tested on one Azure Ubuntu 24.04 VM, running beside other services |
| Other Linux distros, ARM servers, 1 GB servers | **Not tested yet** |
| Slack and Discord messages, MCP from a real Claude app | **Not tested yet** |

Full list: [docs/ROADMAP.md](docs/ROADMAP.md).

## Security

The panel controls Docker on your server, which is the same as root access. Use a strong password, turn on two-factor or a passkey, and keep the dashboard on HTTPS. Report problems privately: see [SECURITY.md](SECURITY.md).

## Try it locally

You need Docker (Docker Desktop is fine) and Node 24.

```bash
docker build -t oss-railway:dev .
docker network create ossr
docker run -d --name ossr-panel --network ossr --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock -v ossr-data:/data \
  -v /proc:/host/proc:ro -v /:/host/root:ro \
  --cap-add SYS_PTRACE --add-host host.docker.internal:host-gateway oss-railway:dev
docker exec ossr-panel cat /data/setup-code
```

Open http://localhost, enter the setup code and create an account. Apps get `name.localhost` addresses.

Work on the dashboard with instant reload (keep the panel above running):

```bash
cd web && npm ci && npm run dev
```

Then open http://localhost:5176. Run every check before pushing:

```bash
sh scripts/check.sh
```

## Desktop app

`desktop/` is a [Tauri](https://tauri.app) app. Add each server with its dashboard address and an API token (Settings → API tokens); tokens are kept in your system keychain.

```bash
cd desktop && cargo run
```

Installers are built for every release.

## Releases

Every push to `main` that passes the checks is released automatically: the next version comes from commit messages (`feat:` raises the minor number, anything else the patch number), then a GitHub Release with Linux binaries, the panel image at `ghcr.io/kumarroushan9234/oss-railway`, desktop installers and the `oss-railway` npm package. Pushes that only change docs or the website don't release. The website in `site/` is published to GitHub Pages.

## Contributing

Issues and pull requests are welcome, but this is a one-person project in early development, so expect slow replies and big changes. Start with [CLAUDE.md](CLAUDE.md) for the layout and commands.

## License

[Apache-2.0](LICENSE)
