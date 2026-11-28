package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const railpackFrontend = "ghcr.io/railwayapp/railpack-frontend:v0.43.0" // keep in step with the railpack binary in the Dockerfile

var (
	nameRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}[a-z0-9]$`)
	imageRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9._/:@-]{0,254}$`)
	repoRe   = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	branchRe = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
	envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	refRe    = regexp.MustCompile(`\$\{\{\s*([a-z0-9-]+)\.([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)
)

func (a *App) deployLogPath(id int64) string {
	return filepath.Join(a.cfg.DataDir, "logs", fmt.Sprintf("deploy-%d.log", id))
}

// deploy queues a deployment. With image != "" it skips the build (rollback).
func (a *App) deploy(s *Service, reason, image string) (int64, error) {
	if s.IsExternal() {
		return 0, errExternal
	}
	a.mu.Lock()
	if a.building[s.ID] {
		a.mu.Unlock()
		return 0, errors.New("a deploy is already running for this service")
	}
	a.building[s.ID] = true
	a.mu.Unlock()

	res, err := a.db.Exec(`INSERT INTO deployments(service_id, status, message, created) VALUES(?, 'queued', ?, ?)`, s.ID, reason, now())
	if err != nil {
		a.doneBuilding(s.ID)
		return 0, err
	}
	id, _ := res.LastInsertId()
	go func() {
		defer a.doneBuilding(s.ID)
		a.deployMu.Lock() // one build at a time
		defer a.deployMu.Unlock()
		a.runDeploy(id, s.ID, image)
	}()
	return id, nil
}

func (a *App) doneBuilding(id int64) {
	a.mu.Lock()
	delete(a.building, id)
	a.mu.Unlock()
}

func (a *App) setDeployStatus(id int64, status, msg string) {
	if status == "live" || status == "failed" {
		a.db.Exec(`UPDATE deployments SET status = ?, message = CASE WHEN ? = '' THEN message ELSE ? END, finished = ? WHERE id = ?`, status, msg, msg, now(), id)
		return
	}
	a.db.Exec(`UPDATE deployments SET status = ? WHERE id = ?`, status, id)
}

func (a *App) runDeploy(id, serviceID int64, image string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	logf, err := os.OpenFile(a.deployLogPath(id), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		a.setDeployStatus(id, "failed", "cannot open log file")
		return
	}
	defer logf.Close()
	step := func(format string, args ...any) { fmt.Fprintf(logf, "==> "+format+"\n", args...) }

	fail := func(err error) {
		step("Failed: %v", err)
		a.setDeployStatus(id, "failed", firstLine(err.Error()))
		if s, e := a.service(serviceID); e == nil {
			a.notify("deploy_failed", serviceID, fmt.Sprintf("Deploy of %s failed: %s", s.Name, firstLine(err.Error())))
		}
	}

	s, err := a.service(serviceID)
	if err != nil {
		fail(err)
		return
	}
	if s.IsDatabase() {
		a.setDeployStatus(id, "deploying", "")
		if err := a.startDatabase(ctx, s, logf); err != nil {
			fail(err)
			return
		}
		a.setDeployStatus(id, "live", "")
		return
	}

	vars, err := a.resolvedEnv(s)
	if err != nil {
		fail(err)
		return
	}

	if image == "" {
		a.setDeployStatus(id, "building", "")
		image, err = a.build(ctx, s, id, vars, logf, step)
		if err != nil {
			fail(err)
			return
		}
	} else {
		step("Reusing image %s", image)
	}
	a.db.Exec(`UPDATE deployments SET image = ? WHERE id = ?`, image, id)

	a.setDeployStatus(id, "deploying", "")
	port := s.Port
	if port == 0 {
		port = exposedPort(ctx, image)
	}
	if _, ok := vars["PORT"]; !ok {
		vars["PORT"] = strconv.Itoa(port)
	}
	name := fmt.Sprintf("ossr-%s-%d", s.Name, id)
	step("Starting %s (port %d)", name, port)
	if err := a.runContainer(ctx, s, id, name, image, vars, logf); err != nil {
		fail(err)
		return
	}
	step("Waiting for the app to accept connections on port %d", port)
	if err := waitHealthy(ctx, name, port, 90*time.Second); err != nil {
		logs, _ := docker(ctx, "logs", "--tail", "40", name)
		fmt.Fprintln(logf, logs)
		a.removeContainer(ctx, name)
		fail(err)
		return
	}

	old := s.Container
	a.db.Exec(`UPDATE services SET container = ?, port = ? WHERE id = ?`, name, port, s.ID)
	if err := a.reloadProxy(ctx); err != nil {
		step("Warning: proxy reload failed: %v", err)
	}
	if old != "" && old != name {
		step("Stopping previous container %s", old)
		a.removeContainer(ctx, old)
	}
	a.db.Exec(`UPDATE deployments SET status = 'replaced' WHERE service_id = ? AND status = 'live'`, s.ID)
	a.setDeployStatus(id, "live", "")
	for _, h := range a.serviceHosts(s) {
		step("Live at %s", siteURL(h))
	}
	a.pruneImages(ctx, s)
}

// build clones the source and produces an image: Dockerfile if present, Railpack otherwise.
func (a *App) build(ctx context.Context, s *Service, id int64, vars map[string]string, logw io.Writer, step func(string, ...any)) (string, error) {
	tag := fmt.Sprintf("ossr/%s:%d", s.Name, id)
	if s.Source == "image" {
		step("Pulling %s", s.Image)
		if err := dockerTo(ctx, logw, nil, "pull", s.Image); err != nil {
			return "", fmt.Errorf("pull failed: %w", err)
		}
		return s.Image, nil
	}

	dir := filepath.Join(a.cfg.DataDir, "builds", strconv.FormatInt(id, 10))
	os.RemoveAll(dir)
	defer os.RemoveAll(dir)

	url, gitEnv, err := a.cloneSource(ctx, s)
	if err != nil {
		return "", err
	}
	step("Cloning %s (%s)", s.Repo, s.Branch)
	args := []string{"clone", "--depth", "1"}
	if s.Branch != "" {
		args = append(args, "--branch", s.Branch)
	}
	args = append(args, "--", url, dir)
	git := exec.CommandContext(ctx, "git", args...)
	git.Env = append(os.Environ(), gitEnv...)
	git.Env = append(git.Env, "GIT_TERMINAL_PROMPT=0")
	git.Stdout, git.Stderr = logw, logw
	if err := git.Run(); err != nil {
		return "", fmt.Errorf("git clone failed: %w", err)
	}
	if sha, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--short", "HEAD").Output(); err == nil {
		a.db.Exec(`UPDATE deployments SET commit_sha = ? WHERE id = ?`, strings.TrimSpace(string(sha)), id)
	}

	ctxDir, err := safeJoin(dir, s.RootDir)
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(filepath.Join(ctxDir, "Dockerfile")); err == nil {
		step("Building with Dockerfile")
		if err := dockerTo(ctx, logw, nil, "buildx", "build", "--load", "-t", tag, ctxDir); err != nil {
			return "", fmt.Errorf("docker build failed: %w", err)
		}
		return tag, nil
	}

	step("No Dockerfile found, building with Railpack")
	keys := sortedKeys(vars)
	plan := filepath.Join(dir, ".ossr-plan.json")
	info := filepath.Join(dir, ".ossr-info.json")
	prepArgs := []string{"prepare", ctxDir, "--plan-out", plan, "--info-out", info}
	for _, k := range keys {
		prepArgs = append(prepArgs, "--env", k+"="+vars[k])
	}
	prep := exec.CommandContext(ctx, "railpack", prepArgs...)
	prep.Stdout, prep.Stderr = logw, logw
	if err := prep.Run(); err != nil {
		return "", fmt.Errorf("railpack could not detect how to build this app (add a Dockerfile): %w", err)
	}

	buildArgs := []string{"buildx", "build", "--load", "-t", tag,
		"--build-arg", "BUILDKIT_SYNTAX=" + railpackFrontend,
		"--build-arg", "secrets-hash=" + secretsHash(vars),
		"-f", plan}
	buildEnv := os.Environ()
	for _, k := range keys {
		buildArgs = append(buildArgs, "--secret", "id="+k+",env="+k)
		buildEnv = append(buildEnv, k+"="+vars[k])
	}
	buildArgs = append(buildArgs, ctxDir)
	if err := dockerTo(ctx, logw, buildEnv, buildArgs...); err != nil {
		return "", fmt.Errorf("build failed: %w", err)
	}
	return tag, nil
}

// runContainer passes env values through the docker CLI's own environment
// ("-e KEY" without a value), so secrets never appear in process arguments.
func (a *App) runContainer(ctx context.Context, s *Service, id int64, name, image string, vars map[string]string, logw io.Writer) error {
	args := []string{"run", "-d", "--name", name, "--network", a.cfg.Network,
		"--restart", "unless-stopped",
		"--label", "ossr.service=" + strconv.FormatInt(s.ID, 10),
		"--label", "ossr.deployment=" + strconv.FormatInt(id, 10),
		"--log-opt", "max-size=10m", "--log-opt", "max-file=3"}
	if s.CPU > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(s.CPU, 'f', 2, 64))
	}
	if s.Memory > 0 {
		args = append(args, "--memory", strconv.Itoa(s.Memory)+"m")
	}
	cmdEnv := os.Environ()
	for _, k := range sortedKeys(vars) {
		args = append(args, "-e", k)
		cmdEnv = append(cmdEnv, k+"="+vars[k])
	}
	args = append(args, image)
	if err := dockerTo(ctx, logw, cmdEnv, args...); err != nil {
		return fmt.Errorf("container did not start: %w", err)
	}
	return nil
}

// waitHealthy waits until the container is running and accepts TCP on its port.
func waitHealthy(ctx context.Context, name string, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := net.JoinHostPort(name, strconv.Itoa(port))
	for time.Now().Before(deadline) {
		// The restart policy hides crashes ("restarting", or "running" again), so any restart counts as a crash.
		state, _ := docker(ctx, "inspect", "-f", "{{.State.Status}} {{.RestartCount}} {{.State.ExitCode}}", name)
		var status string
		var restarts, exit int
		fmt.Sscan(state, &status, &restarts, &exit)
		if restarts > 0 || status == "exited" || status == "dead" || status == "restarting" {
			return fmt.Errorf("app exited on start (exit code %d); check the start command and logs", exit)
		}
		if status == "running" {
			if c, err := net.DialTimeout("tcp", addr, 2*time.Second); err == nil {
				c.Close()
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("app did not listen on port %d within %s (set the right port in Settings)", port, timeout)
}

func exposedPort(ctx context.Context, image string) int {
	out, err := docker(ctx, "image", "inspect", "-f", "{{json .Config.ExposedPorts}}", image)
	if err == nil {
		var ports map[string]any
		if json.Unmarshal([]byte(out), &ports) == nil {
			var nums []int
			for p := range ports {
				if n, err := strconv.Atoi(strings.Split(p, "/")[0]); err == nil {
					nums = append(nums, n)
				}
			}
			sort.Ints(nums)
			if len(nums) > 0 {
				return nums[0]
			}
		}
	}
	return 3000
}

// resolvedEnv returns a service's env with ${{other.KEY}} references filled in.
func (a *App) resolvedEnv(s *Service) (map[string]string, error) {
	vars, err := a.env(s.ID)
	if err != nil {
		return nil, err
	}
	lookup := func(service, key string) (string, bool) {
		other, err := a.serviceByName(service)
		if err != nil {
			return "", false
		}
		ov, err := a.env(other.ID)
		if err != nil {
			return "", false
		}
		v, ok := ov[key]
		return v, ok
	}
	return resolveRefs(vars, lookup)
}

func resolveRefs(vars map[string]string, lookup func(service, key string) (string, bool)) (map[string]string, error) {
	out := make(map[string]string, len(vars))
	for k, v := range vars {
		var missing string
		out[k] = refRe.ReplaceAllStringFunc(v, func(m string) string {
			p := refRe.FindStringSubmatch(m)
			val, ok := lookup(p[1], p[2])
			if !ok {
				missing = p[1] + "." + p[2]
			}
			return val
		})
		if missing != "" {
			return nil, fmt.Errorf("%s references ${{%s}}, which does not exist", k, missing)
		}
	}
	return out, nil
}

func secretsHash(vars map[string]string) string {
	h := sha256.New()
	for _, k := range sortedKeys(vars) {
		fmt.Fprintf(h, "%s=%s\n", k, vars[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// safeJoin joins a user-supplied subdirectory without allowing escape from base.
func safeJoin(base, sub string) (string, error) {
	if sub == "" || sub == "." || sub == "/" {
		return base, nil
	}
	clean := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(sub, "/")))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("root directory must be inside the repository")
	}
	return filepath.Join(base, clean), nil
}

// pruneImages keeps the 3 newest built images per service for rollback.
func (a *App) pruneImages(ctx context.Context, s *Service) {
	rows, err := a.db.Query(`SELECT image FROM deployments WHERE service_id = ? AND image LIKE 'ossr/%' ORDER BY id DESC LIMIT -1 OFFSET 3`, s.ID)
	if err != nil {
		return
	}
	var old []string
	for rows.Next() {
		var img string
		if rows.Scan(&img) == nil {
			old = append(old, img)
		}
	}
	rows.Close()
	for _, img := range old {
		docker(ctx, "rmi", img)
	}
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

func (a *App) markStopping(name string) {
	a.mu.Lock()
	a.stopping[name] = time.Now()
	a.mu.Unlock()
}
