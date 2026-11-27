package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Two-factor sign-in with an authenticator app (TOTP, RFC 6238: SHA-1, 6 digits, 30 s).
// If the phone is lost: a one-time recovery code, or a sign-in link sent to the account email.
// Using the email link turns 2FA off, so the owner can set it up again on a new phone.

const (
	totpStep       = 30
	totpDigits     = 6
	mfaCookie      = "ossr_mfa"
	mfaTTL         = 5 * time.Minute
	recoverTTL     = 15 * time.Minute
	maxMFAAttempts = 5
	recoveryCount  = 10
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func newTOTPSecret() string {
	b := make([]byte, 20)
	rand.Read(b)
	return b32.EncodeToString(b)
}

// totpCode returns the code for one 30-second step.
func totpCode(secret string, step int64) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", n%1_000_000), nil
}

// totpMatch checks the code against the current step and one step either side (clock drift).
// It returns the matched step, or 0.
func totpMatch(secret, code string, at time.Time) int64 {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return 0
	}
	now := at.Unix() / totpStep
	for _, step := range []int64{now - 1, now, now + 1} {
		want, err := totpCode(secret, step)
		if err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step
		}
	}
	return 0
}

func totpURI(secret, email string) string {
	v := url.Values{"secret": {secret}, "issuer": {"OSS Railway"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + url.PathEscape("OSS Railway:"+email) + "?" + v.Encode()
}

// recoveryCode looks like "7kq2m-x9d4p": 10 characters without look-alikes (no 0/o, 1/l/i).
func recoveryCode() string {
	const alphabet = "23456789abcdefghjkmnpqrstuvwxyz"
	b := make([]byte, 10)
	rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b[:5]) + "-" + string(b[5:])
}

func normalizeRecovery(c string) string {
	c = strings.ToLower(strings.TrimSpace(c))
	c = strings.ReplaceAll(c, " ", "")
	if len(c) == 10 {
		c = c[:5] + "-" + c[5:]
	}
	return c
}

func (a *App) userTOTP(uid int64) (secret string, last int64) {
	var enc string
	a.db.QueryRow(`SELECT totp_secret, totp_last FROM users WHERE id = ?`, uid).Scan(&enc, &last)
	if enc == "" {
		return "", last
	}
	secret, err := a.decrypt(enc)
	if err != nil {
		a.log.Error("decrypt totp secret", "err", err)
		return "", last
	}
	return secret, last
}

// checkSecondFactor accepts an authenticator code (each step only once) or an unused recovery code.
func (a *App) checkSecondFactor(uid int64, code string) bool {
	secret, last := a.userTOTP(uid)
	if secret == "" {
		return false
	}
	if step := totpMatch(secret, code, time.Now()); step > last {
		a.db.Exec(`UPDATE users SET totp_last = ? WHERE id = ?`, step, uid)
		return true
	}
	res, err := a.db.Exec(`DELETE FROM recovery_codes WHERE user_id = ? AND code_hash = ?`, uid, sha256Hex(normalizeRecovery(code)))
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

func (a *App) newRecoveryCodes(uid int64) ([]string, error) {
	tx, err := a.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM recovery_codes WHERE user_id = ?`, uid); err != nil {
		return nil, err
	}
	codes := make([]string, recoveryCount)
	for i := range codes {
		codes[i] = recoveryCode()
		if _, err := tx.Exec(`INSERT INTO recovery_codes(user_id, code_hash) VALUES(?, ?)`, uid, sha256Hex(codes[i])); err != nil {
			return nil, err
		}
	}
	return codes, tx.Commit()
}

func (a *App) newLoginToken(uid int64, kind string, ttl time.Duration) string {
	token := randomToken(32)
	a.db.Exec(`DELETE FROM login_tokens WHERE expires < ?`, now())
	a.db.Exec(`INSERT INTO login_tokens(token_hash, user_id, kind, expires) VALUES(?, ?, ?, ?)`, sha256Hex(token), uid, kind, time.Now().Add(ttl).Unix())
	return token
}

// loginToken returns the user of a live token of this kind, or 0.
func (a *App) loginToken(token, kind string) (uid int64, attempts int) {
	err := a.db.QueryRow(`SELECT user_id, attempts FROM login_tokens WHERE token_hash = ? AND kind = ? AND expires > ?`, sha256Hex(token), kind, now()).Scan(&uid, &attempts)
	if err != nil {
		return 0, 0
	}
	return uid, attempts
}

func (a *App) dropLoginToken(token string) {
	a.db.Exec(`DELETE FROM login_tokens WHERE token_hash = ?`, sha256Hex(token))
}

func setMFACookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: mfaCookie, Value: value, Path: "/api/login", MaxAge: maxAge, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: isHTTPS(r)})
}

// POST /api/login/2fa {code}: second step after a correct password.
func (a *App) handleLogin2FA(w http.ResponseWriter, r *http.Request) {
	if a.limiter.blocked(clientIP(r)) {
		httpError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	c, err := r.Cookie(mfaCookie)
	if err != nil {
		httpError(w, http.StatusUnauthorized, "sign in again")
		return
	}
	uid, attempts := a.loginToken(c.Value, "mfa")
	if uid == 0 {
		httpError(w, http.StatusUnauthorized, "sign in again")
		return
	}
	if !a.checkSecondFactor(uid, in.Code) {
		a.limiter.fail(clientIP(r))
		if attempts+1 >= maxMFAAttempts {
			a.dropLoginToken(c.Value)
			httpError(w, http.StatusUnauthorized, "too many wrong codes, sign in again")
			return
		}
		a.db.Exec(`UPDATE login_tokens SET attempts = attempts + 1 WHERE token_hash = ?`, sha256Hex(c.Value))
		httpError(w, http.StatusUnauthorized, "wrong code")
		return
	}
	a.dropLoginToken(c.Value)
	setMFACookie(w, r, "", -1)
	a.startSession(w, r, uid)
	writeJSON(w, map[string]bool{"ok": true})
}

// POST /api/login/recover: emails a one-time sign-in link. Needs the password step first.
func (a *App) handleLoginRecover(w http.ResponseWriter, r *http.Request) {
	if a.limiter.blocked(clientIP(r)) {
		httpError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	c, err := r.Cookie(mfaCookie)
	if err != nil {
		httpError(w, http.StatusUnauthorized, "sign in again")
		return
	}
	uid, _ := a.loginToken(c.Value, "mfa")
	if uid == 0 {
		httpError(w, http.StatusUnauthorized, "sign in again")
		return
	}
	if a.setting("smtp_host") == "" {
		httpError(w, http.StatusConflict, "email is not set up on this server; use a recovery code")
		return
	}
	a.limiter.fail(clientIP(r))
	var email string
	a.db.QueryRow(`SELECT email FROM users WHERE id = ?`, uid).Scan(&email)
	link := a.publicURL(r) + "/api/login/recover/" + a.newLoginToken(uid, "recover", recoverTTL)
	body := "Someone (hopefully you) asked to sign in to OSS Railway without the authenticator app.\n\n" +
		"Open this link within 15 minutes to sign in. It works once and turns two-factor sign-in off, so you can set it up again on your new device:\n\n" +
		link + "\n\nIf this wasn't you, change your password: someone knows it."
	if err := a.sendEmailTo(email, "[OSS Railway] Sign-in link", body); err != nil {
		serverError(w, a, err)
		return
	}
	writeJSON(w, map[string]string{"sent_to": maskEmail(email)})
}

// GET /api/login/recover/{token}: the emailed link. Signs in and turns 2FA off.
func (a *App) handleRecoverLink(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	uid, _ := a.loginToken(token, "recover")
	if uid == 0 {
		http.Redirect(w, r, "/#/?recover=expired", http.StatusSeeOther)
		return
	}
	a.dropLoginToken(token)
	a.db.Exec(`UPDATE users SET totp_secret = '', totp_pending = '' WHERE id = ?`, uid)
	a.db.Exec(`DELETE FROM recovery_codes WHERE user_id = ?`, uid)
	a.db.Exec(`DELETE FROM login_tokens WHERE user_id = ? AND kind = 'mfa'`, uid)
	a.addAlert("security", 0, "Two-factor sign-in was turned off with an email recovery link")
	a.startSession(w, r, uid)
	http.Redirect(w, r, "/#/settings?security=1", http.StatusSeeOther)
}

// publicURL is where links in emails point: the configured dashboard address, else this request's host.
func (a *App) publicURL(r *http.Request) string {
	if h := a.panelHost(); h != "" {
		return siteURL(h)
	}
	return a.baseURL(r)
}

func maskEmail(e string) string {
	at := strings.IndexByte(e, '@')
	if at < 2 {
		return e
	}
	return e[:1] + strings.Repeat("•", at-1) + e[at:]
}

// ---- managing 2FA (signed-in, browser session only) ----

// GET /api/2fa
func (a *App) handle2FAStatus(w http.ResponseWriter, r *http.Request) {
	uid := r.Context().Value(ctxKey{}).(int64)
	secret, _ := a.userTOTP(uid)
	var left int
	a.db.QueryRow(`SELECT COUNT(*) FROM recovery_codes WHERE user_id = ?`, uid).Scan(&left)
	writeJSON(w, map[string]any{"enabled": secret != "", "recovery_codes_left": left, "email_recovery": a.setting("smtp_host") != ""})
}

// POST /api/2fa/setup: new secret for the authenticator app (not active until confirmed).
func (a *App) handle2FASetup(w http.ResponseWriter, r *http.Request) {
	uid := r.Context().Value(ctxKey{}).(int64)
	if s, _ := a.userTOTP(uid); s != "" {
		httpError(w, http.StatusConflict, "two-factor sign-in is already on")
		return
	}
	secret := newTOTPSecret()
	enc, err := a.encrypt(secret)
	if err != nil {
		serverError(w, a, err)
		return
	}
	a.db.Exec(`UPDATE users SET totp_pending = ? WHERE id = ?`, enc, uid)
	var email string
	a.db.QueryRow(`SELECT email FROM users WHERE id = ?`, uid).Scan(&email)
	writeJSON(w, map[string]string{"secret": secret, "uri": totpURI(secret, email)})
}

// POST /api/2fa/enable {code}: confirms the app works, turns 2FA on, returns recovery codes once.
func (a *App) handle2FAEnable(w http.ResponseWriter, r *http.Request) {
	uid := r.Context().Value(ctxKey{}).(int64)
	var in struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	var enc string
	a.db.QueryRow(`SELECT totp_pending FROM users WHERE id = ?`, uid).Scan(&enc)
	secret, err := a.decrypt(enc)
	if enc == "" || err != nil {
		httpError(w, http.StatusConflict, "start setup again")
		return
	}
	step := totpMatch(secret, in.Code, time.Now())
	if step == 0 {
		httpError(w, http.StatusBadRequest, "wrong code; check the time on your phone")
		return
	}
	if _, err := a.db.Exec(`UPDATE users SET totp_secret = totp_pending, totp_pending = '', totp_last = ? WHERE id = ?`, step, uid); err != nil {
		serverError(w, a, err)
		return
	}
	codes, err := a.newRecoveryCodes(uid)
	if err != nil {
		serverError(w, a, err)
		return
	}
	writeJSON(w, map[string]any{"recovery_codes": codes})
}

// POST /api/2fa/disable {password, code}
func (a *App) handle2FADisable(w http.ResponseWriter, r *http.Request) {
	uid := r.Context().Value(ctxKey{}).(int64)
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if err := a.checkPassword(uid, in.Password); err != nil || !a.checkSecondFactor(uid, in.Code) {
		httpError(w, http.StatusUnauthorized, "wrong password or code")
		return
	}
	a.db.Exec(`UPDATE users SET totp_secret = '', totp_pending = '' WHERE id = ?`, uid)
	a.db.Exec(`DELETE FROM recovery_codes WHERE user_id = ?`, uid)
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/2fa/recovery-codes {code}: replaces all recovery codes.
func (a *App) handle2FARecoveryCodes(w http.ResponseWriter, r *http.Request) {
	uid := r.Context().Value(ctxKey{}).(int64)
	var in struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !a.checkSecondFactor(uid, in.Code) {
		httpError(w, http.StatusUnauthorized, "wrong code")
		return
	}
	codes, err := a.newRecoveryCodes(uid)
	if err != nil {
		serverError(w, a, err)
		return
	}
	writeJSON(w, map[string]any{"recovery_codes": codes})
}

func (a *App) checkPassword(uid int64, password string) error {
	var hash string
	if err := a.db.QueryRow(`SELECT pass_hash FROM users WHERE id = ?`, uid).Scan(&hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("no such user")
		}
		return err
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}
