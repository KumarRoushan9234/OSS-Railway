package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (a *App) routes(dist fs.FS) http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/me", a.handleMe)
	m.HandleFunc("POST /api/setup", csrfOnly(a.handleSetup))
	m.HandleFunc("POST /api/login", csrfOnly(a.handleLogin))
	m.HandleFunc("POST /api/logout", a.authSession(a.handleLogout))

	m.HandleFunc("GET /api/overview", a.auth(a.handleOverview))
	m.HandleFunc("GET /api/services", a.auth(a.handleServices))
	m.HandleFunc("POST /api/services", a.auth(a.handleCreateService))
	m.HandleFunc("GET /api/services/{id}", a.auth(a.handleService))
	m.HandleFunc("PATCH /api/services/{id}", a.auth(a.handleUpdateService))
	m.HandleFunc("DELETE /api/services/{id}", a.auth(a.handleDeleteService))
	m.HandleFunc("GET /api/services/{id}/env", a.auth(a.handleGetEnv))
	m.HandleFunc("PUT /api/services/{id}/env", a.auth(a.handlePutEnv))
	m.HandleFunc("POST /api/services/{id}/deploy", a.auth(a.handleDeploy))
	m.HandleFunc("POST /api/services/{id}/restart", a.auth(a.handleRestart))
	m.HandleFunc("GET /api/services/{id}/deployments", a.auth(a.handleDeployments))
	m.HandleFunc("GET /api/services/{id}/logs", a.auth(a.handleRuntimeLogs))
	m.HandleFunc("GET /api/services/{id}/metrics", a.auth(a.handleServiceMetrics))
	m.HandleFunc("GET /api/services/{id}/backups", a.auth(a.handleBackups))
	m.HandleFunc("POST /api/services/{id}/backups", a.auth(a.handleCreateBackup))
	m.HandleFunc("GET /api/backups/{id}/download", a.auth(a.handleDownloadBackup))
	m.HandleFunc("GET /api/deployments/{id}/logs", a.auth(a.handleDeployLogs))
	m.HandleFunc("POST /api/deployments/{id}/rollback", a.auth(a.handleRollback))
	m.HandleFunc("GET /api/metrics/host", a.auth(a.handleHostMetrics))
	m.HandleFunc("GET /api/alerts", a.auth(a.handleAlerts))
	m.HandleFunc("GET /api/settings", a.authSession(a.handleGetSettings))
	m.HandleFunc("PUT /api/settings", a.authSession(a.handlePutSettings))
	m.HandleFunc("POST /api/settings/test-alert", a.authSession(a.handleTestAlert))
	m.HandleFunc("POST /api/settings/test-report", a.authSession(a.handleTestReport))

	m.HandleFunc("POST /api/login/2fa", csrfOnly(a.handleLogin2FA))
	m.HandleFunc("POST /api/login/recover", csrfOnly(a.handleLoginRecover))
	m.HandleFunc("GET /api/login/recover/{token}", a.handleRecoverLink)
	m.HandleFunc("GET /api/2fa", a.authSession(a.handle2FAStatus))
	m.HandleFunc("POST /api/2fa/setup", a.authSession(a.handle2FASetup))
	m.HandleFunc("POST /api/2fa/enable", a.authSession(a.handle2FAEnable))
	m.HandleFunc("POST /api/2fa/disable", a.authSession(a.handle2FADisable))
	m.HandleFunc("POST /api/2fa/recovery-codes", a.authSession(a.handle2FARecoveryCodes))
	m.HandleFunc("POST /api/services/{id}/explain", a.auth(a.handleExplain))
	m.HandleFunc("POST /api/ai/test", a.authSession(a.handleAITest))
	m.HandleFunc("GET /api/tunnel", a.authSession(a.handleTunnelStatus))
	m.HandleFunc("POST /api/tunnel", a.authSession(a.handleTunnelStart))
	m.HandleFunc("DELETE /api/tunnel", a.authSession(a.handleTunnelStop))
	m.HandleFunc("GET /api/logs/search", a.auth(a.handleLogSearch))
	m.HandleFunc("GET /api/passkeys", a.authSession(a.handlePasskeys))
	m.HandleFunc("POST /api/passkeys/register/begin", a.authSession(a.handlePasskeyRegisterBegin))
	m.HandleFunc("POST /api/passkeys/register/finish", a.authSession(a.handlePasskeyRegisterFinish))
	m.HandleFunc("DELETE /api/passkeys/{id}", a.authSession(a.handleDeletePasskey))
	m.HandleFunc("POST /api/passkeys/login/begin", csrfOnly(a.handlePasskeyLoginBegin))
	m.HandleFunc("POST /api/passkeys/login/finish", csrfOnly(a.handlePasskeyLoginFinish))
	m.HandleFunc("GET /api/audit", a.authSession(a.handleAudit))
	m.HandleFunc("POST /api/password", a.authSession(a.handleChangePassword))
	m.HandleFunc("GET /api/sessions", a.authSession(a.handleSessions))
	m.HandleFunc("DELETE /api/sessions", a.authSession(a.handleSignOutOthers))
	m.HandleFunc("GET /api/tokens", a.authSession(a.handleTokens))
	m.HandleFunc("POST /api/tokens", a.authSession(a.handleCreateToken))
	m.HandleFunc("DELETE /api/tokens/{id}", a.authSession(a.handleDeleteToken))
	m.HandleFunc("/api/mcp", a.handleMCP) // authenticated by API token
	m.HandleFunc("GET /api/server", a.auth(a.handleServer))
	m.HandleFunc("GET /api/services/{id}/resources", a.auth(a.handleResources))

	m.HandleFunc("GET /api/github", a.auth(a.handleGitHubStatus))
	m.HandleFunc("GET /api/github/manifest", a.authSession(a.handleGitHubManifest))
	m.HandleFunc("GET /api/github/callback", a.authSession(a.handleGitHubCallback))
	m.HandleFunc("GET /api/github/installed", a.authSession(a.handleGitHubInstalled))
	m.HandleFunc("GET /api/github/repos", a.auth(a.handleGitHubRepos))
	m.HandleFunc("POST /api/github/webhook", a.handleGitHubWebhook) // authenticated by HMAC signature

	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { httpError(w, http.StatusNotFound, "not found") })
	m.Handle("/", spa(dist))
	return securityHeaders(a.auditChanges(m))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; form-action 'self' https://github.com; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		if isHTTPS(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// spa serves the built dashboard; unknown paths fall back to index.html.
