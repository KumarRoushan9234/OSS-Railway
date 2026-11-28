package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

const caddyContainer = "ossr-caddy"
const caddyImage = "caddy:2-alpine"

var proxyMu sync.Mutex

var hostRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9-]{2,63}$`)

func validHost(h string) bool { return len(h) <= 253 && hostRe.MatchString(h) }

// ensureProxy starts the Caddy container on ports 80/443 and loads the current routes.
func (a *App) ensureProxy(ctx context.Context) error {
	if err := a.ensureNetwork(ctx); err != nil {
		return err
	}
	if !containerExists(ctx, caddyContainer) {
		_, err := docker(ctx, "run", "-d", "--name", caddyContainer, "--restart", "unless-stopped",
			"--network", a.cfg.Network,
			"-p", "80:80", "-p", "443:443", "-p", "443:443/udp",
			"-v", "ossr-caddy-data:/data", "-v", "ossr-caddy-config:/config",
			"--label", "ossr.role=proxy",
			caddyImage)
		if err != nil {
			return err
		}
	}
	return a.reloadProxy(ctx)
}

func dashedIP(ip string) string { return strings.ReplaceAll(ip, ".", "-") }

// panelHost is the HTTPS hostname of the dashboard ("" = only plain http on the IP).
func (a *App) panelHost() string {
	if d := a.setting("panel_domain"); d != "" {
		return d
	}
	if a.cfg.PublicIP != "" {
		return dashedIP(a.cfg.PublicIP) + ".sslip.io"
	}
	return ""
}

// serviceHosts are the hostnames routed to an app: its custom domain plus a default one.
func (a *App) serviceHosts(s *Service) []string {
	var hosts []string
	if s.Domain != "" {
		hosts = append(hosts, s.Domain)
	}
	switch base := a.setting("base_domain"); {
	case base != "":
		hosts = append(hosts, s.Name+"."+base)
	case a.cfg.PublicIP != "":
		hosts = append(hosts, s.Name+"."+dashedIP(a.cfg.PublicIP)+".sslip.io")
	default:
		hosts = append(hosts, s.Name+".localhost")
	}
	return hosts
}

func siteURL(host string) string {
	if strings.HasSuffix(host, ".localhost") {
		return "http://" + host
	}
	return "https://" + host
}

func siteAddr(host string) string {
	if strings.HasSuffix(host, ".localhost") {
		return "http://" + host
	}
	return host
}

type route struct {
	hosts    []string
	upstream string
}

// caddyfile renders the full proxy config. Every host was validated on input,
// and is validated again here so a bad row can never inject Caddy directives.
func caddyfile(email, panelHost, panelUpstream string, routes []route) string {
	var b strings.Builder
	if email != "" && !strings.ContainsAny(email, " \n{}") {
		fmt.Fprintf(&b, "{\n\temail %s\n}\n\n", email)
	}
	// Plain-http catch-all so a fresh server answers on http://<ip> before any domain exists.
	fmt.Fprintf(&b, ":80 {\n\treverse_proxy %s\n}\n\n", panelUpstream)
	if panelHost != "" && validHost(panelHost) {
		fmt.Fprintf(&b, "%s {\n\treverse_proxy %s\n}\n\n", panelHost, panelUpstream)
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].upstream < routes[j].upstream })
	for _, r := range routes {
		var addrs []string
		for _, h := range r.hosts {
			if validHost(h) {
				addrs = append(addrs, siteAddr(h))
			}
		}
		if len(addrs) == 0 {
			continue
		}
		fmt.Fprintf(&b, "%s {\n\treverse_proxy %s\n}\n\n", strings.Join(addrs, ", "), r.upstream)
	}
	return b.String()
}

func (a *App) reloadProxy(ctx context.Context) error {
	proxyMu.Lock()
	defer proxyMu.Unlock()
	services, err := a.services()
	if err != nil {
		return err
	}
	var routes []route
	for _, s := range services {
		if s.IsDatabase() || s.IsExternal() || s.Container == "" || s.Port == 0 {
			continue
		}
		routes = append(routes, route{hosts: a.serviceHosts(s), upstream: fmt.Sprintf("%s:%d", s.Container, s.Port)})
	}
	conf := caddyfile(a.setting("acme_email"), a.panelHost(), a.cfg.Upstream, routes)
	path := filepath.Join(a.cfg.DataDir, "Caddyfile")
	if err := os.WriteFile(path, []byte(conf), 0o600); err != nil {
		return err
	}
	if _, err := docker(ctx, "cp", path, caddyContainer+":/etc/caddy/Caddyfile"); err != nil {
		return err
	}
	_, err = docker(ctx, "exec", caddyContainer, "caddy", "reload", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
	return err
}
