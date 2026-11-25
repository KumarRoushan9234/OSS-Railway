# Roadmap

Status as of 2026-11-25. "Local" means tested on Docker Desktop on Windows, not on a real VPS.

## Built

| Area | What exists | Tested |
|---|---|---|
| Install | `install.sh` (Docker, swap, firewall, panel + Caddy), `npx oss-railway setup root@IP` | syntax only; not run on a VPS |
| Panel | One Go binary, SQLite, embedded React dashboard; ~50 MB RAM panel + ~48 MB Caddy | local |
| Deploys | Dockerfile, no Dockerfile (Railpack), Docker image; health-checked switch; rollback (3 images) | local |
| GitHub | GitHub App via manifest, repo import (private repos), push-to-deploy webhook (HMAC checked) | **end to end**: app created on a real account, private repo deployed, `git push` redeployed through a tunnel |
| Domains / HTTPS | Caddy auto HTTPS, custom domains, `name.<ip>.sslip.io` | HTTP locally; HTTPS via Cloudflare tunnel; Let's Encrypt not yet |
| Databases | Postgres, MySQL, Redis, MongoDB; `${{db.KEY}}` references, drag to link | Postgres, Redis |
| Env vars | Encrypted at rest, raw editor | local |
| Logs | Live build and runtime logs; stored 7 days in SQLite FTS5, searchable per service or across all | local |
| Metrics | Host + per-container CPU, memory, network, disk; 7 days | local |
| Server view | Every listening port (process + container), every container incl. external, volumes, disk | local |
| Resources | CPU/memory limits applied live; usage + suggested limits; allocation split across services with overcommit warnings | local |
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

1. **Run on a real VPS**: `install.sh` on fresh Ubuntu, Let's Encrypt via sslip.io and a custom domain, 1 GB RAM server, reboot survival.
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
