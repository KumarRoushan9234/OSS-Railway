package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// Passkeys (WebAuthn): sign in with Windows Hello, Touch ID, a phone or a security key.
// A passkey is already two factors (the device plus its PIN or biometric), so it signs in
// without the authenticator-app code. Browsers only allow passkeys on HTTPS or localhost,
// and on a hostname, not a bare IP address.

const (
	ceremonyCookie = "ossr_wa"
	ceremonyTTL    = 5 * time.Minute
)

type ceremony struct {
	session webauthn.SessionData
	name    string // passkey name, when registering
	userID  int64  // 0 for sign-in
	expires time.Time
}

var ceremonies = struct {
	sync.Mutex
	m map[string]ceremony
}{m: map[string]ceremony{}}

func putCeremony(w http.ResponseWriter, r *http.Request, c ceremony) {
	token := randomToken(24)
	c.expires = time.Now().Add(ceremonyTTL)
	ceremonies.Lock()
	for k, v := range ceremonies.m {
		if time.Now().After(v.expires) {
			delete(ceremonies.m, k)
		}
	}
	ceremonies.m[token] = c
	ceremonies.Unlock()
	http.SetCookie(w, &http.Cookie{Name: ceremonyCookie, Value: token, Path: "/api/passkeys", MaxAge: int(ceremonyTTL.Seconds()),
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: isHTTPS(r)})
}

// takeCeremony returns and forgets the pending ceremony: each one can finish once.
func takeCeremony(r *http.Request) (ceremony, bool) {
	ck, err := r.Cookie(ceremonyCookie)
	if err != nil {
		return ceremony{}, false
	}
	ceremonies.Lock()
	defer ceremonies.Unlock()
	c, ok := ceremonies.m[ck.Value]
	delete(ceremonies.m, ck.Value)
	return c, ok && time.Now().Before(c.expires)
}

// relyingParty is the WebAuthn configuration for the host the browser is on.
// The passkey is bound to that hostname; the Origin header is accepted only for the same host
// (it differs only by port when the dashboard runs behind the dev server).
func (a *App) relyingParty(r *http.Request) (*webauthn.WebAuthn, error) {
	host := strings.Split(r.Host, ":")[0]
	scheme := "http"
	if isHTTPS(r) || a.isTunnelHost(r.Host) {
		scheme = "https"
	}
	origins := []string{scheme + "://" + r.Host}
	if o, err := url.Parse(r.Header.Get("Origin")); err == nil && o.Hostname() == host {
		origins = append(origins, o.Scheme+"://"+o.Host)
	}
	return webauthn.New(&webauthn.Config{RPID: host, RPDisplayName: "OSS Railway", RPOrigins: origins})
}

// passkeyUser adapts the account to the library's User interface.
type passkeyUser struct {
	id     int64
	handle []byte
	email  string
	creds  []webauthn.Credential
}

func (u *passkeyUser) WebAuthnID() []byte                         { return u.handle }
func (u *passkeyUser) WebAuthnName() string                       { return u.email }
func (u *passkeyUser) WebAuthnDisplayName() string                { return u.email }
func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func (a *App) loadPasskeyUser(uid int64) (*passkeyUser, error) {
	u := &passkeyUser{id: uid}
	var handle string
	if err := a.db.QueryRow(`SELECT email, webauthn_id FROM users WHERE id = ?`, uid).Scan(&u.email, &handle); err != nil {
		return nil, err
	}
	if handle == "" {
		handle = randomToken(32)
		a.db.Exec(`UPDATE users SET webauthn_id = ? WHERE id = ?`, handle, uid)
	}
	u.handle = []byte(handle)
	rows, err := a.db.Query(`SELECT credential FROM passkeys WHERE user_id = ?`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var c webauthn.Credential
		if rows.Scan(&raw) == nil && json.Unmarshal([]byte(raw), &c) == nil {
			u.creds = append(u.creds, c)
		}
	}
	return u, nil
}

// GET /api/passkeys
func (a *App) handlePasskeys(w http.ResponseWriter, r *http.Request) {
	uid := r.Context().Value(ctxKey{}).(int64)
	rows, err := a.db.Query(`SELECT id, name, created, last_used FROM passkeys WHERE user_id = ? ORDER BY created`, uid)
	if err != nil {
		serverError(w, a, err)
		return
	}
	defer rows.Close()
	type item struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Created  int64  `json:"created"`
		LastUsed int64  `json:"last_used"`
	}
	out := []item{}
	for rows.Next() {
		var it item
		if rows.Scan(&it.ID, &it.Name, &it.Created, &it.LastUsed) == nil {
			out = append(out, it)
		}
	}
	writeJSON(w, out)
}

