package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const metricsEvery = 15 * time.Second
const metricsKeep = 7 * 24 * time.Hour

type sample struct {
	cpu             float64
	mem, memLimit   int64
	netRx, netTx    int64 // bytes/second
	disk, diskTotal int64
}

type collector struct {
	procDir             string
	prevIdle, prevTotal uint64
	prevNet             map[string][2]int64 // target -> cumulative rx, tx
	prevAt              time.Time
	highMem             int // consecutive samples above 90%
}

func (a *App) collectMetrics(ctx context.Context) {
	c := &collector{prevNet: map[string][2]int64{}}
	for _, dir := range []string{a.cfg.HostProc, "/proc"} {
		if _, err := os.Stat(filepath.Join(dir, "stat")); err == nil {
			c.procDir = dir
			break
		}
	}
	t := time.NewTicker(metricsEvery)
	defer t.Stop()
	for {
		a.collectOnce(ctx, c)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *App) collectOnce(ctx context.Context, c *collector) {
	ts := now()
	elapsed := time.Since(c.prevAt).Seconds()
	first := c.prevAt.IsZero()
	c.prevAt = time.Now()

	if c.procDir != "" {
		s := sample{}
		s.cpu = c.hostCPU()
		s.mem, s.memLimit = hostMem(c.procDir)
		rx, tx := hostNet(c.procDir)
		s.netRx, s.netTx = c.rate("host", rx, tx, elapsed, first)
		s.diskTotal, s.disk = diskUsage(a.cfg.HostRoot)
		a.saveSample(ts, "host", s)
		a.checkHost(c, s)
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := docker(ctx, "ps", "--filter", "label=ossr.service", "--format", `{{.Names}}	{{.Label "ossr.service"}}`)
	if err != nil || out == "" {
		return
	}
	owner := map[string]string{}
	args := []string{"stats", "--no-stream", "--format", "{{json .}}"}
	for _, line := range strings.Split(out, "\n") {
		name, id, ok := strings.Cut(line, "\t")
		if ok {
			owner[name] = id
			args = append(args, name)
		}
	}
	stats, err := docker(ctx, args...)
	if err != nil {
		return
	}
	for _, line := range strings.Split(stats, "\n") {
		var st struct{ Name, CPUPerc, MemUsage, NetIO string }
		if json.Unmarshal([]byte(line), &st) != nil {
			continue
		}
		id, ok := owner[st.Name]
		if !ok {
			continue
		}
		s := parseDockerStats(st.CPUPerc, st.MemUsage)
		rx, tx := parsePair(st.NetIO)
		s.netRx, s.netTx = c.rate(st.Name, rx, tx, elapsed, first)
		a.saveSample(ts, id, s)
	}
}

func (a *App) saveSample(ts int64, target string, s sample) {
	a.db.Exec(`INSERT INTO metrics(ts, target, cpu, mem, mem_limit, net_rx, net_tx, disk, disk_total) VALUES(?,?,?,?,?,?,?,?,?)`,
		ts, target, s.cpu, s.mem, s.memLimit, s.netRx, s.netTx, s.disk, s.diskTotal)
}

func (c *collector) rate(key string, rx, tx int64, elapsed float64, first bool) (int64, int64) {
	prev, ok := c.prevNet[key]
	c.prevNet[key] = [2]int64{rx, tx}
	if first || !ok || elapsed <= 0 || rx < prev[0] || tx < prev[1] {
		return 0, 0
	}
	return int64(float64(rx-prev[0]) / elapsed), int64(float64(tx-prev[1]) / elapsed)
}

func (c *collector) hostCPU() float64 {
	f, err := os.Open(filepath.Join(c.procDir, "stat"))
	if err != nil {
		return 0
	}
	defer f.Close()
	line, _ := bufio.NewReader(f).ReadString('\n')
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0
	}
	var total, idle uint64
	for i, v := range fields[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		total += n
		if i == 3 || i == 4 { // idle + iowait
			idle += n
		}
	}
	dt, di := total-c.prevTotal, idle-c.prevIdle
	first := c.prevTotal == 0
	c.prevTotal, c.prevIdle = total, idle
	if first || dt == 0 {
		return 0
	}
	return 100 * float64(dt-di) / float64(dt)
}

func hostMem(procDir string) (used, total int64) {
	f, err := os.Open(filepath.Join(procDir, "meminfo"))
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	var avail int64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		n, _ := strconv.ParseInt(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			total = n * 1024
		case "MemAvailable:":
			avail = n * 1024
		}
	}
	return total - avail, total
}

