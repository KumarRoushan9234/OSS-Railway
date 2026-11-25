// OSS Railway: a self-hosted, Railway-like deploy panel in one binary.
package main

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

//go:embed all:web/dist
var webFS embed.FS

type Config struct {
	DataDir  string // all state: sqlite, key, build dirs, logs, backups
	Listen   string // panel HTTP address (Caddy proxies to it)
	PublicIP string // used for <name>.<ip>.sslip.io hostnames
	Upstream string // how Caddy reaches the panel
	Network  string // docker network shared by panel, caddy and apps
	HostProc string // /proc of the host (mounted read-only in the panel container)
	HostRoot string // / of the host, for disk usage
}

type App struct {
	cfg Config
	db  *sql.DB
	key []byte
	log *slog.Logger

	deployMu sync.Mutex           // one build at a time: small VPSes run out of RAM otherwise
	building map[int64]bool       // service id -> deploy queued or running
	stopping map[string]time.Time // containers we stopped on purpose (not crashes)
	mu       sync.Mutex           // guards building and stopping
	limiter  *rateLimiter
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := Config{
		DataDir:  env("OSSR_DATA", "./data"),
		Listen:   env("OSSR_LISTEN", ":3000"),
		PublicIP: os.Getenv("OSSR_PUBLIC_IP"),
		Upstream: env("OSSR_UPSTREAM", "ossr-panel:3000"),
		Network:  env("OSSR_NETWORK", "ossr"),
		HostProc: env("OSSR_HOST_PROC", "/host/proc"),
		HostRoot: env("OSSR_HOST_ROOT", "/host/root"),
	}
	if err := run(cfg, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(cfg Config, log *slog.Logger) error {
	for _, dir := range []string{"builds", "logs", "backups"} {
		if err := os.MkdirAll(filepath.Join(cfg.DataDir, dir), 0o700); err != nil {
			return err
		}
	}
	db, err := openDB(filepath.Join(cfg.DataDir, "ossr.db"))
	if err != nil {
		return err
	}
	key, err := loadKey(filepath.Join(cfg.DataDir, "secret.key"))
	if err != nil {
		return err
	}
	a := &App{
		cfg: cfg, db: db, key: key, log: log,
		building: map[int64]bool{},
		stopping: map[string]time.Time{},
		limiter:  newRateLimiter(10, 15*time.Minute),
	}
	if err := a.ensureSetupCode(); err != nil {
		return err
	}
	a.recoverDeployments()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := a.ensureProxy(ctx); err != nil {
		log.Error("proxy not started; apps will not be reachable until Docker works", "err", err)
	}
	go a.collectMetrics(ctx)
	go a.watchEvents(ctx)
	go a.scheduler(ctx)
	go a.collectLogs(ctx)

	dist, err := fs.Sub(webFS, "web/dist")
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           a.routes(dist),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	log.Info("panel listening", "addr", cfg.Listen)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// recoverDeployments marks deploys interrupted by a restart as failed.
func (a *App) recoverDeployments() {
	a.db.Exec(`UPDATE deployments SET status = 'failed', message = 'interrupted by panel restart', finished = ?
		WHERE status IN ('queued', 'building', 'deploying')`, now())
}
