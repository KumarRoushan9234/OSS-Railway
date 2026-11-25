package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

// MCP (Model Context Protocol) endpoint for AI agents such as Claude: POST /api/mcp with
// "Authorization: Bearer ossr_…". Stateless Streamable HTTP: every JSON-RPC request gets
// one JSON response. Read-only tokens can use every tool except deploy and restart.

var mcpVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	write       bool
}

func nameArg() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"name": map[string]any{"type": "string", "description": "Service name"}},
		"required":   []string{"name"},
	}
}

var mcpTools = []mcpTool{
	{Name: "list_services", Description: "List every service (apps and databases) with status, URLs and resource limits.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
	{Name: "service_status", Description: "Status, URLs, resource usage and the last 5 deployments of one service.", InputSchema: nameArg()},
	{Name: "service_logs", Description: "The last lines of a service's runtime logs.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":  map[string]any{"type": "string", "description": "Service name"},
			"lines": map[string]any{"type": "integer", "description": "How many lines, 1-500 (default 100)"},
		},
		"required": []string{"name"},
	}},
	{Name: "deployment_logs", Description: "Build and deploy log of a service's latest deployment.", InputSchema: nameArg()},
	{Name: "server_overview", Description: "Server CPU, memory and disk, every listening port, and every container on the machine.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
	{Name: "deploy", Description: "Build and deploy the service again (latest commit for Git services). Needs a full-access token.", InputSchema: nameArg(), write: true},
	{Name: "restart", Description: "Restart the service's running container. Needs a full-access token.", InputSchema: nameArg(), write: true},
}

func (a *App) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpError(w, http.StatusMethodNotAllowed, "POST JSON-RPC to this endpoint")
		return
	}
	bearer, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	uid, scope := a.tokenUser(bearer)
	if uid == 0 {
		w.Header().Set("WWW-Authenticate", `Bearer realm="OSS Railway"`)
		httpError(w, http.StatusUnauthorized, "create a token in Settings → API tokens")
		return
	}
	var req rpcRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || req.JSONRPC != "2.0" {
		writeRPC(w, nil, nil, &rpcError{-32700, "parse error"})
		return
	}
	if len(req.ID) == 0 { // notification, e.g. notifications/initialized
		w.WriteHeader(http.StatusAccepted)
		return
	}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(req.Params, &p)
		version := mcpVersions[0]
		if slices.Contains(mcpVersions, p.ProtocolVersion) {
			version = p.ProtocolVersion
		}
		writeRPC(w, req.ID, map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": "oss-railway", "version": "0.1"},
			"instructions":    "Manage apps and databases on this OSS Railway server. Use list_services first; services are addressed by name.",
		}, nil)
	case "ping":
		writeRPC(w, req.ID, map[string]any{}, nil)
	case "tools/list":
		writeRPC(w, req.ID, map[string]any{"tools": mcpTools}, nil)
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(req.Params, &p) != nil {
			writeRPC(w, req.ID, nil, &rpcError{-32602, "invalid params"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		out, err := a.callTool(ctx, p.Name, p.Arguments, scope)
		if i := slices.IndexFunc(mcpTools, func(t mcpTool) bool { return t.Name == p.Name }); i >= 0 && mcpTools[i].write && err == nil {
			var args struct {
				Name string `json:"name"`
			}
			json.Unmarshal(p.Arguments, &args)
			a.audit(r, a.actor(r), "MCP "+p.Name, args.Name, http.StatusOK)
		}
		if err != nil {
			writeRPC(w, req.ID, map[string]any{"content": []map[string]string{{"type": "text", "text": err.Error()}}, "isError": true}, nil)
			return
		}
		text, _ := json.MarshalIndent(out, "", "  ")
		writeRPC(w, req.ID, map[string]any{"content": []map[string]string{{"type": "text", "text": string(text)}}}, nil)
	default:
		writeRPC(w, req.ID, nil, &rpcError{-32601, "method not found: " + req.Method})
	}
}

func writeRPC(w http.ResponseWriter, id json.RawMessage, result any, e *rpcError) {
	resp := map[string]any{"jsonrpc": "2.0", "id": id}
	if e != nil {
		resp["error"] = e
	} else {
		resp["result"] = result
	}
	writeJSON(w, resp)
}

func (a *App) callTool(ctx context.Context, name string, raw json.RawMessage, scope string) (any, error) {
	i := slices.IndexFunc(mcpTools, func(t mcpTool) bool { return t.Name == name })
	if i < 0 {
		return nil, errors.New("unknown tool: " + name)
	}
	if mcpTools[i].write && scope != "full" {
		return nil, errors.New("this token is read-only; create a full-access token to " + name)
	}
	var args struct {
		Name  string `json:"name"`
		Lines int    `json:"lines"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &args) != nil {
		return nil, errors.New("invalid arguments")
	}
	var s *Service
	if name != "list_services" && name != "server_overview" {
		var err error
		if s, err = a.serviceByName(strings.ToLower(strings.TrimSpace(args.Name))); err != nil {
			return nil, errors.New("no service named " + strconv.Quote(args.Name) + "; call list_services")
		}
	}
	switch name {
	case "list_services":
		services, err := a.services()
		if err != nil {
			return nil, err
		}
		out := []serviceView{}
		for _, s := range services {
			out = append(out, a.view(ctx, s))
		}
		return out, nil
	case "service_status":
		deps, _ := a.recentDeployments(s.ID, 5)
		return map[string]any{"service": a.view(ctx, s), "deployments": deps}, nil
	case "service_logs":
		if s.Container == "" {
			return nil, errors.New("not deployed yet")
		}
		n := args.Lines
		if n <= 0 {
			n = 100
		}
		n = min(n, 500)
		out, err := exec.CommandContext(ctx, "docker", "logs", "--tail", strconv.Itoa(n), "--timestamps", s.Container).CombinedOutput()
		if err != nil {
			return nil, errors.New(firstLine(string(out)))
		}
		return string(out), nil
	case "deployment_logs":
		deps, _ := a.recentDeployments(s.ID, 1)
		if len(deps) == 0 {
			return nil, errors.New("no deployments yet")
		}
		return a.deployLogTail(deps[0].ID, 400), nil
	case "server_overview":
		var memUsed, memTotal int64
		if a.cfg.HostProc != "" {
			memUsed, memTotal = hostMem(a.cfg.HostProc)
		}
		diskTotal, diskUsed := diskUsage(a.cfg.HostRoot)
		return map[string]any{
			"cpus": hostCPUs(a.cfg.HostProc), "memory_used": human(memUsed), "memory_total": human(memTotal),
			"disk_used": human(diskUsed), "disk_total": human(diskTotal),
			"ports": listeningPorts(a.cfg.HostProc), "containers": a.hostContainers(ctx),
		}, nil
	case "deploy":
		id, err := a.deploy(s, "mcp", "")
		if err != nil {
			return nil, err
		}
		return map[string]any{"deployment": id, "status": "queued", "next": "call service_status to follow progress"}, nil
	case "restart":
		if s.Container == "" {
			return nil, errors.New("not deployed yet")
		}
		a.markStopping(s.Container)
		if _, err := docker(ctx, "restart", s.Container); err != nil {
			return nil, err
		}
		return map[string]string{"status": "restarted"}, nil
	}
	return nil, errors.New("unknown tool: " + name)
}
