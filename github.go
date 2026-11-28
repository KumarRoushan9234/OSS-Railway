package main

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// GitHub connection uses the GitHub App Manifest flow: the panel hands the
// browser a prefilled app definition, the user clicks "Create" on GitHub,
// and GitHub returns the app's keys. Nobody copies secrets by hand.

var githubAPI = "https://api.github.com"
var ghClient = &http.Client{Timeout: 20 * time.Second}

func (a *App) baseURL(r *http.Request) string {
	scheme := "http"
	if isHTTPS(r) || a.isTunnelHost(r.Host) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func isLocalHost(host string) bool {
	h := strings.Split(host, ":")[0]
	return h == "localhost" || strings.HasSuffix(h, ".localhost") || h == "127.0.0.1"
}

// GET /api/github: connection status.
func (a *App) handleGitHubStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"connected":     a.setting("gh_app_id") != "",
		"app_slug":      a.setting("gh_app_slug"),
		"installations": a.installations(),
	})
}

// DELETE /api/github: forget the connected app so it can be connected again, for example from another account.
// The app itself stays on github.com until its owner deletes it there.
func (a *App) handleGitHubDisconnect(w http.ResponseWriter, r *http.Request) {
	for _, key := range []string{"gh_app_id", "gh_app_slug", "gh_pem", "gh_webhook_secret", "gh_client_secret", "gh_client_id", "gh_installations", "gh_state"} {
		if err := a.setSetting(key, ""); err != nil {
			serverError(w, a, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/github/manifest: the form the browser POSTs to github.com.
// ?public=1 makes the app installable from any GitHub account or organization; by default only its creator can install it.
func (a *App) handleGitHubManifest(w http.ResponseWriter, r *http.Request) {
	base := a.baseURL(r)
	state := randomToken(16)
	a.setSetting("gh_state", state)
	manifest := map[string]any{
		"name":         "OSS Railway " + randomToken(3),
		"url":          base,
		"redirect_url": base + "/api/github/callback",
		"setup_url":    base + "/api/github/installed",
		"public":       r.URL.Query().Get("public") == "1",
		"hook_attributes": map[string]any{
			"url":    base + "/api/github/webhook",
			"active": !isLocalHost(r.Host), // GitHub cannot reach localhost
		},
		"default_permissions": map[string]string{"contents": "read", "metadata": "read"},
		"default_events":      []string{"push"},
	}
	b, _ := json.Marshal(manifest)
	target := "https://github.com/settings/apps/new"
	if org := r.URL.Query().Get("org"); org != "" {
		if !repoRe.MatchString(org + "/x") {
			httpError(w, http.StatusBadRequest, "invalid organization")
			return
		}
		target = "https://github.com/organizations/" + org + "/settings/apps/new"
	}
	writeJSON(w, map[string]string{"action": target + "?state=" + state, "manifest": string(b)})
}

// GET /api/github/callback?code=...&state=...: GitHub sends the browser back here after "Create".
func (a *App) handleGitHubCallback(w http.ResponseWriter, r *http.Request) {
	state := a.setting("gh_state")
	if state == "" || !hmac.Equal([]byte(r.URL.Query().Get("state")), []byte(state)) {
		httpError(w, http.StatusBadRequest, "invalid state, start the GitHub connection again")
		return
	}
	a.setSetting("gh_state", "")
	code := r.URL.Query().Get("code")
	if code == "" || len(code) > 100 {
		httpError(w, http.StatusBadRequest, "missing code")
		return
	}
	req, _ := http.NewRequestWithContext(r.Context(), "POST", githubAPI+"/app-manifests/"+url.PathEscape(code)+"/conversions", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := ghClient.Do(req)
	if err != nil {
		serverError(w, a, err)
		return
	}
	defer resp.Body.Close()
	var app struct {
		ID            int64  `json:"id"`
		Slug          string `json:"slug"`
		PEM           string `json:"pem"`
		WebhookSecret string `json:"webhook_secret"`
		ClientID      string `json:"client_id"`
		ClientSecret  string `json:"client_secret"`
	}
	if resp.StatusCode != http.StatusCreated {
		serverError(w, a, fmt.Errorf("github manifest conversion: %s", resp.Status))
		return
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&app); err != nil {
		serverError(w, a, err)
		return
	}
	a.setSetting("gh_app_id", strconv.FormatInt(app.ID, 10))
	a.setSetting("gh_app_slug", app.Slug)
	a.setSecretSetting("gh_pem", app.PEM)
	a.setSecretSetting("gh_webhook_secret", app.WebhookSecret)
	a.setSecretSetting("gh_client_secret", app.ClientSecret)
	a.setSetting("gh_client_id", app.ClientID)
	http.Redirect(w, r, "https://github.com/apps/"+url.PathEscape(app.Slug)+"/installations/new", http.StatusFound)
}

// GET /api/github/installed?installation_id=...: after the user installs the app on their repos.
func (a *App) handleGitHubInstalled(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("installation_id"), 10, 64)
	if err != nil {
		http.Redirect(w, r, "/#/new", http.StatusFound)
		return
	}
	// Only accept installations that belong to our app: the JWT call fails otherwise.
	if _, err := a.installationToken(r.Context(), id); err != nil {
		httpError(w, http.StatusBadRequest, "this installation does not belong to the connected GitHub App")
		return
	}
	ids := a.installations()
	for _, existing := range ids {
		if existing == id {
			http.Redirect(w, r, "/#/new", http.StatusFound)
			return
		}
	}
	ids = append(ids, id)
	b, _ := json.Marshal(ids)
	a.setSetting("gh_installations", string(b))
	http.Redirect(w, r, "/#/new", http.StatusFound)
}

func (a *App) installations() []int64 {
	ids := []int64{} // never null in JSON: the dashboard reads .length
	json.Unmarshal([]byte(a.setting("gh_installations")), &ids)
	if ids == nil {
		ids = []int64{}
	}
	return ids
}

type ghRepo struct {
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
	PushedAt      string `json:"pushed_at"`
	installation  int64
}

// GET /api/github/repos: every repo the app can see, across installations.
func (a *App) handleGitHubRepos(w http.ResponseWriter, r *http.Request) {
	repos, err := a.listRepos(r.Context())
	if err != nil {
		serverError(w, a, err)
		return
	}
	writeJSON(w, repos)
}

func (a *App) listRepos(ctx context.Context) ([]ghRepo, error) {
	var all []ghRepo
	for _, inst := range a.installations() {
		token, err := a.installationToken(ctx, inst)
		if err != nil {
			return nil, err
		}
		for page := 1; page <= 10; page++ {
			var body struct {
				Repositories []ghRepo `json:"repositories"`
			}
			if err := ghGet(ctx, token, fmt.Sprintf("/installation/repositories?per_page=100&page=%d", page), &body); err != nil {
				return nil, err
			}
			for i := range body.Repositories {
				body.Repositories[i].installation = inst
			}
			all = append(all, body.Repositories...)
			if len(body.Repositories) < 100 {
				break
			}
		}
	}
	return all, nil
}

// cloneSource returns the clone URL and extra git env (auth header) for a service.
// The token travels in the environment, never in argv or the URL, so it does not leak into logs or ps.
func (a *App) cloneSource(ctx context.Context, s *Service) (string, []string, error) {
	if s.Source == "git" {
		return s.Repo, nil, nil
	}
	repos, err := a.listRepos(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("github: %w", err)
	}
	for _, repo := range repos {
		if strings.EqualFold(repo.FullName, s.Repo) {
			token, err := a.installationToken(ctx, repo.installation)
			if err != nil {
				return "", nil, err
			}
			basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
			return "https://github.com/" + repo.FullName + ".git", []string{
				"GIT_CONFIG_COUNT=1",
				"GIT_CONFIG_KEY_0=http.https://github.com/.extraheader",
				"GIT_CONFIG_VALUE_0=AUTHORIZATION: basic " + basic,
			}, nil
		}
	}
	return "", nil, fmt.Errorf("the GitHub App has no access to %s (install it on that repo)", s.Repo)
}

// installationToken exchanges the app's private key for a 1-hour installation token.
func (a *App) installationToken(ctx context.Context, installation int64) (string, error) {
	jwt, err := a.appJWT()
	if err != nil {
		return "", err
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/app/installations/%d/access_tokens", githubAPI, installation), nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := ghClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("github installation token: %s", resp.Status)
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body); err != nil {
		return "", err
	}
	return body.Token, nil
}

func (a *App) appJWT() (string, error) {
	appID := a.setting("gh_app_id")
	key := a.secretSetting("gh_pem")
	if appID == "" || key == "" {
		return "", errors.New("GitHub is not connected")
	}
	return signJWT(appID, key, time.Now())
}

// signJWT builds the RS256 JWT GitHub requires for app authentication.
func signJWT(issuer, pemKey string, at time.Time) (string, error) {
	block, _ := pem.Decode([]byte(pemKey))
	if block == nil {
		return "", errors.New("invalid GitHub App private key")
	}
	var key *rsa.PrivateKey
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		key = k
	} else if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return "", errors.New("GitHub App key is not RSA")
		}
		key = rk
	} else {
		return "", errors.New("invalid GitHub App private key")
	}
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{
		"iat": at.Add(-60 * time.Second).Unix(), // allow for clock drift
		"exp": at.Add(9 * time.Minute).Unix(),
		"iss": issuer,
	})
	signing := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

func ghGet(ctx context.Context, token, path string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", githubAPI+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := ghClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("github %s: %s", path, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(out)
}

// POST /api/github/webhook: push events trigger deploys.
func (a *App) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if err != nil {
		httpError(w, http.StatusBadRequest, "bad body")
		return
	}
	secret := a.secretSetting("gh_webhook_secret")
	if secret == "" || !validSignature(secret, body, r.Header.Get("X-Hub-Signature-256")) {
		httpError(w, http.StatusUnauthorized, "bad signature")
		return
	}
	if r.Header.Get("X-GitHub-Event") != "push" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var ev struct {
		Ref        string `json:"ref"`
		Deleted    bool   `json:"deleted"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		HeadCommit struct {
			Message string `json:"message"`
		} `json:"head_commit"`
	}
	if err := json.Unmarshal(body, &ev); err != nil || ev.Deleted {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	branch := strings.TrimPrefix(ev.Ref, "refs/heads/")
	services, _ := a.services()
	n := 0
	for _, s := range services {
		if s.Source == "github" && s.AutoDeploy && strings.EqualFold(s.Repo, ev.Repository.FullName) && s.Branch == branch {
			if _, err := a.deploy(s, "push: "+firstLine(ev.HeadCommit.Message), ""); err == nil {
				n++
			}
		}
	}
	writeJSON(w, map[string]int{"deploys": n})
}

func validSignature(secret string, body []byte, header string) bool {
	sig, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