func spa(dist fs.FS) http.Handler {
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if _, err := fs.Stat(dist, p); err == nil {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, dist, "index.html")
	})
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// serverError logs the detail and shows the user a short message.
func serverError(w http.ResponseWriter, a *App, err error) {
	a.log.Error("request failed", "err", err)
	httpError(w, http.StatusInternalServerError, firstLine(err.Error()))
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func (a *App) serviceFromPath(w http.ResponseWriter, r *http.Request) (*Service, bool) {
	id, ok := pathID(r)
	if !ok {
		httpError(w, http.StatusNotFound, "service not found")
		return nil, false
	}
	s, err := a.service(id)
	if err != nil {
		httpError(w, http.StatusNotFound, "service not found")
		return nil, false
	}
	return s, true
}

// ---- services ----

type serviceView struct {
	*Service
	Kind   string   `json:"kind"`
	Status string   `json:"status"` // last deployment status, or running/stopped
	URLs   []string `json:"urls"`
	Commit string   `json:"commit"`
	Since  int64    `json:"since"`
	Uses   []string `json:"uses"` // services this one references with ${{name.KEY}}
}

func (a *App) view(ctx context.Context, s *Service) serviceView {
	v := serviceView{Service: s, Kind: "app", URLs: []string{}, Uses: []string{}}
	if vars, err := a.env(s.ID); err == nil {
		v.Uses = referencedServices(vars)
	}
	if s.IsDatabase() {
		v.Kind = "database"
	} else if s.Container != "" {
		for _, h := range a.serviceHosts(s) {
			v.URLs = append(v.URLs, siteURL(h))
		}
	}
	a.db.QueryRow(`SELECT status, commit_sha, created FROM deployments WHERE service_id = ? ORDER BY id DESC LIMIT 1`, s.ID).Scan(&v.Status, &v.Commit, &v.Since)
	if v.Status == "live" || v.Status == "replaced" || v.Status == "" {
		v.Status = "stopped"
		if s.Container != "" && containerRunning(ctx, s.Container) {
			v.Status = "running"
		}
	}
	return v
}

func (a *App) handleServices(w http.ResponseWriter, r *http.Request) {
	services, err := a.services()
	if err != nil {
		serverError(w, a, err)
		return
	}
	out := []serviceView{}
	for _, s := range services {
		out = append(out, a.view(r.Context(), s))
	}
	writeJSON(w, out)
}

func (a *App) handleService(w http.ResponseWriter, r *http.Request) {
	s, ok := a.serviceFromPath(w, r)
	if !ok {
		return
	}
	writeJSON(w, a.view(r.Context(), s))
}

type serviceInput struct {
	Name    string `json:"name"`
	Source  string `json:"source"`
	Repo    string `json:"repo"`
	Branch  string `json:"branch"`
	Image   string `json:"image"`
	RootDir string `json:"root_dir"`
	Port    int    `json:"port"`
}

func (in *serviceInput) validate() error {
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	if !nameRe.MatchString(in.Name) {
		return errors.New("name: 2–32 lowercase letters, digits or dashes, starting with a letter")
	}
	if in.Name == "caddy" || in.Name == "panel" {
		return errors.New("that name is reserved")
	}
	if in.Port < 0 || in.Port > 65535 {
		return errors.New("invalid port")
	}
	if in.Branch != "" && (!branchRe.MatchString(in.Branch) || strings.HasPrefix(in.Branch, "-")) {
		return errors.New("invalid branch")
	}
	if _, err := safeJoin("/x", in.RootDir); err != nil {
		return err
	}
	switch in.Source {
	case "github":
		if !repoRe.MatchString(in.Repo) {
			return errors.New("pick a repository")
		}
	case "git":
		u, err := url.Parse(in.Repo)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return errors.New("enter a public https:// Git URL")
		}
	case "image":
		if !imageRe.MatchString(in.Image) {
			return errors.New("enter an image like nginx:latest or ghcr.io/owner/app:tag")
		}
	default:
		if _, ok := dbTemplates[in.Source]; !ok {
			return errors.New("unknown source")
		}
	}
	return nil
}

