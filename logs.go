package main

import (
	"bufio"
	"context"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Stored, searchable logs. Every 10 s the panel copies new lines from each service's
// container into SQLite (full-text indexed with FTS5) and keeps them for 7 days,
// so logs survive redeploys and can be searched across services.

const (
	logPoll       = 10 * time.Second
	logKeep       = 7 * 24 * time.Hour
	logMaxPerPoll = 5000
	logMaxLines   = 200_000 // per service
	logMaxLineLen = 4096
)

func (a *App) collectLogs(ctx context.Context) {
	t := time.NewTicker(logPoll)
	defer t.Stop()
	lastPrune := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		services, _ := a.services()
		for _, s := range services {
			if s.Container != "" {
				a.collectServiceLogs(ctx, s)
			}
		}
		if time.Since(lastPrune) > time.Hour {
			lastPrune = time.Now()
			a.pruneLogs()
		}
	}
}

func (a *App) collectServiceLogs(ctx context.Context, s *Service) {
	var last int64
	a.db.QueryRow(`SELECT COALESCE(MAX(ts), 0) FROM log_lines WHERE service_id = ?`, s.ID).Scan(&last)
	since := time.Now().Add(-time.Hour) // first run: the last hour is enough history
	if last > 0 {
		since = time.Unix(0, last+1)
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "docker", "logs", "--timestamps", "--since", since.UTC().Format(time.RFC3339Nano), s.Container)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	cmd.Stderr = cmd.Stdout // docker writes the app's stderr here; one stream keeps the order
	if cmd.Start() != nil {
		return
	}
	defer cmd.Wait()
	tx, err := a.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	n := 0
	for sc.Scan() && n < logMaxPerPoll {
		ts, line, ok := parseLogLine(sc.Text())
		if !ok || ts <= last {
			continue
		}
		tx.Exec(`INSERT INTO log_lines(service_id, ts, line) VALUES(?, ?, ?)`, s.ID, ts, line)
		n++
	}
	tx.Commit()
}

// parseLogLine splits docker's "2026-11-24T10:00:00.123456789Z message" into unix nanos and text.
func parseLogLine(l string) (int64, string, bool) {
	stamp, line, ok := strings.Cut(l, " ")
	if !ok {
		stamp, line = l, ""
	}
	t, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return 0, "", false
	}
	if len(line) > logMaxLineLen {
		line = line[:logMaxLineLen]
	}
	return t.UnixNano(), strings.TrimRight(line, "\r"), true
}

func (a *App) pruneLogs() {
	a.db.Exec(`DELETE FROM log_lines WHERE ts < ?`, time.Now().Add(-logKeep).UnixNano())
	rows, err := a.db.Query(`SELECT service_id, COUNT(*) FROM log_lines GROUP BY service_id HAVING COUNT(*) > ?`, logMaxLines)
	if err != nil {
		return
	}
	type over struct{ id, n int64 }
	var list []over
	for rows.Next() {
		var o over
		rows.Scan(&o.id, &o.n)
		list = append(list, o)
	}
	rows.Close()
	for _, o := range list {
		a.db.Exec(`DELETE FROM log_lines WHERE id IN (SELECT id FROM log_lines WHERE service_id = ? ORDER BY ts LIMIT ?)`, o.id, o.n-logMaxLines)
	}
}

// ftsQuery turns what the user typed into an FTS5 query: every word must appear,
// each quoted so characters like : - * ( ) are searched for, not parsed.
func ftsQuery(q string) string {
	var terms []string
	for _, w := range strings.Fields(q) {
		terms = append(terms, `"`+strings.ReplaceAll(w, `"`, `""`)+`"`)
	}
	return strings.Join(terms, " ")
}

type logHit struct {
	ID      int64  `json:"id"`
	Service string `json:"service"`
	TS      int64  `json:"ts"` // unix milliseconds
	Line    string `json:"line"`
}

// GET /api/logs/search?q=&service=<id>&before=<id>
// Newest first, 200 at a time. Without q: the latest lines.
func (a *App) handleLogSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 200 {
		httpError(w, http.StatusBadRequest, "search is too long")
		return
	}
	sid, _ := strconv.ParseInt(r.URL.Query().Get("service"), 10, 64)
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	if before <= 0 {
		before = 1 << 62
	}
	where := []string{"l.id < ?"}
	args := []any{before}
	if sid > 0 {
		where = append(where, "l.service_id = ?")
		args = append(args, sid)
	}
	from := "log_lines l"
	if q != "" {
		from = "log_fts f JOIN log_lines l ON l.id = f.rowid"
		where = append(where, "log_fts MATCH ?")
		args = append(args, ftsQuery(q))
	}
	rows, err := a.db.Query(`SELECT l.id, COALESCE(s.name, ''), l.ts, l.line FROM `+from+`
		LEFT JOIN services s ON s.id = l.service_id
		WHERE `+strings.Join(where, " AND ")+` ORDER BY l.id DESC LIMIT 200`, args...)
	if err != nil {
		serverError(w, a, err)
		return
	}
	defer rows.Close()
	out := []logHit{}
	for rows.Next() {
		var h logHit
		if rows.Scan(&h.ID, &h.Service, &h.TS, &h.Line) == nil {
			h.TS /= 1e6
			out = append(out, h)
		}
	}
	writeJSON(w, out)
}
