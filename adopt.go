package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Adopting: most servers already run something. Any container OSS Railway didn't create can be
// adopted with one click. The panel then shows it on the canvas with logs, metrics, crash alerts,
// restart/stop/start and live CPU/memory limits, but never rebuilds, recreates or re-routes it:
// the container stays exactly as its owner set it up (docker run, Compose, ...).
// "Stop managing" forgets it again without touching it.

const externalSource = "external"

func (s *Service) IsExternal() bool { return s.Source == externalSource }

var errExternal = errors.New("this is an adopted container: OSS Railway doesn't build or recreate it. Use Restart, Stop or Start, or redeploy it the way it was created")

// systemContainer reports containers that belong to OSS Railway itself.
func systemContainer(name string, labels map[string]string) bool {
	return labels["ossr.service"] != "" || labels["ossr.role"] != "" || name == "ossr-panel" || name == caddyContainer || name == tunnelContainer
}

type externalContainer struct {
	Name      string `json:"name"`
	Image     string `json:"image"`
	State     string `json:"state"`
	Status    string `json:"status"`
	Ports     string `json:"ports"`
	Project   string `json:"project"`    // Docker Compose project, if any
	ServiceID int64  `json:"service_id"` // set once adopted
}

func parseLabels(s string) map[string]string {
	m := map[string]string{}
	for _, kv := range strings.Split(s, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

// externalContainers lists every container OSS Railway did not create, adopted or not.
func (a *App) externalContainers(ctx context.Context) ([]externalContainer, error) {
	out, err := docker(ctx, "ps", "-a", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	adopted := map[string]int64{}
	services, _ := a.services()
	for _, s := range services {
		if s.IsExternal() {
			adopted[s.Container] = s.ID
		}
	}
	list := []externalContainer{}
	for _, line := range strings.Split(out, "\n") {
		var c struct{ Names, Image, State, Status, Ports, Labels string }
		if json.Unmarshal([]byte(line), &c) != nil {
			continue
		}
		labels := parseLabels(c.Labels)
		if systemContainer(c.Names, labels) {
			continue
		}
		list = append(list, externalContainer{Name: c.Names, Image: c.Image, State: c.State, Status: c.Status, Ports: c.Ports,
			Project: labels["com.docker.compose.project"], ServiceID: adopted[c.Names]})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Project != list[j].Project {
			return list[i].Project < list[j].Project
		}
		return list[i].Name < list[j].Name
	})
	return list, nil
}

// GET /api/external
func (a *App) handleExternal(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	list, err := a.externalContainers(ctx)
	if err != nil {
		serverError(w, a, err)
		return
	}
	writeJSON(w, list)
}

var slugRe = regexp.MustCompile(`[^a-z0-9-]+`)

// serviceNameFor turns a container name into a free, valid service name: "kx-gateway" stays,
// "My_App.1" becomes "my-app-1", a clash gets "-2".
func (a *App) serviceNameFor(container string) string {
	base := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(container), "-"), "-")
	if base == "" || base[0] < 'a' || base[0] > 'z' {
		base = "c-" + base
	}
	if base == "caddy" || base == "panel" {
		base = "my-" + base
	}
	if len(base) > 28 {
		base = strings.Trim(base[:28], "-")
	}
	if len(base) < 2 {
		base += "-c"
	}
	name := base
	for i := 2; ; i++ {
		if _, err := a.serviceByName(name); err != nil {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}

// adopt registers one existing container as a service. It does not change the container.
func (a *App) adopt(ctx context.Context, container string) (*Service, error) {
	raw, err := docker(ctx, "inspect", container)
	if err != nil {
		return nil, fmt.Errorf("no container named %q", container)
	}
	var info []struct {
		Name   string
		Config struct {
			Image  string
			Labels map[string]string
		}
		NetworkSettings struct {
			Ports map[string][]struct{ HostIP, HostPort string }
		}
	}
	if json.Unmarshal([]byte(raw), &info) != nil || len(info) == 0 {
		return nil, fmt.Errorf("could not read container %q", container)
	}
	c := info[0]
	name := strings.TrimPrefix(c.Name, "/")
	if systemContainer(name, c.Config.Labels) {
		return nil, fmt.Errorf("%s is part of OSS Railway already", name)
	}
	services, _ := a.services()
	for _, s := range services {
		if s.IsExternal() && s.Container == name {
			return s, nil // already adopted: adopting twice is harmless
		}
	}
	// The first published port, so the card can link to it.
	port := 0
	keys := make([]string, 0, len(c.NetworkSettings.Ports))
	for k := range c.NetworkSettings.Ports {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, b := range c.NetworkSettings.Ports[k] {
			if p, err := strconv.Atoi(b.HostPort); err == nil && port == 0 && strings.HasSuffix(k, "/tcp") {
				port = p
			}
		}
	}
	res, err := a.db.Exec(`INSERT INTO services(name, source, image, port, container, autodeploy, created) VALUES(?, ?, ?, ?, ?, 0, ?)`,
		a.serviceNameFor(name), externalSource, c.Config.Image, port, name, now())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return a.service(id)
}

// POST /api/adopt {containers: [...]} or {project: "..."} (every container of a Compose project)
func (a *App) handleAdopt(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Containers []string `json:"containers"`
		Project    string   `json:"project"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	names := in.Containers
	if in.Project != "" {
		list, err := a.externalContainers(ctx)
		if err != nil {
			serverError(w, a, err)
			return
		}
		for _, c := range list {
			if c.Project == in.Project {
				names = append(names, c.Name)
			}
		}
	}
	if len(names) == 0 || len(names) > 100 {
		httpError(w, http.StatusBadRequest, "pick 1 to 100 containers")
		return
	}
	out := []serviceView{}
	for _, n := range names {
		s, err := a.adopt(ctx, n)
		if err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		out = append(out, a.view(ctx, s))
	}
	writeJSON(w, out)
}

// POST /api/services/{id}/stop and /start. Works for every service; a stop is never reported as a crash.
func (a *App) handleStop(w http.ResponseWriter, r *http.Request) {
	a.startStop(w, r, "stop")
}

func (a *App) handleStart(w http.ResponseWriter, r *http.Request) {
	a.startStop(w, r, "start")
}

func (a *App) startStop(w http.ResponseWriter, r *http.Request, action string) {
	s, ok := a.serviceFromPath(w, r)
	if !ok {
		return
	}
	if s.Container == "" {
		httpError(w, http.StatusConflict, "not deployed yet")
		return
	}
	if action == "stop" {
		a.markStopping(s.Container)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	if _, err := docker(ctx, action, s.Container); err != nil {
		serverError(w, a, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