func (a *App) handleCreateService(w http.ResponseWriter, r *http.Request) {
	var in serviceInput
	if !readJSON(w, r, &in) {
		return
	}
	if err := in.validate(); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := a.serviceByName(in.Name); err == nil {
		httpError(w, http.StatusConflict, "a service with that name already exists")
		return
	}
	if in.Source == "github" && in.Branch == "" {
		in.Branch = "main"
		if repos, err := a.listRepos(r.Context()); err == nil {
			for _, repo := range repos {
				if strings.EqualFold(repo.FullName, in.Repo) {
					in.Branch = repo.DefaultBranch
				}
			}
		}
	}
	res, err := a.db.Exec(`INSERT INTO services(name, source, repo, branch, image, root_dir, port, created) VALUES(?,?,?,?,?,?,?,?)`,
		in.Name, in.Source, in.Repo, in.Branch, in.Image, in.RootDir, in.Port, now())
	if err != nil {
		serverError(w, a, err)
		return
	}
	id, _ := res.LastInsertId()
	s, _ := a.service(id)
	if t, ok := dbTemplates[s.Source]; ok {
		if err := a.setEnv(s.ID, t.vars(dbContainer(s), randomPassword())); err != nil {
			serverError(w, a, err)
			return
		}
	}
	// Apps wait for env vars on the first screen; databases and images start right away.
	if s.IsDatabase() || s.Source == "image" {
		a.deploy(s, "created", "")
	}
	writeJSON(w, a.view(r.Context(), s))
}

