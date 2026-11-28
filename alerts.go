package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const alertCooldown = 15 * time.Minute

var (
	lastSent   = map[string]time.Time{}
	lastSentMu sync.Mutex
	crashes    = map[int64][]time.Time{}
)

// notify records an alert and sends it by email and webhook, at most once
// per kind and service every 15 minutes so a crash loop does not flood inboxes.
func (a *App) notify(kind string, serviceID int64, msg string) {
	key := kind + ":" + strconv.FormatInt(serviceID, 10)
	lastSentMu.Lock()
	if time.Since(lastSent[key]) < alertCooldown {
		lastSentMu.Unlock()
		return
	}
	lastSent[key] = time.Now()
	lastSentMu.Unlock()
	a.addAlert(kind, serviceID, msg)
	a.log.Warn("alert", "kind", kind, "message", msg)
	go a.send("[OSS Railway] "+msg, msg+"\n\nTime: "+time.Now().UTC().Format(time.RFC1123))
}

func (a *App) send(subject, body string) {
	if err := a.sendEmail(subject, body); err != nil {
		a.log.Error("send alert email", "err", err)
	}
	if err := a.sendWebhook(subject); err != nil {
		a.log.Error("send alert webhook", "err", err)
	}
}

// sendEmail sends to the alert addresses; it does nothing until SMTP is set up.
func (a *App) sendEmail(subject, body string) error {
	if a.setting("alert_to") == "" {
		return nil
	}
	return a.sendEmailTo(a.setting("alert_to"), subject, body)
}

func (a *App) sendEmailTo(to, subject, body string) error {
	host := a.setting("smtp_host")
	if host == "" || to == "" {
		return nil
	}
	port := a.setting("smtp_port")
	if port == "" {
		port = "587"
	}
	from := a.setting("alert_from")
	if from == "" {
		from = a.setting("smtp_user")
	}
	user, pass := a.setting("smtp_user"), a.secretSetting("smtp_pass")
	clean := func(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }
	msg := "From: " + clean(from) + "\r\nTo: " + clean(to) + "\r\nSubject: " + clean(subject) +
		"\r\nDate: " + time.Now().Format(time.RFC1123Z) +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" +
		strings.ReplaceAll(body, "\n", "\r\n")

	addr := net.JoinHostPort(host, port)
	var c *smtp.Client
	var err error
	if port == "465" {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", addr, &tls.Config{ServerName: host})
		if err != nil {
			return err
		}
		c, err = smtp.NewClient(conn, host)
		if err != nil {
			return err
		}
	} else {
		conn, err := net.DialTimeout("tcp", addr, 15*time.Second)
		if err != nil {
			return err
		}
		c, err = smtp.NewClient(conn, host)
		if err != nil {
			return err
		}
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
				return err
			}
		} else if user != "" {
			return errors.New("SMTP server does not support STARTTLS; refusing to send the password in clear text")
		}
	}
	defer c.Close()
	if user != "" {
		if err = c.Auth(smtp.PlainAuth("", user, pass, host)); err != nil {
			return err
		}
	}
	if err = c.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range strings.Split(to, ",") {
		if err = c.Rcpt(strings.TrimSpace(rcpt)); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err = w.Write([]byte(msg)); err != nil {
		return err
	}
	if err = w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// sendWebhook posts {"text","content"} so Slack and Discord incoming webhooks both work.
func (a *App) sendWebhook(text string) error {
	hook := a.secretSetting("webhook_url")
	if hook == "" {
		return nil
	}
	b, _ := json.Marshal(map[string]string{"text": text, "content": text})
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(hook, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %s", resp.Status)
	}
	return nil
}

func validWebhook(u string) bool {
	p, err := url.Parse(u)
	return err == nil && (p.Scheme == "https" || p.Scheme == "http") && p.Host != ""
}

// watchEvents follows docker events for our containers and alerts on crashes and OOM kills.
func (a *App) watchEvents(ctx context.Context) {
	for ctx.Err() == nil {
		cmd := exec.CommandContext(ctx, "docker", "events",
			"--filter", "type=container",
			"--filter", "event=die", "--filter", "event=oom", "--filter", "event=health_status",
			"--format", "{{json .}}")
		out, err := cmd.StdoutPipe()
		if err == nil && cmd.Start() == nil {
			sc := bufio.NewScanner(out)
			for sc.Scan() {
				a.handleEvent(sc.Bytes())
			}
			cmd.Wait()
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second): // docker restarted or not ready yet
		}
	}
}

