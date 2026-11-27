package main

import (
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// ---- audit log ----

// Every change made through the API is recorded: who (session or token), from where, what.
// Request bodies are never stored, so secrets never end up here.

type auditEntry struct {
	ID     int64  `json:"id"`
	TS     int64  `json:"ts"`
	Actor  string `json:"actor"`
	IP     string `json:"ip"`
	Action string `json:"action"`
	Target string `json:"target"`
	Status int    `json:"status"`
}

func (a *App) audit(r *http.Request, actor, action, target string, status int) {
	a.db.Exec(`INSERT INTO audit(ts, actor, ip, action, target, status) VALUES(?, ?, ?, ?, ?, ?)`,
		now(), actor, clientIP(r), action, target, status)
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// auditChanges records every state-changing /api request after it runs.
// Sign-in attempts are recorded whether they succeed or not.
func (a *App) auditChanges(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/mcp" || r.URL.Path == "/api/github/webhook" {
			next.ServeHTTP(w, r)
			return
		}
		actor := a.actor(r) // before the handler: logout ends the session
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		login := strings.HasPrefix(r.URL.Path, "/api/login") || r.URL.Path == "/api/setup"
		if sw.status >= 400 && !login {
			return
		}
		if login && actor == "" {
			actor = "sign-in"
		}
		a.audit(r, actor, r.Method+" "+r.URL.Path, a.auditTarget(r.URL.Path), sw.status)
	})
}

// actor names who is making the request: the account email, or "token <name>".
func (a *App) actor(r *http.Request) string {
	if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		var name string
		a.db.QueryRow(`SELECT name FROM api_tokens WHERE token_hash = ?`, sha256Hex(bearer)).Scan(&name)
		return "token " + name
	}
	if uid := a.userFromRequest(r); uid != 0 {
		var email string
		a.db.QueryRow(`SELECT email FROM users WHERE id = ?`, uid).Scan(&email)
		return email
	}
	return ""
}

// auditTarget turns /api/services/12/... into the service name, so the log stays readable after renames.
func (a *App) auditTarget(path string) string {
	rest, ok := strings.CutPrefix(path, "/api/services/")
	if !ok {
		return ""
	}
	id, err := strconv.ParseInt(strings.SplitN(rest, "/", 2)[0], 10, 64)
	if err != nil {
		return ""
	}
	var name string
	a.db.QueryRow(`SELECT name FROM services WHERE id = ?`, id).Scan(&name)
	return name
}

// GET /api/audit?before=<id>
func (a *App) handleAudit(w http.ResponseWriter, r *http.Request) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	if before <= 0 {
		before = 1 << 62
	}
	rows, err := a.db.Query(`SELECT id, ts, actor, ip, action, target, status FROM audit WHERE id < ? ORDER BY id DESC LIMIT 100`, before)
	if err != nil {
		serverError(w, a, err)
		return
	}
	defer rows.Close()
	out := []auditEntry{}
	for rows.Next() {
		var e auditEntry
		if rows.Scan(&e.ID, &e.TS, &e.Actor, &e.IP, &e.Action, &e.Target, &e.Status) == nil {
			out = append(out, e)
		}
	}
	writeJSON(w, out)
}

// ---- password ----

// POST /api/password {current, new, code}: needs the current password, and a 2FA code when 2FA is on.
// Every other session is signed out.
func (a *App) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if a.limiter.blocked(clientIP(r)) {
		httpError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	uid := r.Context().Value(ctxKey{}).(int64)
	var in struct {
		Current string `json:"current"`
		New     string `json:"new"`
		Code    string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if a.checkPassword(uid, in.Current) != nil {
		a.limiter.fail(clientIP(r))
		httpError(w, http.StatusUnauthorized, "current password is wrong")
		return
	}
	if secret, _ := a.userTOTP(uid); secret != "" && !a.checkSecondFactor(uid, in.Code) {
		a.limiter.fail(clientIP(r))
		httpError(w, http.StatusUnauthorized, "wrong authenticator code")
		return
	}
	if len(in.New) < 10 || len(in.New) > 72 {
		httpError(w, http.StatusBadRequest, "password must be 10–72 characters")
		return
	}
	if in.New == in.Current {
		httpError(w, http.StatusBadRequest, "choose a different password")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.New), 12)
	if err != nil {
		serverError(w, a, err)
		return
	}
	a.db.Exec(`UPDATE users SET pass_hash = ? WHERE id = ?`, string(hash), uid)
	a.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, uid)
	a.db.Exec(`DELETE FROM login_tokens WHERE user_id = ?`, uid)
	a.startSession(w, r, uid)
	a.addAlert("security", 0, "The account password was changed; other sessions were signed out")
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/sessions: how many browsers are signed in. DELETE signs out all others.
func (a *App) handleSessions(w http.ResponseWriter, r *http.Request) {
	uid := r.Context().Value(ctxKey{}).(int64)
	var n int
	a.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id = ? AND expires > ?`, uid, now()).Scan(&n)
	writeJSON(w, map[string]int{"active": n})
}

func (a *App) handleSignOutOthers(w http.ResponseWriter, r *http.Request) {
	uid := r.Context().Value(ctxKey{}).(int64)
	c, _ := r.Cookie(sessionCookie)
	a.db.Exec(`DELETE FROM sessions WHERE user_id = ? AND token_hash != ?`, uid, sha256Hex(c.Value))
	w.WriteHeader(http.StatusNoContent)
}