func (a *App) handleUpdateService(w http.ResponseWriter, r *http.Request) {
	s, ok := a.serviceFromPath(w, r)
	if !ok {
		return
	}
	var in struct {
		Branch     *string  `json:"branch"`
		RootDir    *string  `json:"root_dir"`
		Port       *int     `json:"port"`
		Domain     *string  `json:"domain"`
		Image      *string  `json:"image"`
		AutoDeploy *bool    `json:"autodeploy"`
		CPU        *float64 `json:"cpu"`
		Memory     *int     `json:"memory"`
		X          *float64 `json:"x"`
		Y          *float64 `json:"y"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.CPU != nil {
		if *in.CPU < 0 || *in.CPU > maxCPU {
			httpError(w, http.StatusBadRequest, "CPU limit must be 0 to 64 cores")
			return
		}
		s.CPU = *in.CPU
	}
	if in.Memory != nil {
		if *in.Memory != 0 && (*in.Memory < minMemoryMB || *in.Memory > maxMemoryMB) {
			httpError(w, http.StatusBadRequest, "memory limit must be 0 or 16 MB to 1 TB")
			return
		}
		s.Memory = *in.Memory
	}
	if in.X != nil && in.Y != nil {
		s.X, s.Y = clamp(*in.X, -canvasLimit, canvasLimit), clamp(*in.Y, -canvasLimit, canvasLimit)
	}
	if in.Branch != nil {
		if !branchRe.MatchString(*in.Branch) || strings.HasPrefix(*in.Branch, "-") {
			httpError(w, http.StatusBadRequest, "invalid branch")
			return
		}
		s.Branch = *in.Branch
	}
	if in.RootDir != nil {
		if _, err := safeJoin("/x", *in.RootDir); err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.RootDir = *in.RootDir
	}
	if in.Port != nil {
		if *in.Port < 0 || *in.Port > 65535 {
			httpError(w, http.StatusBadRequest, "invalid port")
			return
		}
		s.Port = *in.Port
	}
	if in.Domain != nil {
		d := strings.ToLower(strings.TrimSpace(*in.Domain))
		if d != "" && !validHost(d) {
			httpError(w, http.StatusBadRequest, "invalid domain")
			return
		}
		s.Domain = d
	}
	if in.Image != nil {
		if s.Source != "image" || !imageRe.MatchString(*in.Image) {
			httpError(w, http.StatusBadRequest, "invalid image")
			return
		}
		s.Image = *in.Image
	}
	if in.AutoDeploy != nil {
		s.AutoDeploy = *in.AutoDeploy
	}
	_, err := a.db.Exec(`UPDATE services SET branch=?, root_dir=?, port=?, domain=?, image=?, autodeploy=?, cpu=?, memory=?, x=?, y=? WHERE id=?`,
		s.Branch, s.RootDir, s.Port, s.Domain, s.Image, s.AutoDeploy, s.CPU, s.Memory, s.X, s.Y, s.ID)
	if err != nil {
		serverError(w, a, err)
		return
	}
	if in.Domain != nil {
		if err := a.reloadProxy(r.Context()); err != nil {
			serverError(w, a, err)
			return
		}
	}
	if (in.CPU != nil || in.Memory != nil) && s.Container != "" {
		if err := applyLimits(r.Context(), s); err != nil {
			a.log.Warn("live limit update failed; applies on next deploy", "service", s.Name, "err", err)
		}
	}
	writeJSON(w, a.view(r.Context(), s))
}

// applyLimits changes CPU and memory limits of the running container without a restart.
// Docker cannot lift a memory limit in place, so removing one waits for the next deploy.
func applyLimits(ctx context.Context, s *Service) error {
	args := []string{"update", "--cpus", strconv.FormatFloat(s.CPU, 'f', 2, 64)}
	if s.Memory > 0 {
		m := strconv.Itoa(s.Memory) + "m"
		args = append(args, "--memory", m, "--memory-swap", m)
	}
	_, err := docker(ctx, append(args, s.Container)...)
	return err
}

const (
	maxCPU      = 64
	minMemoryMB = 16 // Docker refuses less than 6 MB; below 16 MB nothing useful runs
	maxMemoryMB = 1 << 20
	canvasLimit = 100000
)

func clamp(v, lo, hi float64) float64 { return max(lo, min(hi, v)) }

// referencedServices lists the service names used in ${{name.KEY}} references, sorted and unique.
func referencedServices(vars map[string]string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range vars {
		for _, m := range refRe.FindAllStringSubmatch(v, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	sort.Strings(out)
	return out
}

func (a *App) handleDeleteService(w http.ResponseWriter, r *http.Request) {
	s, ok := a.serviceFromPath(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if out, err := docker(ctx, "ps", "-aq", "--filter", "label=ossr.service="+strconv.FormatInt(s.ID, 10)); err == nil {
		for _, id := range strings.Fields(out) {
			name, _ := docker(ctx, "inspect", "-f", "{{.Name}}", id)
			a.removeContainer(ctx, strings.TrimPrefix(name, "/"))
		}
	}
	if s.IsDatabase() {
		docker(ctx, "volume", "rm", dbVolume(s))
	}
	for _, q := range []string{
		`DELETE FROM services WHERE id = ?`, `DELETE FROM env WHERE service_id = ?`,
		`DELETE FROM deployments WHERE service_id = ?`, `DELETE FROM metrics WHERE target = CAST(? AS TEXT)`,
	} {
		a.db.Exec(q, s.ID)
	}
	a.reloadProxy(ctx)
	w.WriteHeader(http.StatusNoContent)
}

// ---- env ----

func (a *App) handleGetEnv(w http.ResponseWriter, r *http.Request) {
	s, ok := a.serviceFromPath(w, r)
	if !ok {
		return
	}
	vars, err := a.env(s.ID)
	if err != nil {
		serverError(w, a, err)
		return
	}
	writeJSON(w, vars)
}

func (a *App) handlePutEnv(w http.ResponseWriter, r *http.Request) {
	s, ok := a.serviceFromPath(w, r)
	if !ok {
		return
	}
	var vars map[string]string
	if !readJSON(w, r, &vars) {
		return
	}
	if len(vars) > 500 {
		httpError(w, http.StatusBadRequest, "too many variables")
		return
	}
	for k, v := range vars {
		if !envKeyRe.MatchString(k) {
			httpError(w, http.StatusBadRequest, "invalid variable name: "+k)
			return
		}
		if len(v) > 64<<10 {
			httpError(w, http.StatusBadRequest, k+" is too long")
			return
		}
	}
	if _, err := resolveRefs(vars, func(svc, key string) (string, bool) {
		o, err := a.serviceByName(svc)
		if err != nil {
			return "", false
		}
		ov, _ := a.env(o.ID)
		_, ok := ov[key]
		return "", ok
	}); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.setEnv(s.ID, vars); err != nil {
		serverError(w, a, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- deploys ----

func (a *App) handleDeploy(w http.ResponseWriter, r *http.Request) {
	s, ok := a.serviceFromPath(w, r)
	if !ok {
		return
	}
	id, err := a.deploy(s, "manual", "")
	if err != nil {
		httpError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, map[string]int64{"deployment": id})
}

func (a *App) handleRestart(w http.ResponseWriter, r *http.Request) {
	s, ok := a.serviceFromPath(w, r)
	if !ok {
		return
	}
	if s.Container == "" {
		httpError(w, http.StatusConflict, "not deployed yet")
		return
	}
	a.markStopping(s.Container)
	if _, err := docker(r.Context(), "restart", s.Container); err != nil {
		serverError(w, a, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleRollback(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		httpError(w, http.StatusNotFound, "not found")
		return
	}
	var sid int64
	var image string
	if err := a.db.QueryRow(`SELECT service_id, image FROM deployments WHERE id = ?`, id).Scan(&sid, &image); err != nil || image == "" {
		httpError(w, http.StatusNotFound, "this deployment has no image to roll back to")
		return
	}
	if _, err := docker(r.Context(), "image", "inspect", image); err != nil {
		httpError(w, http.StatusGone, "that image was cleaned up")
		return
	}
	s, err := a.service(sid)
	if err != nil {
		httpError(w, http.StatusNotFound, "service not found")
		return
	}
	newID, err := a.deploy(s, fmt.Sprintf("rollback to #%d", id), image)
	if err != nil {
		httpError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, map[string]int64{"deployment": newID})
}

type deploymentView struct {
	ID       int64  `json:"id"`
	Status   string `json:"status"`
	Commit   string `json:"commit"`
	Message  string `json:"message"`
	Image    string `json:"image"`
	Created  int64  `json:"created"`
	Finished int64  `json:"finished"`
}

func (a *App) handleDeployments(w http.ResponseWriter, r *http.Request) {
	s, ok := a.serviceFromPath(w, r)
	if !ok {
		return
	}
	out, err := a.recentDeployments(s.ID, 30)
	if err != nil {
		serverError(w, a, err)
		return
	}
	writeJSON(w, out)
}

func (a *App) recentDeployments(serviceID int64, limit int) ([]deploymentView, error) {
	rows, err := a.db.Query(`SELECT id, status, commit_sha, message, image, created, finished FROM deployments WHERE service_id = ? ORDER BY id DESC LIMIT ?`, serviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []deploymentView{}
	for rows.Next() {
		var d deploymentView
		rows.Scan(&d.ID, &d.Status, &d.Commit, &d.Message, &d.Image, &d.Created, &d.Finished)
		out = append(out, d)
	}
	return out, rows.Err()
}

// deployLogTail returns the last lines of a deployment's build log.
func (a *App) deployLogTail(id int64, n int) string {
	b, err := os.ReadFile(a.deployLogPath(id))
	if err != nil {
		return ""
	}
	return lastLines(string(b), n)
}

// ---- logs (Server-Sent Events) ----

func sse(w http.ResponseWriter) (func(string), bool) {
	fl, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	return func(line string) {
		data, _ := json.Marshal(line)
		fmt.Fprintf(w, "data: %s\n\n", data)
		fl.Flush()
	}, true
}

// handleDeployLogs tails a deploy log until the deployment finishes.
func (a *App) handleDeployLogs(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		httpError(w, http.StatusNotFound, "not found")
		return
	}
	send, ok := sse(w)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	var f *os.File
	defer func() {
		if f != nil {
			f.Close()
		}
	}()
	var partial string
	for {
		if f == nil {
			f, _ = os.Open(a.deployLogPath(id))
		}
		if f != nil {
			buf := make([]byte, 32<<10)
			for {
				n, err := f.Read(buf)
				if n > 0 {
					lines := strings.Split(partial+string(buf[:n]), "\n")
					partial = lines[len(lines)-1]
					for _, l := range lines[:len(lines)-1] {
						send(strings.TrimRight(l, "\r"))
					}
				}
				if err != nil {
					break
				}
			}
		}
		var status string
		a.db.QueryRow(`SELECT status FROM deployments WHERE id = ?`, id).Scan(&status)
		if status != "queued" && status != "building" && status != "deploying" {
			if partial != "" {
				send(partial)
			}
			fmt.Fprintf(w, "event: done\ndata: %q\n\n", status)
			w.(http.Flusher).Flush()
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// handleRuntimeLogs streams `docker logs -f` for the live container.
func (a *App) handleRuntimeLogs(w http.ResponseWriter, r *http.Request) {
	s, ok := a.serviceFromPath(w, r)
	if !ok {
		return
	}
	if s.Container == "" {
		httpError(w, http.StatusConflict, "not deployed yet")
		return
	}
	send, ok := sse(w)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	pr, pw := io.Pipe()
	cmd := exec.CommandContext(r.Context(), "docker", "logs", "-f", "--tail", "300", "--timestamps", s.Container)
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		send("could not read logs: " + err.Error())
		return
	}
	go func() { cmd.Wait(); pw.Close() }()
	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		send(sc.Text())
	}
}

// ---- metrics, alerts, overview ----

func (a *App) handleServiceMetrics(w http.ResponseWriter, r *http.Request) {
	s, ok := a.serviceFromPath(w, r)
	if !ok {
		return
	}
	pts, err := a.series(strconv.FormatInt(s.ID, 10), r.URL.Query().Get("range"))
	if err != nil {
		serverError(w, a, err)
		return
	}
	writeJSON(w, pts)
}

func (a *App) handleHostMetrics(w http.ResponseWriter, r *http.Request) {
	pts, err := a.series("host", r.URL.Query().Get("range"))
	if err != nil {
		serverError(w, a, err)
		return
	}
	writeJSON(w, pts)
}

type alertView struct {
	TS      int64  `json:"ts"`
	Kind    string `json:"kind"`
	Service int64  `json:"service_id"`
	Message string `json:"message"`
}

func (a *App) recentAlerts(limit int) []alertView {
	out := []alertView{}
	rows, err := a.db.Query(`SELECT ts, kind, service_id, message FROM alerts ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var v alertView
		rows.Scan(&v.TS, &v.Kind, &v.Service, &v.Message)
		out = append(out, v)
	}
	return out
}

