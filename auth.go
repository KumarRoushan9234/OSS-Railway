package main

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const sessionCookie = "ossr_session"
const sessionTTL = 30 * 24 * time.Hour

func (a *App) setupCodePath() string { return filepath.Join(a.cfg.DataDir, "setup-code") }

func (a *App) hasUsers() bool {
	var n int
	a.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n > 0
}

// ensureSetupCode writes a one-time code on first start. Only someone with
// shell access to the server can read it, so only they can claim the panel.
func (a *App) ensureSetupCode() error {
	if a.hasUsers() {
		os.Remove(a.setupCodePath())
		return nil
	}
	if b, err := os.ReadFile(a.setupCodePath()); err == nil {
		a.log.Info("waiting for setup", "setup_code", strings.TrimSpace(string(b)))
		return nil
	}
	code := setupCode()
	if err := os.WriteFile(a.setupCodePath(), []byte(code+"\n"), 0o600); err != nil {
		return err
	}
	a.log.Info("waiting for setup", "setup_code", code)
	return nil
}

type credentials struct {
	Code     string `json:"code"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (c *credentials) validate() error {
	c.Email = strings.ToLower(strings.TrimSpace(c.Email))
	if _, err := mail.ParseAddress(c.Email); err != nil || len(c.Email) > 254 {
		return errors.New("enter a valid email")
	}
	if len(c.Password) < 10 || len(c.Password) > 72 {
		return errors.New("password must be 10–72 characters")
	}
	return nil
}

func (a *App) handleSetup(w http.ResponseWriter, r *http.Request) {
	if a.limiter.blocked(clientIP(r)) {
		httpError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	var c credentials
	if !readJSON(w, r, &c) {
		return
	}
	if a.hasUsers() {
		httpError(w, http.StatusConflict, "already set up")
		return
	}
	want, err := os.ReadFile(a.setupCodePath())
	got := strings.ToUpper(strings.TrimSpace(c.Code))
	if err != nil || subtle.ConstantTimeCompare([]byte(got), []byte(strings.TrimSpace(string(want)))) != 1 {
		a.limiter.fail(clientIP(r))
		httpError(w, http.StatusUnauthorized, "wrong setup code")
		return
	}
	if err := c.validate(); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(c.Password), 12)
	if err != nil {
		serverError(w, a, err)
		return
	}
	res, err := a.db.Exec(`INSERT INTO users(email, pass_hash, created) VALUES(?, ?, ?)`, c.Email, string(hash), now())
	if err != nil {
		serverError(w, a, err)
		return
	}
	os.Remove(a.setupCodePath())
	id, _ := res.LastInsertId()
	a.startSession(w, r, id)
	writeJSON(w, map[string]string{"email": c.Email})
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if a.limiter.blocked(clientIP(r)) {
		httpError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	var c credentials
	if !readJSON(w, r, &c) {
		return
	}
	var id int64
	var hash string
	err := a.db.QueryRow(`SELECT id, pass_hash FROM users WHERE email = ?`, strings.ToLower(strings.TrimSpace(c.Email))).Scan(&id, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		// Same cost as a real check, so response time does not reveal which emails exist.
		bcrypt.CompareHashAndPassword(dummyHash(), []byte(c.Password))
		a.limiter.fail(clientIP(r))
		httpError(w, http.StatusUnauthorized, "wrong email or password")
		return
	}
	if err != nil {
		serverError(w, a, err)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(c.Password)) != nil {
		a.limiter.fail(clientIP(r))
		httpError(w, http.StatusUnauthorized, "wrong email or password")
		return
	}
	// 2FA on: the password step only earns a short-lived cookie for the code step.
	if secret, _ := a.userTOTP(id); secret != "" {
		setMFACookie(w, r, a.newLoginToken(id, "mfa", mfaTTL), int(mfaTTL.Seconds()))
		writeJSON(w, map[string]any{"needs_2fa": true, "email_recovery": a.setting("smtp_host") != ""})
		return
	}
	a.startSession(w, r, id)
	writeJSON(w, map[string]string{"email": c.Email})
}

var dummyHash = sync.OnceValue(func() []byte {
	h, _ := bcrypt.GenerateFromPassword([]byte(randomToken(16)), 12)
	return h
})

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, sha256Hex(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: isHTTPS(r)})
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	uid := a.userFromRequest(r)
	if uid == 0 {
		writeJSON(w, map[string]any{"user": nil, "needs_setup": !a.hasUsers()})
		return
	}
	var email string
	a.db.QueryRow(`SELECT email FROM users WHERE id = ?`, uid).Scan(&email)
	writeJSON(w, map[string]any{"user": map[string]string{"email": email}, "needs_setup": false})
}

func (a *App) startSession(w http.ResponseWriter, r *http.Request, userID int64) {
	token := randomToken(32)
	a.db.Exec(`DELETE FROM sessions WHERE expires < ?`, now())
	a.db.Exec(`INSERT INTO sessions(token_hash, user_id, expires) VALUES(?, ?, ?)`, sha256Hex(token), userID, time.Now().Add(sessionTTL).Unix())
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", MaxAge: int(sessionTTL.Seconds()),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: isHTTPS(r),
	})
}

func (a *App) userFromRequest(r *http.Request) int64 {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return 0
	}
	var uid int64
	a.db.QueryRow(`SELECT user_id FROM sessions WHERE token_hash = ? AND expires > ?`, sha256Hex(c.Value), now()).Scan(&uid)
	return uid
}

type ctxKey struct{}

// auth accepts a browser session or an API token (Authorization: Bearer ossr_…).
// Session requests that change state must carry the X-OSSR header: browsers cannot
// send custom headers cross-site without a CORS preflight, which this server never
// allows, so this blocks CSRF. Tokens are not cookies, so they need no such header;
// read-only tokens may only GET.
func (a *App) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			uid, scope := a.tokenUser(bearer)
			if uid == 0 {
				httpError(w, http.StatusUnauthorized, "invalid token")
				return
			}
			if scope != "full" && r.Method != http.MethodGet {
				httpError(w, http.StatusForbidden, "this token is read-only")
				return
			}
			next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, uid)))
			return
		}
		a.authSession(next)(w, r)
	}
}

// authSession is for account and security settings: a browser session only, never a token.
func (a *App) authSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := a.userFromRequest(r)
		if uid == 0 {
			httpError(w, http.StatusUnauthorized, "sign in required")
			return
		}
		if r.Method != http.MethodGet && r.Header.Get("X-OSSR") != "1" {
			httpError(w, http.StatusForbidden, "missing X-OSSR header")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, uid)))
	}
}

// csrfOnly protects the unauthenticated login/setup endpoints the same way.
func csrfOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-OSSR") != "1" {
			httpError(w, http.StatusForbidden, "missing X-OSSR header")
			return
		}
		next(w, r)
	}
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// clientIP trusts X-Forwarded-For only from Caddy, which sits on the private docker network.
func clientIP(r *http.Request) string {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); ip != nil && ip.IsPrivate() {
		if f := r.Header.Get("X-Forwarded-For"); f != "" {
			return strings.TrimSpace(strings.Split(f, ",")[0])
		}
	}
	return host
}

type rateLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	return &rateLimiter{max: max, window: window, hits: map[string][]time.Time{}}
}

// recent drops entries older than the window. The caller holds l.mu.
func (l *rateLimiter) recent(key string) []time.Time {
	cutoff := time.Now().Add(-l.window)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.hits, key)
	} else {
		l.hits[key] = kept
	}
	return kept
}

// blocked reports whether this address has failed too often recently. Successful
// sign-ins never count, so the owner is not locked out by their own normal use.
func (l *rateLimiter) blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(key)) >= l.max
}

// fail records one failed attempt (or one costly action, like sending an email).
func (l *rateLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hits[key] = append(l.recent(key), time.Now())
}
