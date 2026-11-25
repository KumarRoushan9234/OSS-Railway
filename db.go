package main

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  pass_hash TEXT NOT NULL,
  created INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
  token_hash TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL,
  expires INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS services (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  source TEXT NOT NULL,          -- github | git | image | postgres | mysql | redis | mongo
  repo TEXT NOT NULL DEFAULT '', -- owner/name for github, https URL for git
  branch TEXT NOT NULL DEFAULT '',
  image TEXT NOT NULL DEFAULT '',
  root_dir TEXT NOT NULL DEFAULT '',
  port INTEGER NOT NULL DEFAULT 0,
  domain TEXT NOT NULL DEFAULT '',
  autodeploy INTEGER NOT NULL DEFAULT 1,
  container TEXT NOT NULL DEFAULT '', -- container currently serving traffic
  created INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS env (
  service_id INTEGER NOT NULL,
  key TEXT NOT NULL,
  value TEXT NOT NULL, -- encrypted
  PRIMARY KEY (service_id, key)
);
CREATE TABLE IF NOT EXISTS deployments (
  id INTEGER PRIMARY KEY,
  service_id INTEGER NOT NULL,
  status TEXT NOT NULL,          -- queued | building | deploying | live | failed | replaced
  commit_sha TEXT NOT NULL DEFAULT '',
  message TEXT NOT NULL DEFAULT '',
  image TEXT NOT NULL DEFAULT '',
  created INTEGER NOT NULL,
  finished INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS deployments_service ON deployments(service_id, id);
CREATE TABLE IF NOT EXISTS metrics (
  ts INTEGER NOT NULL,
  target TEXT NOT NULL,          -- 'host' or service id
  cpu REAL NOT NULL,             -- percent
  mem INTEGER NOT NULL,          -- bytes
  mem_limit INTEGER NOT NULL,
  net_rx INTEGER NOT NULL,
  net_tx INTEGER NOT NULL,
  disk INTEGER NOT NULL,
  disk_total INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS metrics_target_ts ON metrics(target, ts);
CREATE TABLE IF NOT EXISTS alerts (
  id INTEGER PRIMARY KEY,
  ts INTEGER NOT NULL,
  kind TEXT NOT NULL,
  service_id INTEGER NOT NULL DEFAULT 0,
  message TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS login_tokens (
  token_hash TEXT PRIMARY KEY,   -- sha256 of the token
  user_id INTEGER NOT NULL,
  kind TEXT NOT NULL,            -- mfa (password ok, waiting for code) | recover (email link)
  attempts INTEGER NOT NULL DEFAULT 0,
  expires INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS recovery_codes (
  user_id INTEGER NOT NULL,
  code_hash TEXT NOT NULL,
  PRIMARY KEY (user_id, code_hash)
);
CREATE TABLE IF NOT EXISTS api_tokens (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  scope TEXT NOT NULL,           -- read | full
  created INTEGER NOT NULL,
  last_used INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS audit (
  id INTEGER PRIMARY KEY,
  ts INTEGER NOT NULL,
  actor TEXT NOT NULL,           -- account email, "token <name>" or "sign-in"
  ip TEXT NOT NULL,
  action TEXT NOT NULL,          -- "POST /api/services/3/deploy"
  target TEXT NOT NULL DEFAULT '',
  status INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS log_lines (
  id INTEGER PRIMARY KEY,
  service_id INTEGER NOT NULL,
  ts INTEGER NOT NULL,           -- unix nanoseconds, from docker's timestamp
  line TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS log_lines_service_ts ON log_lines(service_id, ts);
CREATE VIRTUAL TABLE IF NOT EXISTS log_fts USING fts5(line, content='log_lines', content_rowid='id');
CREATE TRIGGER IF NOT EXISTS log_lines_ai AFTER INSERT ON log_lines BEGIN
  INSERT INTO log_fts(rowid, line) VALUES (new.id, new.line);
END;
CREATE TRIGGER IF NOT EXISTS log_lines_ad AFTER DELETE ON log_lines BEGIN
  INSERT INTO log_fts(log_fts, rowid, line) VALUES('delete', old.id, old.line);
END;
CREATE TABLE IF NOT EXISTS passkeys (
  id TEXT PRIMARY KEY,           -- base64url credential id
  user_id INTEGER NOT NULL,
  name TEXT NOT NULL,
  credential TEXT NOT NULL,      -- JSON of the webauthn credential (public key, counters)
  created INTEGER NOT NULL,
  last_used INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS backups (
  id INTEGER PRIMARY KEY,
  service_id INTEGER NOT NULL,
  file TEXT NOT NULL,
  size INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL,
  created INTEGER NOT NULL
);
`

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: one writer, avoids "database is locked"
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	// Columns added after v0.1. SQLite has no ADD COLUMN IF NOT EXISTS, so "duplicate column" is expected.
	for _, col := range []string{
		"services ADD COLUMN cpu REAL NOT NULL DEFAULT 0",       // CPU limit in cores, 0 = no limit
		"services ADD COLUMN memory INTEGER NOT NULL DEFAULT 0", // memory limit in MB, 0 = no limit
		"services ADD COLUMN x REAL NOT NULL DEFAULT 0",         // canvas position
		"services ADD COLUMN y REAL NOT NULL DEFAULT 0",
		"users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT ''",    // encrypted; '' = 2FA off
		"users ADD COLUMN totp_pending TEXT NOT NULL DEFAULT ''",   // encrypted; set while enrolling
		"users ADD COLUMN totp_last INTEGER NOT NULL DEFAULT 0",    // last accepted time step (no replays)
		"api_tokens ADD COLUMN expires INTEGER NOT NULL DEFAULT 0", // unix time; 0 = never
		"users ADD COLUMN webauthn_id TEXT NOT NULL DEFAULT ''",    // random passkey user handle
	} {
		if _, err := db.Exec("ALTER TABLE " + col); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return nil, err
		}
	}
	return db, nil
}

func now() int64 { return time.Now().Unix() }

func (a *App) setting(key string) string {
	var v string
	err := a.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		a.log.Error("read setting", "key", key, "err", err)
	}
	return v
}

func (a *App) setSetting(key, value string) error {
	_, err := a.db.Exec(`INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// secretSetting reads an encrypted setting.
func (a *App) secretSetting(key string) string {
	v := a.setting(key)
	if v == "" {
		return ""
	}
	plain, err := a.decrypt(v)
	if err != nil {
		a.log.Error("decrypt setting", "key", key, "err", err)
		return ""
	}
	return plain
}

func (a *App) setSecretSetting(key, value string) error {
	if value == "" {
		return a.setSetting(key, "")
	}
	enc, err := a.encrypt(value)
	if err != nil {
		return err
	}
	return a.setSetting(key, enc)
}

type Service struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Source     string  `json:"source"`
	Repo       string  `json:"repo"`
	Branch     string  `json:"branch"`
	Image      string  `json:"image"`
	RootDir    string  `json:"root_dir"`
	Port       int     `json:"port"`
	Domain     string  `json:"domain"`
	AutoDeploy bool    `json:"autodeploy"`
	CPU        float64 `json:"cpu"`
	Memory     int     `json:"memory"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Container  string  `json:"-"`
	Created    int64   `json:"created"`
}

const serviceCols = `id, name, source, repo, branch, image, root_dir, port, domain, autodeploy, cpu, memory, x, y, container, created`

func scanService(row interface{ Scan(...any) error }) (*Service, error) {
	s := &Service{}
	err := row.Scan(&s.ID, &s.Name, &s.Source, &s.Repo, &s.Branch, &s.Image, &s.RootDir, &s.Port, &s.Domain, &s.AutoDeploy, &s.CPU, &s.Memory, &s.X, &s.Y, &s.Container, &s.Created)
	return s, err
}

func (a *App) service(id int64) (*Service, error) {
	return scanService(a.db.QueryRow(`SELECT `+serviceCols+` FROM services WHERE id = ?`, id))
}

func (a *App) serviceByName(name string) (*Service, error) {
	return scanService(a.db.QueryRow(`SELECT `+serviceCols+` FROM services WHERE name = ?`, name))
}

func (a *App) services() ([]*Service, error) {
	rows, err := a.db.Query(`SELECT ` + serviceCols + ` FROM services ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Service
	for rows.Next() {
		s, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (s *Service) IsDatabase() bool {
	_, ok := dbTemplates[s.Source]
	return ok
}

// env returns the decrypted env vars of a service.
func (a *App) env(serviceID int64) (map[string]string, error) {
	rows, err := a.db.Query(`SELECT key, value FROM env WHERE service_id = ?`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		plain, err := a.decrypt(v)
		if err != nil {
			return nil, err
		}
		out[k] = plain
	}
	return out, rows.Err()
}

func (a *App) setEnv(serviceID int64, vars map[string]string) error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM env WHERE service_id = ?`, serviceID); err != nil {
		return err
	}
	for k, v := range vars {
		enc, err := a.encrypt(v)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO env(service_id, key, value) VALUES(?, ?, ?)`, serviceID, k, enc); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *App) addAlert(kind string, serviceID int64, msg string) {
	if _, err := a.db.Exec(`INSERT INTO alerts(ts, kind, service_id, message) VALUES(?, ?, ?, ?)`, now(), kind, serviceID, msg); err != nil {
		a.log.Error("save alert", "err", err)
	}
}
