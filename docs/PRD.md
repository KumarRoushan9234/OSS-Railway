# OSS Railway — PRD (spike)

## Problem
Deploying on your own VPS means setting up Docker, a proxy, TLS, builds, databases and monitoring by hand. Existing panels are heavy (Coolify), CLI-only (Dokku, Kamal) or fading (CapRover).

## User
Someone who just bought a VPS and wants deploys that feel like Railway, without needing to know Docker.

## Core flow
1. `curl -fsSL <install-url> | sh` (or `npx oss-railway setup root@IP`)
2. Open the printed URL, enter the setup code, create the admin account
3. Connect GitHub (one click, App Manifest flow) or paste a public Git URL
4. Pick a repo → deploy → get a live URL. No Dockerfile needed (Railpack)
5. Add a Postgres/MySQL/Redis/Mongo database in one click, linked through env vars
6. Watch logs and metrics; receive an email when something crashes

## In scope (spike)
- Go single binary, SQLite, Svelte dashboard embedded in the binary
- Installer script + npx CLI that runs the installer over SSH
- Caddy proxy managed by the panel (automatic HTTPS when a domain is set)
- Services: GitHub repo, public Git URL, Docker image, database
- Builds: Dockerfile if present, otherwise Railpack
- Deploys: health check before switching traffic, then remove the old container; redeploy, push-to-deploy webhook
- Env vars encrypted at rest; `${{service.VAR}}` references
- Live build and runtime logs (SSE)
- Host + per-container metrics (CPU, RAM, disk, network), 7-day history
- Alerts: crash, OOM, disk > 85%, RAM > 90%, deploy failed → email (SMTP) and webhook
- Daily report email
- Postgres/MySQL backup on demand and daily

## Out of scope (later)
Multi-server, teams/roles, PR previews, templates catalog, AI Dockerfile fallback, restore tests, S3 backups, GitLab.

## Success check
On a fresh Ubuntu VPS: one command → dashboard → connect GitHub → deploy a Node app with no Dockerfile → live URL → kill the container → alert email arrives. Panel idle RAM < 60 MB.