func (a *App) handleAlerts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, a.recentAlerts(100))
}

func (a *App) handleOverview(w http.ResponseWriter, r *http.Request) {
	var p point
	a.db.QueryRow(`SELECT ts, cpu, mem, mem_limit, net_rx, net_tx, disk, disk_total FROM metrics WHERE target = 'host' ORDER BY ts DESC LIMIT 1`).
		Scan(&p.T, &p.CPU, &p.Mem, &p.MemLimit, &p.NetRx, &p.NetTx, &p.Disk, &p.DiskTot)
	panel := ""
	if h := a.panelHost(); h != "" {
		panel = siteURL(h)
	}
	writeJSON(w, map[string]any{
		"host":      p,
		"alerts":    a.recentAlerts(5),
		"panel_url": panel,
		"github":    a.setting("gh_app_id") != "",
		"email":     a.setting("smtp_host") != "" && a.setting("alert_to") != "",
	})
}

// ---- backups ----

func (a *App) handleBackups(w http.ResponseWriter, r *http.Request) {
	s, ok := a.serviceFromPath(w, r)
	if !ok {
		return
	}
	rows, err := a.db.Query(`SELECT id, file, size, status, created FROM backups WHERE service_id = ? ORDER BY id DESC`, s.ID)
	if err != nil {
		serverError(w, a, err)
		return
	}
	defer rows.Close()
	type bv struct {
		ID      int64  `json:"id"`
		File    string `json:"file"`
		Size    int64  `json:"size"`
		Status  string `json:"status"`
		Created int64  `json:"created"`
	}
	out := []bv{}
	for rows.Next() {
		var b bv
		rows.Scan(&b.ID, &b.File, &b.Size, &b.Status, &b.Created)
		out = append(out, b)
	}
	writeJSON(w, out)
}