func (a *App) handleEvent(line []byte) {
	var ev struct {
		Action string `json:"Action"`
		Actor  struct {
			Attributes map[string]string `json:"Attributes"`
		} `json:"Actor"`
	}
	if json.Unmarshal(line, &ev) != nil {
		return
	}
	attrs := ev.Actor.Attributes
	name := attrs["name"]
	// A container that is still being health-checked is not serving traffic yet;
	// its failure is reported once, as "deploy failed".
	if dep := attrs["ossr.deployment"]; dep != "" {
		var status string
		a.db.QueryRow(`SELECT status FROM deployments WHERE id = ?`, dep).Scan(&status)
		if status != "live" {
			return
		}
	}
	sid, _ := strconv.ParseInt(attrs["ossr.service"], 10, 64)
	if sid == 0 {
		// Not ours: only adopted containers are watched.
		if a.db.QueryRow(`SELECT id FROM services WHERE source = ? AND container = ?`, externalSource, name).Scan(&sid) != nil {
			return
		}
	}
	label := name
	if s, err := a.service(sid); err == nil {
		label = s.Name
	}

	switch {
	case ev.Action == "oom":
		a.notify("oom", sid, fmt.Sprintf("%s ran out of memory and was killed", label))
	case ev.Action == "die":
		a.mu.Lock()
		at, intended := a.stopping[name]
		if intended {
			delete(a.stopping, name)
		}
		a.mu.Unlock()
		if intended && time.Since(at) < 5*time.Minute {
			return
		}
		code := attrs["exitCode"]
		a.mu.Lock()
		recent := crashes[sid][:0]
		for _, t := range crashes[sid] {
			if time.Since(t) < 10*time.Minute {
				recent = append(recent, t)
			}
		}
		recent = append(recent, time.Now())
		crashes[sid] = recent
		a.mu.Unlock()
		if len(recent) >= 3 {
			a.notify("crash_loop", sid, fmt.Sprintf("%s is crash-looping (%d crashes in 10 minutes, last exit code %s)", label, len(recent), code))
		} else {
			a.notify("crash", sid, fmt.Sprintf("%s crashed (exit code %s) and is restarting", label, code))
		}
	case strings.HasPrefix(ev.Action, "health_status") && strings.Contains(ev.Action, "unhealthy"):
		a.notify("unhealthy", sid, fmt.Sprintf("%s failed its health check", label))
	}
}

// scheduler runs daily backups (03:00), the daily report (09:00) and cleanup, in server time.
func (a *App) scheduler(ctx context.Context) {
	// First start: don't fire today's backup and report straight away on a fresh install.
	today := time.Now().Format("2006-01-02")
	for _, k := range []string{"last_backup_day", "last_report_day"} {
		if a.setting(k) == "" {
			a.setSetting(k, today)
		}
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		n := time.Now()
		today := n.Format("2006-01-02")
		if n.Hour() >= 3 && a.setting("last_backup_day") != today {
			a.setSetting("last_backup_day", today)
			a.dailyBackups(ctx)
		}
		if n.Hour() >= 9 && a.setting("last_report_day") != today && a.setting("daily_report") != "off" {
			a.setSetting("last_report_day", today)
			if a.setting("smtp_host") != "" {
				subject, body := a.report(24 * time.Hour)
				if err := a.sendEmail(subject, body); err != nil {
					a.log.Error("daily report", "err", err)
				}
			}
		}
		if n.Minute() == 0 {
			a.db.Exec(`DELETE FROM metrics WHERE ts < ?`, n.Add(-metricsKeep).Unix())
			a.db.Exec(`DELETE FROM alerts WHERE ts < ?`, n.Add(-90*24*time.Hour).Unix())
		}
	}
}

func (a *App) dailyBackups(ctx context.Context) {
	services, _ := a.services()
	for _, s := range services {
		if (s.Source == "postgres" || s.Source == "mysql") && containerRunning(ctx, dbContainer(s)) {
			bctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
			if _, err := a.backup(bctx, s); err != nil {
				a.log.Error("daily backup", "service", s.Name, "err", err)
			}
			cancel()
		}
	}
}

// report builds the plain-text daily summary.
func (a *App) report(window time.Duration) (string, string) {
	since := time.Now().Add(-window).Unix()
	var b strings.Builder
	var alertCount int
	a.db.QueryRow(`SELECT COUNT(*) FROM alerts WHERE ts >= ?`, since).Scan(&alertCount)

	var cpu float64
	var mem, memTot, disk, diskTot int64
	a.db.QueryRow(`SELECT COALESCE(AVG(cpu),0), CAST(COALESCE(AVG(mem),0) AS INTEGER), COALESCE(MAX(mem_limit),0),
		COALESCE(MAX(disk),0), COALESCE(MAX(disk_total),0) FROM metrics WHERE target = 'host' AND ts >= ?`, since).
		Scan(&cpu, &mem, &memTot, &disk, &diskTot)
	fmt.Fprintf(&b, "Server\n  CPU avg %.0f%%   Memory avg %s / %s   Disk %s / %s\n\n", cpu, human(mem), human(memTot), human(disk), human(diskTot))

	services, _ := a.services()
	b.WriteString("Services\n")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, s := range services {
		state := "stopped"
		if s.Container != "" && containerRunning(ctx, s.Container) {
			state = "running"
		}
		var deploys, alerts int
		a.db.QueryRow(`SELECT COUNT(*) FROM deployments WHERE service_id = ? AND created >= ?`, s.ID, since).Scan(&deploys)
		a.db.QueryRow(`SELECT COUNT(*) FROM alerts WHERE service_id = ? AND ts >= ?`, s.ID, since).Scan(&alerts)
		fmt.Fprintf(&b, "  %-20s %-8s deploys %d   alerts %d\n", s.Name, state, deploys, alerts)
	}

	rows, err := a.db.Query(`SELECT ts, message FROM alerts WHERE ts >= ? ORDER BY ts DESC LIMIT 20`, since)
	if err == nil {
		b.WriteString("\nAlerts\n")
		for rows.Next() {
			var ts int64
			var m string
			rows.Scan(&ts, &m)
			fmt.Fprintf(&b, "  %s  %s\n", time.Unix(ts, 0).Format("15:04"), m)
		}
		rows.Close()
	}
	subject := fmt.Sprintf("[OSS Railway] Daily report: %d services, %d alerts", len(services), alertCount)
	return subject, b.String()
}
