package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fxamacker/cbor/v2"
)

// softKey is a minimal software authenticator: one P-256 key, "none" attestation.
type softKey struct {
	key    *ecdsa.PrivateKey
	id     []byte
	handle []byte
	count  uint32
}

var b64 = base64.RawURLEncoding

func (k *softKey) authData(rpID string, withCred bool) []byte {
	h := sha256.Sum256([]byte(rpID))
	var b bytes.Buffer
	b.Write(h[:])
	flags := byte(0x01 | 0x04) // user present, user verified
	if withCred {
		flags |= 0x40 // attested credential data
	}
	b.WriteByte(flags)
	binary.Write(&b, binary.BigEndian, k.count)
	if withCred {
		b.Write(make([]byte, 16)) // AAGUID
		binary.Write(&b, binary.BigEndian, uint16(len(k.id)))
		b.Write(k.id)
		cose, _ := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: k.key.X.FillBytes(make([]byte, 32)), -3: k.key.Y.FillBytes(make([]byte, 32))})
		b.Write(cose)
	}
	return b.Bytes()
}

func clientData(kind, challenge, origin string) []byte {
	b, _ := json.Marshal(map[string]string{"type": kind, "challenge": challenge, "origin": origin})
	return b
}

func TestPasskeyRegisterAndSignIn(t *testing.T) {
	a := testApp(t)
	a.db.Exec(`INSERT INTO users(id, email, pass_hash, created) VALUES(1, 'me@example.com', 'x', 0)`)
	srv := httptest.NewServer(a.routes(fstest.MapFS{"index.html": {Data: []byte("ok")}}))
	defer srv.Close()

	var cookies []*http.Cookie
	call := func(path string, body []byte, session bool) (int, []byte) {
		req, _ := http.NewRequest("POST", srv.URL+path, bytes.NewReader(body))
		req.Host = "localhost"
		req.Header.Set("Origin", "http://localhost")
		req.Header.Set("X-OSSR", "1")
		req.Header.Set("Content-Type", "application/json")
		for _, c := range cookies {
			if session || c.Name != sessionCookie {
				req.AddCookie(c)
			}
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		for _, nc := range res.Cookies() { // a newer cookie replaces one with the same name
			kept := cookies[:0]
			for _, c := range cookies {
				if c.Name != nc.Name {
					kept = append(kept, c)
				}
			}
			cookies = append(kept, nc)
		}
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}
	// Sign in the normal way to register a passkey.
	token := randomToken(32)
	a.db.Exec(`INSERT INTO sessions(token_hash, user_id, expires) VALUES(?, 1, ?)`, sha256Hex(token), now()+3600)
	cookies = []*http.Cookie{{Name: sessionCookie, Value: token}}

	code, body := call("/api/passkeys/register/begin", []byte(`{"name":"laptop"}`), true)
	if code != 200 {
		t.Fatalf("register begin: %d %s", code, body)
	}
	var create struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
			User      struct {
				ID string `json:"id"`
			} `json:"user"`
		} `json:"publicKey"`
	}
	json.Unmarshal(body, &create)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	k := &softKey{key: key, id: []byte("credential-id-123"), handle: []byte(create.PublicKey.User.ID)}
	attObj, _ := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": k.authData("localhost", true)})
	reg, _ := json.Marshal(map[string]any{
		"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id), "type": "public-key",
		"response": map[string]string{
			"clientDataJSON":    b64.EncodeToString(clientData("webauthn.create", create.PublicKey.Challenge, "http://localhost")),
			"attestationObject": b64.EncodeToString(attObj),
		},
	})
	if code, body := call("/api/passkeys/register/finish", reg, true); code != 200 {
		t.Fatalf("register finish: %d %s", code, body)
	}
	// Replaying the same finish must fail: each ceremony works once.
	if code, _ := call("/api/passkeys/register/finish", reg, true); code == 200 {
		t.Fatal("registration replay accepted")
	}

	// Sign in with the passkey, without a session.
	signIn := func(origin string, tamper bool) int {
		_, body := call("/api/passkeys/login/begin", nil, false)
		var get struct {
			PublicKey struct {
				Challenge string `json:"challenge"`
			} `json:"publicKey"`
		}
		json.Unmarshal(body, &get)
		k.count++
		ad := k.authData("localhost", false)
		cd := clientData("webauthn.get", get.PublicKey.Challenge, origin)
		sum := sha256.Sum256(cd)
		digest := sha256.Sum256(append(append([]byte{}, ad...), sum[:]...))
		sig, _ := ecdsa.SignASN1(rand.Reader, k.key, digest[:])
		if tamper {
			sig[len(sig)-1] ^= 0xff
		}
		handle, _ := b64.DecodeString(string(k.handle))
		if handle == nil {
			handle = k.handle
		}
		assertion, _ := json.Marshal(map[string]any{
			"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id), "type": "public-key",
			"response": map[string]string{
				"clientDataJSON": b64.EncodeToString(cd), "authenticatorData": b64.EncodeToString(ad),
				"signature": b64.EncodeToString(sig), "userHandle": b64.EncodeToString(handle),
			},
		})
		code, _ := call("/api/passkeys/login/finish", assertion, false)
		return code
	}
	if code := signIn("http://localhost", true); code != 401 {
		t.Fatalf("bad signature: %d", code)
	}
	if code := signIn("https://evil.example", false); code != 401 {
		t.Fatalf("wrong origin: %d", code)
	}
	if code := signIn("http://localhost", false); code != 200 {
		t.Fatalf("passkey sign-in: %d", code)
	}
	var last int64
	a.db.QueryRow(`SELECT last_used FROM passkeys`).Scan(&last)
	if last == 0 || !strings.Contains(cookieNames(cookies), sessionCookie) {
		t.Fatal("no session or last_used after passkey sign-in")
	}
}

