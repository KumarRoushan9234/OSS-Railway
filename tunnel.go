package main

import (
	"context"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// Remote access without a public IP or open ports: a Cloudflare Tunnel container
// (ossr-tunnel) makes an outbound connection and forwards HTTPS traffic to Caddy.
//   quick: a random https://<words>.trycloudflare.com address, no account needed (for testing)
//   token: your own tunnel from the Cloudflare dashboard, on your own domain (for real use)
// The tunnel exposes the panel to the internet: keep 2FA on.

const (
	tunnelContainer = "ossr-tunnel"
	tunnelImage     = "cloudflare/cloudflared:2025.11.1"
)

var quickURLRe = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

type tunnelStatus struct {
	Running bool   `json:"running"`
	Mode    string `json:"mode"`
	URL     string `json:"url"`
}

func (a *App) tunnelStatus(ctx context.Context) tunnelStatus {
	st := tunnelStatus{Mode: a.setting("tunnel_mode")}
	st.Running = containerRunning(ctx, tunnelContainer)
	if !st.Running {
		return st
	}
	switch st.Mode {
	case "quick":
		logs, _ := dockerCombined(ctx, "logs", tunnelContainer)
		st.URL = quickURLRe.FindString(logs)
	case "token":
		if h := a.setting("tunnel_host"); h != "" {
			st.URL = "https://" + h
		}
	}
	return st
}

// isTunnelHost reports whether a request arrived through the tunnel, which always uses HTTPS.
func (a *App) isTunnelHost(host string) bool {
	host = strings.Split(host, ":")[0]
	return strings.HasSuffix(host, ".trycloudflare.com") || (host != "" && host == a.setting("tunnel_host"))
}

// GET /api/tunnel
func (a *App) handleTunnelStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, a.tunnelStatus(r.Context()))
}

// POST /api/tunnel {mode: "quick"} or {mode: "token", token, host}
func (a *App) handleTunnelStart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mode  string `json:"mode"`
		Token string `json:"token"`
		Host  string `json:"host"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Host = strings.ToLower(strings.TrimSpace(in.Host))
	switch in.Mode {
	case "quick":
	case "token":
		if in.Token == "" && a.setting("tunnel_token") == "" {
			httpError(w, http.StatusBadRequest, "paste the tunnel token from the Cloudflare dashboard")
			return
		}
		if in.Host != "" && !validHost(in.Host) {
			httpError(w, http.StatusBadRequest, "invalid hostname")
			return
		}
		if len(in.Token) > 1000 {
			httpError(w, http.StatusBadRequest, "token is too long")
			return
		}
		if in.Token != "" {
			a.setSecretSetting("tunnel_token", strings.TrimSpace(in.Token))
		}
		a.setSetting("tunnel_host", in.Host)
	default:
		httpError(w, http.StatusBadRequest, "mode must be quick or token")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	removeContainerQuiet(ctx, tunnelContainer)
	args := []string{"run", "-d", "--name", tunnelContainer, "--restart", "unless-stopped",
		"--network", a.cfg.Network, "--label", "ossr.role=tunnel"}
	env := os.Environ()
	if in.Mode == "quick" {
		args = append(args, tunnelImage, "tunnel", "--no-autoupdate", "--url", "http://"+caddyContainer+":80")
	} else {
		// The token goes in the CLI's environment, never in its arguments.
		env = append(env, "TUNNEL_TOKEN="+a.secretSetting("tunnel_token"))
		args = append(args, "-e", "TUNNEL_TOKEN", tunnelImage, "tunnel", "--no-autoupdate", "run")
	}
	if err := dockerTo(ctx, nil, env, args...); err != nil {
		serverError(w, a, err)
		return
	}
	a.setSetting("tunnel_mode", in.Mode)
	// A quick tunnel prints its address within a few seconds.
	deadline := time.Now().Add(30 * time.Second)
	st := a.tunnelStatus(ctx)
	for in.Mode == "quick" && st.URL == "" && time.Now().Before(deadline) {
		time.Sleep(time.Second)
		st = a.tunnelStatus(ctx)
	}
	writeJSON(w, st)
}

// DELETE /api/tunnel
func (a *App) handleTunnelStop(w http.ResponseWriter, r *http.Request) {
	removeContainerQuiet(r.Context(), tunnelContainer)
	a.setSetting("tunnel_mode", "")
	w.WriteHeader(http.StatusNoContent)
}

func removeContainerQuiet(ctx context.Context, name string) {
	docker(ctx, "rm", "-f", name)
}