// hostNet reads the host's network namespace through pid 1.
func hostNet(procDir string) (rx, tx int64) {
	path := filepath.Join(procDir, "1", "net", "dev")
	if _, err := os.Stat(path); err != nil {
		path = filepath.Join(procDir, "net", "dev")
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		iface, rest, ok := strings.Cut(sc.Text(), ":")
		iface = strings.TrimSpace(iface)
		if !ok || iface == "lo" || strings.HasPrefix(iface, "veth") || strings.HasPrefix(iface, "br-") || iface == "docker0" {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		r, _ := strconv.ParseInt(f[0], 10, 64)
		t, _ := strconv.ParseInt(f[8], 10, 64)
		rx += r
		tx += t
	}
	return rx, tx
}

// parseDockerStats turns "12.5%" and "45.2MiB / 1.94GiB" into numbers.
func parseDockerStats(cpu, mem string) sample {
	s := sample{}
	s.cpu, _ = strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(cpu), "%"), 64)
	s.mem, s.memLimit = parsePair(mem)
	return s
}

func parsePair(v string) (int64, int64) {
	a, b, _ := strings.Cut(v, "/")
	return parseSize(a), parseSize(b)
}

func parseSize(v string) int64 {
	v = strings.TrimSpace(v)
	units := []struct {
		suffix string
		mult   float64
	}{
		{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}, {"TiB", 1 << 40},
		{"kB", 1e3}, {"KB", 1e3}, {"MB", 1e6}, {"GB", 1e9}, {"TB", 1e12}, {"B", 1},
	}
	for _, u := range units {
		if num, ok := strings.CutSuffix(v, u.suffix); ok {
			f, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
			if err != nil {
				return 0
			}
			return int64(f * u.mult)
		}
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}

func (a *App) checkHost(c *collector, s sample) {
	if s.memLimit > 0 && float64(s.mem)/float64(s.memLimit) > 0.9 {
		c.highMem++
		if c.highMem == 20 { // ~5 minutes
			a.notify("high_memory", 0, fmt.Sprintf("Server memory above 90%% for 5 minutes (%s of %s)", human(s.mem), human(s.memLimit)))
		}
	} else {
		c.highMem = 0
	}
	if s.diskTotal > 0 && float64(s.disk)/float64(s.diskTotal) > 0.85 {
		a.notify("disk_full", 0, fmt.Sprintf("Disk is %.0f%% full (%s of %s)", 100*float64(s.disk)/float64(s.diskTotal), human(s.disk), human(s.diskTotal)))
	}
}

func human(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

type point struct {
	T        int64   `json:"t"`
	CPU      float64 `json:"cpu"`
	Mem      int64   `json:"mem"`
	MemLimit int64   `json:"mem_limit"`
	NetRx    int64   `json:"net_rx"`
	NetTx    int64   `json:"net_tx"`
	Disk     int64   `json:"disk"`
	DiskTot  int64   `json:"disk_total"`
}

// series returns about 120 averaged points for the range.
func (a *App) series(target, rng string) ([]point, error) {
	d := map[string]time.Duration{"1h": time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour}[rng]
	if d == 0 {
		d = time.Hour
	}
	bucket := int64(d.Seconds() / 120)
	if bucket < 15 {
		bucket = 15
	}
	rows, err := a.db.Query(`SELECT (ts / ?) * ? AS b, AVG(cpu), CAST(AVG(mem) AS INTEGER), MAX(mem_limit),
		CAST(AVG(net_rx) AS INTEGER), CAST(AVG(net_tx) AS INTEGER), CAST(AVG(disk) AS INTEGER), MAX(disk_total)
		FROM metrics WHERE target = ? AND ts >= ? GROUP BY b ORDER BY b`,
		bucket, bucket, target, time.Now().Add(-d).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []point{}
	for rows.Next() {
		var p point
		if err := rows.Scan(&p.T, &p.CPU, &p.Mem, &p.MemLimit, &p.NetRx, &p.NetTx, &p.Disk, &p.DiskTot); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