func (a *App) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	s, ok := a.serviceFromPath(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if _, err := a.backup(ctx, s); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		httpError(w, http.StatusNotFound, "not found")
		return
	}
	var file string
	if err := a.db.QueryRow(`SELECT file FROM backups WHERE id = ? AND status = 'ok'`, id).Scan(&file); err != nil {
		httpError(w, http.StatusNotFound, "not found")
		return
	}
	name := filepath.Base(file)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, filepath.Join(a.cfg.DataDir, "backups", name))
}

// ---- settings ----

var plainSettings = []string{"base_domain", "panel_domain", "acme_email", "smtp_host", "smtp_port", "smtp_user", "alert_from", "alert_to", "daily_report", "ai_url", "ai_model"}
var secretSettings = []string{"smtp_pass", "webhook_url", "ai_key"}

func (a *App) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{}
	for _, k := range plainSettings {
		out[k] = a.setting(k)
	}
	for _, k := range secretSettings {
		out[k+"_set"] = a.setting(k) != "" // never send secrets back to the browser
	}
	out["public_ip"] = a.cfg.PublicIP
	writeJSON(w, out)
}

func (a *App) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var in map[string]string
	if !readJSON(w, r, &in) {
		return
	}
	allowed := map[string]bool{}
	for _, k := range append(append([]string{}, plainSettings...), secretSettings...) {
		allowed[k] = true
	}
	for k, v := range in {
		v = strings.TrimSpace(v)
		if !allowed[k] || len(v) > 2000 {
			httpError(w, http.StatusBadRequest, "invalid setting: "+k)
			return
		}
		switch k {
		case "base_domain", "panel_domain", "smtp_host":
			v = strings.ToLower(v)
			if v != "" && !validHost(v) {
				httpError(w, http.StatusBadRequest, "invalid hostname: "+v)
				return
			}
		case "smtp_port":
			if n, err := strconv.Atoi(v); v != "" && (err != nil || n < 1 || n > 65535) {
				httpError(w, http.StatusBadRequest, "invalid SMTP port")
				return
			}
		case "webhook_url":
			if v != "" && !validWebhook(v) {
				httpError(w, http.StatusBadRequest, "invalid webhook URL")
				return
			}
		case "ai_url":
			if v != "" && !validAIURL(v) {
				httpError(w, http.StatusBadRequest, "AI endpoint must be an http(s) URL, e.g. http://host.docker.internal:11434/v1")
				return
			}
		}
		in[k] = v
	}
	for k, v := range in {
		var err error
		if slices.Contains(secretSettings, k) {
			err = a.setSecretSetting(k, v)
		} else {
			err = a.setSetting(k, v)
		}
		if err != nil {
			serverError(w, a, err)
			return
		}
	}
	if err := a.reloadProxy(r.Context()); err != nil {
		a.log.Error("proxy reload after settings", "err", err)
	}
	a.handleGetSettings(w, r)
}

func (a *App) handleTestAlert(w http.ResponseWriter, r *http.Request) {
	if a.setting("smtp_host") == "" && a.setting("webhook_url") == "" {
		httpError(w, http.StatusBadRequest, "add email or a webhook first")
		return
	}
	msg := "Test alert from OSS Railway"
	if err := a.sendEmail("[OSS Railway] "+msg, msg+". Alerts are working."); err != nil {
		httpError(w, http.StatusBadGateway, "email failed: "+firstLine(err.Error()))
		return
	}
	if err := a.sendWebhook(msg); err != nil {
		httpError(w, http.StatusBadGateway, "webhook failed: "+firstLine(err.Error()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleTestReport(w http.ResponseWriter, r *http.Request) {
	subject, body := a.report(24 * time.Hour)
	if a.setting("smtp_host") == "" {
		writeJSON(w, map[string]string{"subject": subject, "body": body})
		return
	}
	if err := a.sendEmail(subject, body); err != nil {
		httpError(w, http.StatusBadGateway, "email failed: "+firstLine(err.Error()))
		return
	}
	writeJSON(w, map[string]string{"subject": subject, "body": body})
}
