package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AI analysis: send a service's recent logs to any OpenAI-compatible chat endpoint and get
// back the likely cause and a fix. Off until an endpoint is set. Point it at Ollama on this
// server or your own computer (http://host.docker.internal:11434/v1) and nothing leaves
// your machines; point it at a hosted API only if you're fine sending logs there.

const aiTimeout = 3 * time.Minute

const aiSystemPrompt = `You help people run apps on their own server with OSS Railway (Docker containers behind Caddy; builds use a Dockerfile or Railpack).
You get a service's details and recent logs. Reply in plain text, under 200 words:
1. What went wrong, in one or two sentences, quoting the log line that shows it.
2. The most likely fix, as short numbered steps. Mention OSS Railway settings (Variables, Port, Root directory, Resources) when they're the fix.
If the logs look healthy, say so. Never invent log lines.`

func validAIURL(u string) bool {
	p, err := url.Parse(u)
	return err == nil && (p.Scheme == "http" || p.Scheme == "https") && p.Host != ""
}

// askAI sends one chat completion request and returns the reply text.
func (a *App) askAI(ctx context.Context, system, user string) (string, error) {
	base, model := strings.TrimRight(a.setting("ai_url"), "/"), a.setting("ai_model")
	if base == "" || model == "" {
		return "", errors.New("set an AI endpoint and model in Settings first")
	}
	body, _ := json.Marshal(map[string]any{
		"model":       model,
		"temperature": 0.2,
		"stream":      false,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	})
	ctx, cancel := context.WithTimeout(ctx, aiTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := a.secretSetting("ai_key"); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not reach the AI endpoint: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("AI endpoint returned %s: %s", res.Status, firstLine(string(raw)))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(raw, &out) != nil || len(out.Choices) == 0 {
		return "", errors.New("the AI endpoint's reply was not an OpenAI-style chat completion")
	}
	return stripThinking(out.Choices[0].Message.Content), nil
}

// stripThinking drops <think>…</think> blocks that reasoning models put before the answer.
func stripThinking(s string) string {
	for {
		i := strings.Index(s, "<think>")
		j := strings.Index(s, "</think>")
		if i < 0 || j < i {
			return strings.TrimSpace(s)
		}
		s = s[:i] + s[j+len("</think>"):]
	}
}

// POST /api/services/{id}/explain {deployment}: explains a deployment's build log,
// or with no deployment, the service's latest runtime logs.
func (a *App) handleExplain(w http.ResponseWriter, r *http.Request) {
	s, ok := a.serviceFromPath(w, r)
	if !ok {
		return
	}
	var in struct {
		Deployment int64 `json:"deployment"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	v := a.view(r.Context(), s)
	var b strings.Builder
	fmt.Fprintf(&b, "Service: %s (%s %s%s)\nStatus: %s\nPort: %d\nLimits: CPU %v cores, memory %d MB (0 = none)\n",
		s.Name, v.Kind, s.Source, map[bool]string{true: " " + s.Repo + s.Image, false: ""}[s.Repo+s.Image != ""], v.Status, s.Port, s.CPU, s.Memory)
	if in.Deployment > 0 {
		var status, msg string
		if a.db.QueryRow(`SELECT status, message FROM deployments WHERE id = ? AND service_id = ?`, in.Deployment, s.ID).Scan(&status, &msg) != nil {
			httpError(w, http.StatusNotFound, "deployment not found")
			return
		}
		fmt.Fprintf(&b, "Deployment #%d: %s — %s\n\nBuild and deploy log (last lines):\n%s\n", in.Deployment, status, msg, a.deployLogTail(in.Deployment, 150))
	} else {
		fmt.Fprintf(&b, "\nRuntime log (last lines):\n%s\n", a.recentLogText(s.ID, 150))
	}
	rows, err := a.db.Query(`SELECT message FROM alerts WHERE service_id = ? ORDER BY ts DESC LIMIT 5`, s.ID)
	if err == nil {
		b.WriteString("\nRecent alerts:\n")
		for rows.Next() {
			var m string
			rows.Scan(&m)
			b.WriteString("- " + m + "\n")
		}
		rows.Close()
	}
	reply, err := a.askAI(r.Context(), aiSystemPrompt, b.String())
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]string{"answer": reply, "model": a.setting("ai_model")})
}

// POST /api/ai/test: checks the endpoint and model answer.
func (a *App) handleAITest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	reply, err := a.askAI(r.Context(), "Answer with one word.", "Say OK.")
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"reply": reply, "seconds": time.Since(start).Seconds()})
}

func (a *App) recentLogText(serviceID int64, n int) string {
	rows, err := a.db.Query(`SELECT line FROM (SELECT id, line FROM log_lines WHERE service_id = ? ORDER BY id DESC LIMIT ?) ORDER BY id`, serviceID, n)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var l string
		rows.Scan(&l)
		lines = append(lines, l)
	}
	if len(lines) == 0 {
		return "(no logs stored yet)"
	}
	return strings.Join(lines, "\n")
}
