package main

import (
	"net/http"
	"strings"
)

// API tokens let scripts and AI agents use the API and the MCP endpoint.
// Only a SHA-256 of each token is stored; the token itself is shown once.

const tokenPrefix = "ossr_"

// tokenUser returns the owner (the single admin) and scope of a valid token, or 0.
func (a *App) tokenUser(token string) (int64, string) {
	if !strings.HasPrefix(token, tokenPrefix) || len(token) > 128 {
		return 0, ""
	}
	var id, expires int64
	var scope string
	if a.db.QueryRow(`SELECT id, scope, expires FROM api_tokens WHERE token_hash = ?`, sha256Hex(token)).Scan(&id, &scope, &expires) != nil {
		return 0, ""
	}
	if expires != 0 && expires < now() {
		return 0, ""
	}
	a.db.Exec(`UPDATE api_tokens SET last_used = ? WHERE id = ?`, now(), id)
	var uid int64
	a.db.QueryRow(`SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&uid)
	return uid, scope
}

type apiToken struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Scope    string `json:"scope"`
	Created  int64  `json:"created"`
	LastUsed int64  `json:"last_used"`
	Expires  int64  `json:"expires"`
}

// GET /api/tokens
func (a *App) handleTokens(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(`SELECT id, name, scope, created, last_used, expires FROM api_tokens ORDER BY id DESC`)
	if err != nil {
		serverError(w, a, err)
		return
	}
	defer rows.Close()
	out := []apiToken{}
	for rows.Next() {
		var t apiToken
		if rows.Scan(&t.ID, &t.Name, &t.Scope, &t.Created, &t.LastUsed, &t.Expires) == nil {
			out = append(out, t)
		}
	}
	writeJSON(w, out)
}

// POST /api/tokens {name, scope}: returns the token once.
func (a *App) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name    string `json:"name"`
		Scope   string `json:"scope"`
		Expires int    `json:"expires_days"` // 0 = never
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 64 {
		httpError(w, http.StatusBadRequest, "name: 1–64 characters")
		return
	}
	if in.Scope != "read" && in.Scope != "full" {
		httpError(w, http.StatusBadRequest, "scope must be read or full")
		return
	}
	if in.Expires < 0 || in.Expires > 3650 {
		httpError(w, http.StatusBadRequest, "expiry must be 0 (never) to 3650 days")
		return
	}
	var expires int64
	if in.Expires > 0 {
		expires = now() + int64(in.Expires)*86400
	}
	token := tokenPrefix + randomToken(24)
	res, err := a.db.Exec(`INSERT INTO api_tokens(name, token_hash, scope, created, expires) VALUES(?, ?, ?, ?, ?)`, in.Name, sha256Hex(token), in.Scope, now(), expires)
	if err != nil {
		serverError(w, a, err)
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, map[string]any{"id": id, "token": token, "mcp_url": a.publicURL(r) + "/api/mcp"})
}

// DELETE /api/tokens/{id}
func (a *App) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		httpError(w, http.StatusNotFound, "token not found")
		return
	}
	a.db.Exec(`DELETE FROM api_tokens WHERE id = ?`, id)
	w.WriteHeader(http.StatusNoContent)
}
