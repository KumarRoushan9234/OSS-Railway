# OSS Railway

Self-hosted Railway-like PaaS. One Go binary (API + embedded dashboard) that drives Docker and Caddy on the server.

## Stack
- Go (package `main`, flat files at the root), SQLite via `modernc.org/sqlite` (no cgo)
- Dashboard: React 19 + TypeScript + Vite in `web/`, built to `web/dist` and embedded in the binary
- Talks to Docker by running the `docker` CLI. Builds use Railpack (`railpack prepare`) + `docker buildx`
- Caddy runs as a container (`ossr-caddy`); the panel writes the Caddyfile and reloads it

## Commands
- Build UI: `cd web && npm ci && npm run build`
- All checks: `sh scripts/check.sh` (gofmt, vet, tests, UI build, installer syntax)
- Run locally: build the image and run it as a container on the `ossr` network (see README). `go run .` alone cannot reach app containers for health checks.
- Panel image: `docker build -t oss-railway .`
- Dashboard dev server: `cd web && npm run dev` (http://localhost:5176, API proxied to the panel on http://localhost)
- Desktop app: `cd desktop && cargo test && cargo run` (Tauri 2; UI is plain HTML/JS in `desktop/ui`)
- Lockfile: regenerate `web/package-lock.json` in a Linux container (`node:24-alpine`), or `npm ci` fails in the Docker build

## Conventions
- Containers, networks and volumes the panel owns are prefixed `ossr-` and labelled `ossr.service=<id>`
- Never put secrets in logs. Env values are encrypted with the key in `<data>/secret.key`
- Shell out with `exec.Command(name, args...)` only — never a shell string
- Audit: every non-GET `/api` request is logged by middleware; never log request bodies
- Security routes (settings, tokens, 2FA, passkeys, tunnel) use `authSession` (browser only); `auth` also accepts API tokens
- Docs: `docs/PRD.md`, `docs/RESEARCH.md`, `docs/ROADMAP.md`; website in `site/`
