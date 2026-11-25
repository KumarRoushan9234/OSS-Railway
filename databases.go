package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

type dbTemplate struct {
	image string
	port  int
	data  string // data directory inside the container
	cmd   []string
	vars  func(host, pw string) map[string]string
}

var dbTemplates = map[string]dbTemplate{
	"postgres": {
		image: "postgres:17-alpine", port: 5432, data: "/var/lib/postgresql/data",
		vars: func(h, pw string) map[string]string {
			return map[string]string{
				"POSTGRES_USER": "postgres", "POSTGRES_PASSWORD": pw, "POSTGRES_DB": "app",
				"PGHOST": h, "PGPORT": "5432", "PGUSER": "postgres", "PGPASSWORD": pw, "PGDATABASE": "app",
				"DATABASE_URL": fmt.Sprintf("postgresql://postgres:%s@%s:5432/app", pw, h),
			}
		},
	},
	"mysql": {
		image: "mysql:8.4", port: 3306, data: "/var/lib/mysql",
		vars: func(h, pw string) map[string]string {
			return map[string]string{
				"MYSQL_ROOT_PASSWORD": pw, "MYSQL_DATABASE": "app",
				"MYSQLHOST": h, "MYSQLPORT": "3306", "MYSQLUSER": "root", "MYSQLPASSWORD": pw, "MYSQLDATABASE": "app",
				"DATABASE_URL": fmt.Sprintf("mysql://root:%s@%s:3306/app", pw, h),
			}
		},
	},
	"redis": {
		image: "redis:7-alpine", port: 6379, data: "/data",
		cmd: []string{"sh", "-c", `exec redis-server --appendonly yes --requirepass "$REDIS_PASSWORD"`},
		vars: func(h, pw string) map[string]string {
			return map[string]string{
				"REDIS_PASSWORD": pw, "REDISHOST": h, "REDISPORT": "6379",
				"REDIS_URL": fmt.Sprintf("redis://default:%s@%s:6379", pw, h),
			}
		},
	},
	"mongo": {
		image: "mongo:8", port: 27017, data: "/data/db",
		vars: func(h, pw string) map[string]string {
			return map[string]string{
				"MONGO_INITDB_ROOT_USERNAME": "mongo", "MONGO_INITDB_ROOT_PASSWORD": pw,
				"MONGO_URL": fmt.Sprintf("mongodb://mongo:%s@%s:27017", pw, h),
			}
		},
	},
}

func dbContainer(s *Service) string { return "ossr-" + s.Name }
func dbVolume(s *Service) string    { return "ossr-" + s.Name + "-data" }

// startDatabase runs the database container, or starts it again if it exists.
func (a *App) startDatabase(ctx context.Context, s *Service, logw io.Writer) error {
	t := dbTemplates[s.Source]
	name := dbContainer(s)
	switch {
	case containerRunning(ctx, name):
		fmt.Fprintf(logw, "==> %s is already running\n", name)
	case containerExists(ctx, name):
		fmt.Fprintf(logw, "==> Starting %s\n", name)
		if _, err := docker(ctx, "start", name); err != nil {
			return err
		}
	default:
		vars, err := a.env(s.ID)
		if err != nil {
			return err
		}
		fmt.Fprintf(logw, "==> Pulling %s\n", t.image)
		if err := dockerTo(ctx, logw, nil, "pull", t.image); err != nil {
			return fmt.Errorf("pull failed: %w", err)
		}
		args := []string{"run", "-d", "--name", name, "--network", a.cfg.Network,
			"--restart", "unless-stopped",
			"--label", "ossr.service=" + strconv.FormatInt(s.ID, 10),
			"--log-opt", "max-size=10m", "--log-opt", "max-file=3",
			"-v", dbVolume(s) + ":" + t.data}
		cmdEnv := os.Environ()
		for _, k := range sortedKeys(vars) {
			args = append(args, "-e", k)
			cmdEnv = append(cmdEnv, k+"="+vars[k])
		}
		args = append(args, t.image)
		args = append(args, t.cmd...)
		fmt.Fprintf(logw, "==> Starting %s\n", name)
		if err := dockerTo(ctx, logw, cmdEnv, args...); err != nil {
			return fmt.Errorf("database did not start: %w", err)
		}
	}
	fmt.Fprintf(logw, "==> Waiting for %s to accept connections\n", name)
	if err := waitHealthy(ctx, name, t.port, 120*time.Second); err != nil {
		return err
	}
	a.db.Exec(`UPDATE services SET container = ?, port = ? WHERE id = ?`, name, t.port, s.ID)
	fmt.Fprintf(logw, "==> Ready. Reference it from apps with ${{%s.%s}}\n", s.Name, firstURLKey(s.Source))
	return nil
}

func firstURLKey(source string) string {
	switch source {
	case "redis":
		return "REDIS_URL"
	case "mongo":
		return "MONGO_URL"
	}
	return "DATABASE_URL"
}

// backup dumps a database into <data>/backups. Postgres and MySQL only for now.
func (a *App) backup(ctx context.Context, s *Service) (int64, error) {
	var args []string
	ext := ".sql"
	switch s.Source {
	case "postgres":
		args = []string{"exec", dbContainer(s), "pg_dump", "-U", "postgres", "-Fc", "app"}
		ext = ".dump"
	case "mysql":
		args = []string{"exec", dbContainer(s), "sh", "-c", `exec mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" --single-transaction --all-databases`}
	default:
		return 0, errors.New("backups are supported for Postgres and MySQL")
	}
	file := fmt.Sprintf("%s-%s%s", s.Name, time.Now().UTC().Format("20060102-150405"), ext)
	path := filepath.Join(a.cfg.DataDir, "backups", file)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = f
	errOut := &limitedBuffer{max: 4096}
	cmd.Stderr = errOut
	runErr := cmd.Run()
	f.Close()
	status, size := "ok", int64(0)
	if st, err := os.Stat(path); err == nil {
		size = st.Size()
	}
	if runErr != nil || size == 0 {
		status = "failed"
		os.Remove(path)
	}
	res, err := a.db.Exec(`INSERT INTO backups(service_id, file, size, status, created) VALUES(?, ?, ?, ?, ?)`, s.ID, file, size, status, now())
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if status == "failed" {
		msg := fmt.Sprintf("Backup of %s failed: %s", s.Name, firstLine(errOut.String()))
		a.notify("backup_failed", s.ID, msg)
		return id, errors.New(msg)
	}
	a.pruneBackups(s.ID, 7)
	return id, nil
}

func (a *App) pruneBackups(serviceID int64, keep int) {
	rows, err := a.db.Query(`SELECT id, file FROM backups WHERE service_id = ? ORDER BY id DESC LIMIT -1 OFFSET ?`, serviceID, keep)
	if err != nil {
		return
	}
	type old struct {
		id   int64
		file string
	}
	var olds []old
	for rows.Next() {
		var o old
		if rows.Scan(&o.id, &o.file) == nil {
			olds = append(olds, o)
		}
	}
	rows.Close()
	for _, o := range olds {
		os.Remove(filepath.Join(a.cfg.DataDir, "backups", filepath.Base(o.file)))
		a.db.Exec(`DELETE FROM backups WHERE id = ?`, o.id)
	}
}

type limitedBuffer struct {
	buf []byte
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.max - len(b.buf); room > 0 {
		if len(p) > room {
			b.buf = append(b.buf, p[:room]...)
		} else {
			b.buf = append(b.buf, p...)
		}
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string { return string(b.buf) }
