# Roadmap

Status as of 2026-11-27. "Local" means tested on Docker Desktop on Windows. "VM" means tested on one real Azure Ubuntu 24.04 VM (4 cores, 16 GB) that was already running other services.

## Built

| Area | What exists | Tested |
|---|---|---|
| Install | `install.sh` (Docker, swap, firewall, panel + Caddy), `npx oss-railway setup root@IP` | **VM**: the real `curl \| sh` command; re-running it to update (5 s, data kept); the documented uninstall (nothing of the server's own left touched); a fresh install with `npx oss-railway setup azureuser@IP -i key.pem` from Windows (9 s). The same 42-check test passed after each install |
| Panel | One Go binary, SQLite, embedded React dashboard; ~50 MB RAM panel + ~48 MB Caddy | local |
| Deploys | Dockerfile, no Dockerfile (Railpack), Docker image; health-checked switch; rollback (3 images) | local |
| GitHub | GitHub App via manifest, repo import (private repos), push-to-deploy webhook (HMAC checked) | **end to end**: app created on a real account, private repo deployed, `git push` redeployed through a tunnel |
| Domains / HTTPS | Caddy auto HTTPS, custom domains, `name.<ip>.sslip.io` | **VM**: real Let's Encrypt certificates for the panel and for app subdomains, issued automatically |
| Databases | Postgres, MySQL, Redis, MongoDB; `${{db.KEY}}` references, drag to link | Postgres, Redis |
| Env vars | Encrypted at rest, raw editor | local |
| Logs | Live build and runtime logs; stored 7 days in SQLite FTS5, searchable per service or across all | local |
| Metrics | Host + per-container CPU, memory, network, disk; 7 days | local |
| Adopt existing containers | Shows every container not created by the panel on the canvas (grouped by Compose project); adopt one or a whole project for logs, metrics, crash alerts, restart/stop/start and live limits; never rebuilt, recreated or re-routed; "stop managing" hands it back | local: 25 checks incl. a real crash and stop-managing; your `runner-stream` containers were listed but not touched |
| Server view | Every listening port (container), every container incl. external, volumes, disk | **VM**: shows the VM's own services as external; process names blank under AppArmor |
| Resources | CPU/memory limits applied live; usage + suggested limits; allocation split across services with overcommit warnings | **VM**: 0.5 CPU / 256 MB applied to a running container |
| Alerts | Crash, crash loop, OOM, deploy failed, disk, memory; email + Slack/Discord webhook | email through a local SMTP server; webhook payload by test, not a real Slack/Discord |
| Backups / reports | Daily Postgres/MySQL backups (keep 7), daily report email | local |
| Sign-in security | bcrypt password, rate limits, CSRF header, TOTP 2FA, 10 recovery codes, emailed one-time link, passkeys, change password, sign out other browsers, audit log | local; passkeys by a software-authenticator test, not yet with a real device |
| API / AI agents | API tokens (read/full, expiry), MCP endpoint `/api/mcp` with 7 tools | curl and tests; not yet from a real Claude client |
| AI analysis | Any OpenAI-compatible endpoint explains failed deploys and logs (Ollama by default) | local Ollama `qwen2.5:7b` |
| Remote access | Cloudflare quick tunnel, or your own tunnel token | quick tunnel, end to end |
| Desktop app | Tauri app: many servers, tokens in the OS keychain, status, logs, deploy/restart, analysis with local Ollama, opens each dashboard | builds and unit tests on Windows; macOS/Linux build in CI |
| Dashboard | Canvas with drag and link, side panels, Ctrl+K, themes, 375–1920 px | local |
| CI / releases | Checks on every push; every push to main releases binaries, image, desktop installers, npm | not run yet (first push pending) |
| Website | `site/` on GitHub Pages: features and docs | local preview |

## Still to do

1. **More real-server runs**: done once on Azure Ubuntu 24.04 (install, HTTPS, update, image deploy, Railpack build, crash restart, MCP, 2FA). Still to do: Debian/RHEL, ARM, a 1 GB server, a custom domain, and surviving a host reboot.
2. **Real notifications**: send to a real Slack and Discord webhook; a real SMTP provider (Resend, SES).
3. **Passkeys on real devices**: Windows Hello, iPhone, a security key (needs HTTPS and a hostname).
4. **MCP from Claude**: connect Claude Code / Claude Desktop with a token and run the tools.
5. **Desktop app polish**: signed installers (Windows code signing, macOS notarization), auto-update, tray icon with alerts.
6. **More**: restore a backup from the UI, Redis/Mongo backups and S3 upload, `docker-compose.yml` import, replicas, cron jobs, per-service alert rules, team accounts, multiple servers in one panel.

## Known limits

- The panel controls Docker on the server, which equals root. Anyone who can sign in owns the server.
- Disk can't be capped per container in a normal Docker setup; it is shown and alerted on.
- Railpack's `prepare --env` puts variable values in process arguments during a build.
- A quick tunnel's address changes every time it starts; use your own tunnel token for a fixed address.
