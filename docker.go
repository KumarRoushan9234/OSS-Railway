package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// docker runs the docker CLI with an argument list (never a shell string)
// and returns trimmed stdout.
func docker(ctx context.Context, args ...string) (string, error) {
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", args[0], err, lastLines(errb.String(), 5))
	}
	return strings.TrimSpace(out.String()), nil
}

// dockerTo streams combined output into w (build logs, runtime logs).
func dockerTo(ctx context.Context, w io.Writer, env []string, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = w
	cmd.Stderr = w
	if env != nil {
		cmd.Env = env
	}
	return cmd.Run()
}

// dockerCombined returns stdout and stderr together (for commands like logs).
func dockerCombined(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	return string(out), err
}

func containerRunning(ctx context.Context, name string) bool {
	out, err := docker(ctx, "inspect", "-f", "{{.State.Running}}", name)
	return err == nil && out == "true"
}

func containerExists(ctx context.Context, name string) bool {
	_, err := docker(ctx, "inspect", "-f", "{{.Id}}", name)
	return err == nil
}

// removeContainer stops a container we own; the event watcher ignores the resulting "die".
func (a *App) removeContainer(ctx context.Context, name string) {
	if name == "" {
		return
	}
	a.markStopping(name)
	docker(ctx, "rm", "-f", name)
}

func (a *App) ensureNetwork(ctx context.Context) error {
	if _, err := docker(ctx, "network", "inspect", a.cfg.Network); err == nil {
		return nil
	}
	_, err := docker(ctx, "network", "create", a.cfg.Network)
	return err
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
