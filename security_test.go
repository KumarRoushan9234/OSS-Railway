package main

import (
	"encoding/base32"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func testApp(t *testing.T) *App {
	t.Helper()
	db, err := openDB(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	key, err := loadKey(filepath.Join(t.TempDir(), "k"))
	if err != nil {
		t.Fatal(err)
	}
	return &App{db: db, key: key, log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		building: map[int64]bool{}, stopping: map[string]time.Time{}, limiter: newRateLimiter(1000, time.Minute)}
}

func TestTOTPMatchesRFC6238(t *testing.T) {
	// RFC 6238 appendix B, SHA-1 key "12345678901234567890", T=59s -> 94287082 (last 6 digits).
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	if got, _ := totpCode(secret, 59/30); got != "287082" {
		t.Fatalf("got %s", got)
	}
	at := time.Unix(59, 0)
	if totpMatch(secret, "287082", at) == 0 || totpMatch(secret, "287 082", at) == 0 {
		t.Fatal("valid code rejected")
	}
	if totpMatch(secret, "287083", at) != 0 || totpMatch(secret, "28708", at) != 0 {
		t.Fatal("bad code accepted")
	}
	if totpMatch(secret, "287082", at.Add(5*time.Minute)) != 0 {
		t.Fatal("old code accepted")
	}
}

func TestSecondFactorRejectsReplayAndReusedRecoveryCode(t *testing.T) {
	a := testApp(t)
	a.db.Exec(`INSERT INTO users(id, email, pass_hash, created) VALUES(1, 'a@b.c', 'x', 0)`)
	secret := newTOTPSecret()
	enc, _ := a.encrypt(secret)
	a.db.Exec(`UPDATE users SET totp_secret = ? WHERE id = 1`, enc)

	code, _ := totpCode(secret, time.Now().Unix()/totpStep)
	if !a.checkSecondFactor(1, code) {
		t.Fatal("fresh code rejected")
	}
	if a.checkSecondFactor(1, code) {
		t.Fatal("same code accepted twice")
	}
	codes, err := a.newRecoveryCodes(1)
	if err != nil || len(codes) != recoveryCount {
		t.Fatal(err)
	}
	if !a.checkSecondFactor(1, strings.ToUpper(strings.ReplaceAll(codes[0], "-", ""))) {
		t.Fatal("recovery code rejected")
	}
	if a.checkSecondFactor(1, codes[0]) {
		t.Fatal("recovery code accepted twice")
	}
}

func TestLoginNeedsSecondFactorWhenEnabled(t *testing.T) {
	a := testApp(t)
	srv := httptest.NewServer(a.routes(fstest.MapFS{"index.html": {Data: []byte("ok")}}))
	defer srv.Close()
	post := func(path, body string, cookies []*http.Cookie) *http.Response {
		req, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader(body))
		req.Header.Set("X-OSSR", "1")
		req.Header.Set("Content-Type", "application/json")
		for _, c := range cookies {
			req.AddCookie(c)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	setupCode := setupCodeForTest(t, a)
	res := post("/api/setup", `{"code":"`+setupCode+`","email":"me@example.com","password":"correct horse battery"}`, nil)
	if res.StatusCode != 200 {
		t.Fatalf("setup: %d", res.StatusCode)
	}
	session := res.Cookies()

	// Turn 2FA on.
	res = post("/api/2fa/setup", ``, session)
	var setup struct{ Secret string }
	json.NewDecoder(res.Body).Decode(&setup)
	code, _ := totpCode(setup.Secret, time.Now().Unix()/totpStep)
	res = post("/api/2fa/enable", `{"code":"`+code+`"}`, session)
	var enabled struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	json.NewDecoder(res.Body).Decode(&enabled)
	if res.StatusCode != 200 || len(enabled.RecoveryCodes) != recoveryCount {
		t.Fatalf("enable: %d", res.StatusCode)
	}

	// Password alone no longer gives a session.
	res = post("/api/login", `{"email":"me@example.com","password":"correct horse battery"}`, nil)
	var login struct {
		Needs2FA bool `json:"needs_2fa"`
	}
	json.NewDecoder(res.Body).Decode(&login)
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			t.Fatal("session issued before second factor")
		}
	}
	if !login.Needs2FA {
		t.Fatal("expected needs_2fa")
	}
	mfa := res.Cookies()
	if res := post("/api/login/2fa", `{"code":"000000"}`, mfa); res.StatusCode != 401 {
		t.Fatalf("wrong code: %d", res.StatusCode)
	}
	res = post("/api/login/2fa", `{"code":"`+enabled.RecoveryCodes[0]+`"}`, mfa)
	if res.StatusCode != 200 {
		t.Fatalf("recovery code: %d", res.StatusCode)
	}
	got := false
	for _, c := range res.Cookies() {
		got = got || c.Name == sessionCookie
	}
	if !got {
		t.Fatal("no session after second factor")
	}
	// Without email set up, the recovery link is refused (and says why).
	res = post("/api/login", `{"email":"me@example.com","password":"correct horse battery"}`, nil)
	if res := post("/api/login/recover", ``, res.Cookies()); res.StatusCode != 409 {
		t.Fatalf("recover without smtp: %d", res.StatusCode)
	}
}

func setupCodeForTest(t *testing.T, a *App) string {
	t.Helper()
	a.cfg.DataDir = t.TempDir()
	if err := a.ensureSetupCode(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(a.setupCodePath())
	return strings.TrimSpace(string(b))
}

func TestTokensScopeAndMCP(t *testing.T) {
	a := testApp(t)
	a.db.Exec(`INSERT INTO users(id, email, pass_hash, created) VALUES(1, 'a@b.c', 'x', 0)`)
	a.db.Exec(`INSERT INTO api_tokens(name, token_hash, scope, created) VALUES('r', ?, 'read', 0)`, sha256Hex("ossr_read"))
	srv := httptest.NewServer(a.routes(fstest.MapFS{"index.html": {Data: []byte("ok")}}))
	defer srv.Close()
	call := func(method, path, token, body string) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	if code, _ := call("GET", "/api/services", "ossr_read", ""); code != 200 {
		t.Fatalf("read token GET: %d", code)
	}
	if code, _ := call("POST", "/api/services", "ossr_read", `{}`); code != 403 {
		t.Fatalf("read token POST: %d", code)
	}
	if code, _ := call("GET", "/api/services", "ossr_wrong", ""); code != 401 {
		t.Fatalf("bad token: %d", code)
	}
	if code, _ := call("GET", "/api/tokens", "ossr_read", ""); code != 401 {
		t.Fatalf("token reached session-only route: %d", code)
	}
	code, body := call("POST", "/api/mcp", "ossr_read", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if code != 200 || !strings.Contains(body, `"protocolVersion":"2025-06-18"`) {
		t.Fatalf("initialize: %d %s", code, body)
	}
	_, body = call("POST", "/api/mcp", "ossr_read", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if !strings.Contains(body, `"service_logs"`) || !strings.Contains(body, `"deploy"`) {
		t.Fatalf("tools/list: %s", body)
	}
	_, body = call("POST", "/api/mcp", "ossr_read", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"deploy","arguments":{"name":"x"}}}`)
	if !strings.Contains(body, `read-only`) || !strings.Contains(body, `"isError":true`) {
		t.Fatalf("read token deploy: %s", body)
	}
	if code, _ := call("POST", "/api/mcp", "", `{}`); code != 401 {
		t.Fatalf("mcp without token: %d", code)
	}
	if code, _ := call("POST", "/api/mcp", "ossr_read", `{"jsonrpc":"2.0","method":"notifications/initialized"}`); code != 202 {
		t.Fatalf("notification: %d", code)
	}
}

func TestParseProcNetLine(t *testing.T) {
	p, inode, ok := parseProcNetLine("   0: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0")
	if !ok || p.Port != 80 || p.Address != "0.0.0.0" || !p.Public || inode != "12345" {
		t.Fatalf("v4 any: %+v %s %v", p, inode, ok)
	}
	p, _, ok = parseProcNetLine("   1: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 9 1 0 100 0 0 10 0")
	if !ok || p.Port != 8080 || p.Address != "127.0.0.1" || p.Public {
		t.Fatalf("v4 loopback: %+v", p)
	}
	p, _, ok = parseProcNetLine("   0: 00000000000000000000000001000000:01BB 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 7 1 0 100 0 0 10 0")
	if !ok || p.Port != 443 || p.Address != "::1" {
		t.Fatalf("v6 loopback: %+v", p)
	}
	if _, _, ok := parseProcNetLine("   2: 0100007F:1F90 0100007F:D431 01 00000000:00000000 00:00000000 00000000     0        0 9 1 0 100 0 0 10 0"); ok {
		t.Fatal("established socket reported as listening")
	}
}

func TestSuggestLimits(t *testing.T) {
	if s := suggestLimits(usage{Samples: 5}, 0, 0); s.Memory != 0 || s.CPU != 0 {
		t.Fatal("suggested without enough data")
	}
	s := suggestLimits(usage{Samples: 100, CPUPeak: 40, MemPeak: 200 << 20}, 0, 0)
	if s.Memory != 320 || s.CPU != 0.75 {
		t.Fatalf("got %+v", s)
	}
	if s := suggestLimits(usage{Samples: 100, CPUPeak: 2, MemPeak: 10 << 20}, 0, 0); s.Memory != 128 || s.CPU != 0.25 {
		t.Fatalf("minimums: %+v", s)
	}
	if s := suggestLimits(usage{Samples: 100, CPUPeak: 95, MemPeak: 250 << 20}, 1, 256); len(s.Warnings) != 2 {
		t.Fatalf("warnings: %+v", s.Warnings)
	}
}

func TestLogStoreAndSearch(t *testing.T) {
	a := testApp(t)
	a.db.Exec(`INSERT INTO services(id, name, source, created) VALUES(1, 'web', 'image', 0)`)
	lines := []string{
		"2026-11-24T10:00:00.000000001Z GET /health 200",
		"2026-11-24T10:00:01.5Z ERROR: connect ECONNREFUSED 10.0.0.5:5432",
		"not a docker line",
	}
	for _, l := range lines {
		if ts, line, ok := parseLogLine(l); ok {
			a.db.Exec(`INSERT INTO log_lines(service_id, ts, line) VALUES(1, ?, ?)`, ts, line)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(a.handleLogSearch))
	defer srv.Close()
	get := func(q string) []logHit {
		res, err := http.Get(srv.URL + "?q=" + q)
		if err != nil {
			t.Fatal(err)
		}
		var hits []logHit
		json.NewDecoder(res.Body).Decode(&hits)
		return hits
	}
	if hits := get("econnrefused"); len(hits) != 1 || hits[0].Service != "web" || !strings.Contains(hits[0].Line, "5432") {
		t.Fatalf("search: %+v", hits)
	}
	if hits := get(`ERROR:%20"(`); hits == nil {
		t.Fatal("special characters broke the query")
	}
	if hits := get(""); len(hits) != 2 {
		t.Fatalf("latest: %d", len(hits))
	}
	a.db.Exec(`DELETE FROM log_lines WHERE line LIKE 'ERROR%'`)
	if hits := get("econnrefused"); len(hits) != 0 {
		t.Fatal("deleted line still in the index")
	}
}

func TestOnlyFailedSignInsCountTowardTheLimit(t *testing.T) {
	a := testApp(t)
	a.limiter = newRateLimiter(10, 15*time.Minute)
	srv := httptest.NewServer(a.routes(fstest.MapFS{"index.html": {Data: []byte("ok")}}))
	defer srv.Close()
	login := func(pw string) int {
		req, _ := http.NewRequest("POST", srv.URL+"/api/login", strings.NewReader(`{"email":"me@example.com","password":"`+pw+`"}`))
		req.Header.Set("X-OSSR", "1")
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode
	}
	code := setupCodeForTest(t, a)
	req, _ := http.NewRequest("POST", srv.URL+"/api/setup", strings.NewReader(`{"code":"`+code+`","email":"me@example.com","password":"right password 1"}`))
	req.Header.Set("X-OSSR", "1")
	http.DefaultClient.Do(req)

	for i := 0; i < 25; i++ { // the owner signing in many times (scripts, desktop app, new browsers)
		if c := login("right password 1"); c != 200 {
			t.Fatalf("successful sign-in %d got %d: successes must not count", i+1, c)
		}
	}
	for i := 0; i < 10; i++ {
		if c := login("wrong"); c != 401 {
			t.Fatalf("wrong password %d got %d", i+1, c)
		}
	}
	if c := login("right password 1"); c != 429 {
		t.Fatalf("after 10 failures the address must be blocked, got %d", c)
	}
}