func cookieNames(cs []*http.Cookie) string {
	var n []string
	for _, c := range cs {
		n = append(n, c.Name)
	}
	return strings.Join(n, ",")
}

func TestWebhookPayloadWorksForSlackAndDiscord(t *testing.T) {
	a := testApp(t)
	var got map[string]string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		if r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(400)
		}
	}))
	defer hook.Close()
	a.setSecretSetting("webhook_url", hook.URL)
	if err := a.sendWebhook("web crashed"); err != nil {
		t.Fatal(err)
	}
	if got["text"] != "web crashed" || got["content"] != "web crashed" { // Slack reads text, Discord reads content
		t.Fatalf("payload: %v", got)
	}
	hook.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	if err := a.sendWebhook("x"); err == nil {
		t.Fatal("a failed webhook must report an error")
	}
}

func TestPasswordChangeAuditAndTokenExpiry(t *testing.T) {
	a := testApp(t)
	srv := httptest.NewServer(a.routes(fstest.MapFS{"index.html": {Data: []byte("ok")}}))
	defer srv.Close()
	do := func(method, path, body string, cookies []*http.Cookie, bearer string) *http.Response {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("X-OSSR", "1")
		req.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		for _, c := range cookies {
			req.AddCookie(c)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	code := setupCodeForTest(t, a)
	first := do("POST", "/api/setup", `{"code":"`+code+`","email":"me@example.com","password":"alpha-secret-111"}`, nil, "").Cookies()
	second := do("POST", "/api/login", `{"email":"me@example.com","password":"alpha-secret-111"}`, nil, "").Cookies()

	if res := do("POST", "/api/password", `{"current":"wrong","new":"bravo-secret-222"}`, first, ""); res.StatusCode != 401 {
		t.Fatalf("wrong current password: %d", res.StatusCode)
	}
	res := do("POST", "/api/password", `{"current":"alpha-secret-111","new":"bravo-secret-222"}`, first, "")
	if res.StatusCode != 204 {
		t.Fatalf("change: %d", res.StatusCode)
	}
	if do("GET", "/api/me", "", second, "").Body != nil {
		var me struct{ User any }
		json.NewDecoder(do("GET", "/api/me", "", second, "").Body).Decode(&me)
		if me.User != nil {
			t.Fatal("other session survived a password change")
		}
	}
	if res := do("POST", "/api/login", `{"email":"me@example.com","password":"bravo-secret-222"}`, nil, ""); res.StatusCode != 200 {
		t.Fatalf("new password: %d", res.StatusCode)
	}

	// An expired token is refused.
	a.db.Exec(`INSERT INTO api_tokens(name, token_hash, scope, created, expires) VALUES('old', ?, 'full', 0, ?)`, sha256Hex("ossr_old"), now()-1)
	if res := do("GET", "/api/services", "", nil, "ossr_old"); res.StatusCode != 401 {
		t.Fatalf("expired token: %d", res.StatusCode)
	}

	// The audit log has the setup, the failed and good password changes are recorded without secrets.
	var actions []string
	rows, _ := a.db.Query(`SELECT actor || ' ' || action || ' ' || status FROM audit ORDER BY id`)
	for rows.Next() {
		var s string
		rows.Scan(&s)
		actions = append(actions, s)
	}
	rows.Close()
	all := strings.Join(actions, "\n")
	for _, want := range []string{"sign-in POST /api/setup 200", "me@example.com POST /api/password 204", "sign-in POST /api/login 200"} {
		if !strings.Contains(all, want) {
			t.Fatalf("audit missing %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "secret-111") || strings.Contains(all, "secret-222") {
		t.Fatal("secrets in the audit log")
	}
}