// POST /api/passkeys/register/begin {name}
func (a *App) handlePasskeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	uid := r.Context().Value(ctxKey{}).(int64)
	var in struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 64 {
		httpError(w, http.StatusBadRequest, "name: 1–64 characters")
		return
	}
	wa, err := a.relyingParty(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, "passkeys need a hostname (not an IP address): "+err.Error())
		return
	}
	u, err := a.loadPasskeyUser(uid)
	if err != nil {
		serverError(w, a, err)
		return
	}
	var exclude []protocol.CredentialDescriptor
	for _, c := range u.creds {
		exclude = append(exclude, c.Descriptor())
	}
	opts, session, err := wa.BeginRegistration(u,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExclusions(exclude))
	if err != nil {
		serverError(w, a, err)
		return
	}
	putCeremony(w, r, ceremony{session: *session, name: in.Name, userID: uid})
	writeJSON(w, opts)
}

// POST /api/passkeys/register/finish (body: the browser's credential)
func (a *App) handlePasskeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	uid := r.Context().Value(ctxKey{}).(int64)
	c, ok := takeCeremony(r)
	if !ok || c.userID != uid {
		httpError(w, http.StatusBadRequest, "start again")
		return
	}
	wa, err := a.relyingParty(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := a.loadPasskeyUser(uid)
	if err != nil {
		serverError(w, a, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	cred, err := wa.FinishRegistration(u, c.session, r)
	if err != nil {
		httpError(w, http.StatusBadRequest, "the passkey could not be verified")
		return
	}
	raw, _ := json.Marshal(cred)
	id := base64.RawURLEncoding.EncodeToString(cred.ID)
	if _, err := a.db.Exec(`INSERT INTO passkeys(id, user_id, name, credential, created) VALUES(?, ?, ?, ?, ?)`, id, uid, c.name, string(raw), now()); err != nil {
		serverError(w, a, err)
		return
	}
	writeJSON(w, map[string]string{"id": id, "name": c.name})
}

// DELETE /api/passkeys/{id}
func (a *App) handleDeletePasskey(w http.ResponseWriter, r *http.Request) {
	uid := r.Context().Value(ctxKey{}).(int64)
	a.db.Exec(`DELETE FROM passkeys WHERE id = ? AND user_id = ?`, r.PathValue("id"), uid)
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/passkeys/login/begin
func (a *App) handlePasskeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	if a.limiter.blocked(clientIP(r)) {
		httpError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	wa, err := a.relyingParty(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, "passkeys need a hostname (not an IP address)")
		return
	}
	opts, session, err := wa.BeginDiscoverableLogin()
	if err != nil {
		serverError(w, a, err)
		return
	}
	putCeremony(w, r, ceremony{session: *session})
	writeJSON(w, opts)
}

// POST /api/passkeys/login/finish (body: the browser's assertion)
func (a *App) handlePasskeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	if a.limiter.blocked(clientIP(r)) {
		httpError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	c, ok := takeCeremony(r)
	if !ok || c.userID != 0 {
		httpError(w, http.StatusBadRequest, "start again")
		return
	}
	wa, err := a.relyingParty(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	var found *passkeyUser
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		var uid int64
		if err := a.db.QueryRow(`SELECT id FROM users WHERE webauthn_id = ?`, string(userHandle)).Scan(&uid); err != nil {
			return nil, errors.New("unknown passkey")
		}
		u, err := a.loadPasskeyUser(uid)
		found = u
		return u, err
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	_, cred, err := wa.FinishPasskeyLogin(handler, c.session, r)
	if err != nil || found == nil {
		a.limiter.fail(clientIP(r))
		httpError(w, http.StatusUnauthorized, "that passkey isn't registered here")
		return
	}
	raw, _ := json.Marshal(cred) // new signature counter
	a.db.Exec(`UPDATE passkeys SET credential = ?, last_used = ? WHERE id = ? AND user_id = ?`,
		string(raw), now(), base64.RawURLEncoding.EncodeToString(cred.ID), found.id)
	a.startSession(w, r, found.id)
	writeJSON(w, map[string]string{"email": found.email})
}
