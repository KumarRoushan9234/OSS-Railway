package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The Server view: everything running on the machine, not only what OSS Railway deployed.
// Listening ports come from the host's /proc (pid 1's network namespace), containers from Docker.

type listenPort struct {
	Port      int    `json:"port"`
	Address   string `json:"address"`
	Process   string `json:"process"`
	PID       int    `json:"pid"`
	Container string `json:"container"`
	Public    bool   `json:"public"` // listening on all interfaces, not just localhost
}

type hostContainer struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Image     string  `json:"image"`
	State     string  `json:"state"`
	Status    string  `json:"status"`
	Ports     string  `json:"ports"`
	ServiceID int64   `json:"service_id"` // 0 when not deployed by OSS Railway
	Project   string  `json:"project"`    // Docker Compose project, if any
	CPU       float64 `json:"cpu"`        // percent of one core
	Mem       int64   `json:"mem"`
	MemLimit  int64   `json:"mem_limit"`
	Size      string  `json:"size"`
}

type hostVolume struct {
	Name  string `json:"name"`
	Size  string `json:"size"`
	Links string `json:"links"`
}

// GET /api/server
func (a *App) handleServer(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	// Docker calls and the /proc walk each take a second or more; run them together.
	var containers []hostContainer
	var volumes []hostVolume
	var sizes map[string]string
	var ports []listenPort
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); containers = a.hostContainers(ctx) }()
	go func() { defer wg.Done(); volumes, sizes = cachedDiskUsage(ctx) }()
	go func() { defer wg.Done(); ports = listeningPorts(a.cfg.HostProc) }()
	wg.Wait()
	a.dropStaleOwners(containers)
	if services, err := a.services(); err == nil {
		for _, sv := range services {
			for i := range containers {
				if sv.IsExternal() && containers[i].Name == sv.Container {
					containers[i].ServiceID = sv.ID
				}
			}
		}
	}
	for i := range containers {
		containers[i].Size = sizes[containers[i].Name]
	}
	byPort := publishedPorts(containers)
	for i := range ports {
		ports[i].Container = byPort[ports[i].Port]
	}
	var memUsed, memTotal int64
	if a.cfg.HostProc != "" {
		memUsed, memTotal = hostMem(a.cfg.HostProc)
	}
	diskTotal, diskUsed := diskUsage(a.cfg.HostRoot)
	cpus := hostCPUs(a.cfg.HostProc)
	writeJSON(w, map[string]any{
		"allocation": a.allocation(containers, cpus, memTotal),
		"ports":      ports,
		"containers": containers,
		"volumes":    volumes,
		"host": map[string]int64{
			"cpus": int64(cpus), "mem": memUsed, "mem_total": memTotal,
			"disk": diskUsed, "disk_total": diskTotal,
		},
	})
}

func (a *App) hostContainers(ctx context.Context) []hostContainer {
	out := []hostContainer{}
	var stats string
	var statsErr error
	done := make(chan struct{})
	go func() {
		stats, statsErr = docker(ctx, "stats", "--no-stream", "--format", "{{json .}}")
		close(done)
	}()
	list, err := docker(ctx, "ps", "-a", "--format", "{{json .}}")
	<-done
	if err != nil {
		return out
	}
	for _, line := range strings.Split(list, "\n") {
		var c struct{ ID, Names, Image, State, Status, Ports, Labels string }
		if json.Unmarshal([]byte(line), &c) != nil {
			continue
		}
		hc := hostContainer{ID: c.ID, Name: c.Names, Image: c.Image, State: c.State, Status: c.Status, Ports: c.Ports}
		hc.Project = parseLabels(c.Labels)["com.docker.compose.project"]
		for _, l := range strings.Split(c.Labels, ",") {
			if v, ok := strings.CutPrefix(l, "ossr.service="); ok {
				hc.ServiceID, _ = strconv.ParseInt(v, 10, 64)
			}
		}
		out = append(out, hc)
	}
	if statsErr == nil {
		live := map[string]sample{}
		for _, line := range strings.Split(stats, "\n") {
			var st struct{ Name, CPUPerc, MemUsage string }
			if json.Unmarshal([]byte(line), &st) == nil {
				live[st.Name] = parseDockerStats(st.CPUPerc, st.MemUsage)
			}
		}
		for i, c := range out {
			if s, ok := live[c.Name]; ok {
				out[i].CPU, out[i].Mem, out[i].MemLimit = s.cpu, s.mem, s.memLimit
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].State == "running") != (out[j].State == "running") {
			return out[i].State == "running"
		}
		return out[i].Name < out[j].Name
	})
	return out
}

type allocShare struct {
	ID      int64   `json:"id"`
	Name    string  `json:"name"`
	CPU     float64 `json:"cpu"`      // limit in cores; 0 = no limit
	Memory  int     `json:"memory"`   // limit in MB; 0 = no limit
	CPUUsed float64 `json:"cpu_used"` // cores, now
	MemUsed int64   `json:"mem_used"` // bytes, now
	Disk    string  `json:"disk"`
}

// allocation shows how the server's CPU and memory are divided between services:
// what each is guaranteed to stay under (its limit), what it uses now, and what is left.
func (a *App) allocation(cs []hostContainer, cpus int, memTotal int64) map[string]any {
	live := map[int64]hostContainer{}
	for _, c := range cs {
		if c.ServiceID != 0 && c.State == "running" {
			live[c.ServiceID] = c
		}
	}
	services, _ := a.services()
	shares := []allocShare{}
	var cpuSum float64
	var memSum int64
	unlimited := 0
	for _, s := range services {
		c := live[s.ID]
		shares = append(shares, allocShare{ID: s.ID, Name: s.Name, CPU: s.CPU, Memory: s.Memory, CPUUsed: c.CPU / 100, MemUsed: c.Mem, Disk: c.Size})
		cpuSum += s.CPU
		memSum += int64(s.Memory) << 20
		if s.CPU == 0 || s.Memory == 0 {
			unlimited++
		}
	}
	var warnings []string
	if cpus > 0 && cpuSum > float64(cpus) {
		warnings = append(warnings, "CPU limits add up to more cores than the server has; services will compete under load.")
	}
	if memTotal > 0 && memSum > memTotal*9/10 {
		warnings = append(warnings, "Memory limits add up to more than 90% of the server's memory; the server itself may run out.")
	}
	return map[string]any{
		"services": shares, "cpu_limited": cpuSum, "memory_limited": memSum, "unlimited": unlimited,
		"cpus": cpus, "memory_total": memTotal, "warnings": append([]string{}, warnings...),
	}
}

// dropStaleOwners keeps a container's service link only if the name still matches that
// service: leftovers from a deleted service or an older panel database count as external.
func (a *App) dropStaleOwners(cs []hostContainer) {
	services, _ := a.services()
	byID := map[int64]*Service{}
	for _, s := range services {
		byID[s.ID] = s
	}
	for i, c := range cs {
		s := byID[c.ServiceID]
		if s == nil || (!s.IsExternal() && c.Name != s.Container && c.Name != dbContainer(s) && !strings.HasPrefix(c.Name, "ossr-"+s.Name+"-")) {
			cs[i].ServiceID = 0
		}
	}
}

var diskCache struct {
	sync.Mutex
	at      time.Time
	volumes []hostVolume
	sizes   map[string]string
}

// cachedDiskUsage reuses the last disk scan for a minute: sizing every volume is slow.
func cachedDiskUsage(ctx context.Context) ([]hostVolume, map[string]string) {
	diskCache.Lock()
	defer diskCache.Unlock()
	if time.Since(diskCache.at) > time.Minute || diskCache.sizes == nil {
		diskCache.volumes, diskCache.sizes = dockerDiskUsage(ctx)
		diskCache.at = time.Now()
	}
	return diskCache.volumes, diskCache.sizes
}

// dockerDiskUsage returns volume sizes and each container's writable-layer size.
func dockerDiskUsage(ctx context.Context) ([]hostVolume, map[string]string) {
	vols, sizes := []hostVolume{}, map[string]string{}
	out, err := docker(ctx, "system", "df", "-v", "--format", "{{json .}}")
	if err != nil {
		return vols, sizes
	}
	var df struct {
		Containers []struct{ Names, Size string }
		Volumes    []struct{ Name, Size, Links string }
	}
	if json.Unmarshal([]byte(out), &df) != nil {
		return vols, sizes
	}
	for _, c := range df.Containers {
		sizes[c.Names] = c.Size
	}
	for _, v := range df.Volumes {
		vols = append(vols, hostVolume{Name: v.Name, Size: v.Size, Links: v.Links})
	}
	return vols, sizes
}

var publishedRe = regexp.MustCompile(`(?:[0-9.]+|\[[0-9a-f:]*\]|::):(\d+)->\d+/tcp`)

func publishedPorts(cs []hostContainer) map[int]string {
	m := map[int]string{}
	for _, c := range cs {
		for _, g := range publishedRe.FindAllStringSubmatch(c.Ports, -1) {
			if p, err := strconv.Atoi(g[1]); err == nil {
				m[p] = c.Name
			}
		}
	}
	return m
}

// listeningPorts reads TCP sockets in LISTEN state from the host's network namespace and,
// where /proc allows it, the process that owns each one.
func listeningPorts(procDir string) []listenPort {
	out := []listenPort{}
	if procDir == "" {
		return out
	}
	owners := socketOwners(procDir)
	seen := map[string]bool{}
	for _, file := range []string{"1/net/tcp", "1/net/tcp6"} {
		f, err := os.Open(filepath.Join(procDir, file))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Scan() // header
		for sc.Scan() {
			p, inode, ok := parseProcNetLine(sc.Text())
			if !ok {
				continue
			}
			key := p.Address + ":" + strconv.Itoa(p.Port)
			if seen[key] {
				continue
			}
			seen[key] = true
			if o, ok := owners[inode]; ok {
				p.PID, p.Process = o.pid, o.name
			}
			out = append(out, p)
		}
		f.Close()
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Address < out[j].Address
	})
	return out
}

// parseProcNetLine parses one /proc/net/tcp{,6} row; ok only for LISTEN sockets (state 0A).
func parseProcNetLine(line string) (listenPort, string, bool) {
	f := strings.Fields(line)
	if len(f) < 10 || f[3] != "0A" {
		return listenPort{}, "", false
	}
	hostHex, portHex, ok := strings.Cut(f[1], ":")
	if !ok {
		return listenPort{}, "", false
	}
	port, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return listenPort{}, "", false
	}
	raw, err := hex.DecodeString(hostHex)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return listenPort{}, "", false
	}
	// The kernel prints each 32-bit word in host (little-endian) byte order.
	for i := 0; i < len(raw); i += 4 {
		raw[i], raw[i+1], raw[i+2], raw[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
	}
	ip := net.IP(raw)
	return listenPort{Port: int(port), Address: ip.String(), Public: ip.IsUnspecified()}, f[9], true
}

type sockOwner struct {
	pid  int
	name string
}

// socketOwners maps socket inode -> process. Processes we may not inspect are skipped.
func socketOwners(procDir string) map[string]sockOwner {
	m := map[string]sockOwner{}
	pids, _ := filepath.Glob(filepath.Join(procDir, "[0-9]*"))
	for _, dir := range pids {
		pid, err := strconv.Atoi(filepath.Base(dir))
		if err != nil {
			continue
		}
		fds, err := os.ReadDir(filepath.Join(dir, "fd"))
		if err != nil {
			continue
		}
		var name string
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(dir, "fd", fd.Name()))
			if err != nil || !strings.HasPrefix(link, "socket:[") {
				continue
			}
			if name == "" {
				b, _ := os.ReadFile(filepath.Join(dir, "comm"))
				name = strings.TrimSpace(string(b))
			}
			m[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")] = sockOwner{pid, name}
		}
	}
	return m
}

func hostCPUs(procDir string) int {
	b, err := os.ReadFile(filepath.Join(procDir, "stat"))
	if err != nil {
		return 0
	}
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if len(l) > 3 && strings.HasPrefix(l, "cpu") && l[3] >= '0' && l[3] <= '9' {
			n++
		}
	}
	return n
}

// ---- resource suggestions ----

type usage struct {
	Samples int     `json:"samples"`
	CPUAvg  float64 `json:"cpu_avg"`  // percent of one core
	CPUPeak float64 `json:"cpu_peak"` // 95th percentile
	MemAvg  int64   `json:"mem_avg"`
	MemPeak int64   `json:"mem_peak"`
}

type suggestion struct {
	CPU      float64  `json:"cpu"`    // cores
	Memory   int      `json:"memory"` // MB
	Warnings []string `json:"warnings"`
}

const minSamples = 20 // ~5 minutes of data at the 15 s collection interval

// suggestLimits sizes limits with headroom over what the service really used:
// memory 1.5× the peak (rounded up to 64 MB, at least 128 MB), CPU 1.5× the
// 95th-percentile load (rounded up to a quarter core, at least 0.25).
func suggestLimits(u usage, cpuLimit float64, memLimitMB int) suggestion {
	s := suggestion{Warnings: []string{}}
	if u.Samples < minSamples {
		return s
	}
	peakMB := float64(u.MemPeak) / (1 << 20)
	s.Memory = int(math.Max(128, math.Ceil(peakMB*1.5/64)*64))
	s.CPU = math.Max(0.25, math.Ceil(u.CPUPeak/100*1.5*4)/4)
	if memLimitMB > 0 && peakMB > 0.9*float64(memLimitMB) {
		s.Warnings = append(s.Warnings, "Memory peaked above 90% of its limit; it may be killed for running out of memory.")
	}
	if cpuLimit > 0 && u.CPUPeak/100 > 0.9*cpuLimit {
		s.Warnings = append(s.Warnings, "CPU is often at its limit; requests may be slow.")
	}
	return s
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[int(math.Min(float64(len(sorted)-1), math.Ceil(p*float64(len(sorted)))-1))]
}

// GET /api/services/{id}/resources: usage over the last 24 h, current limits, a suggestion.
func (a *App) handleResources(w http.ResponseWriter, r *http.Request) {
	s, ok := a.serviceFromPath(w, r)
	if !ok {
		return
	}
	rows, err := a.db.Query(`SELECT cpu, mem FROM metrics WHERE target = ? AND ts >= ?`, strconv.FormatInt(s.ID, 10), time.Now().Add(-24*time.Hour).Unix())
	if err != nil {
		serverError(w, a, err)
		return
	}
	var u usage
	var cpus []float64
	var cpuSum, memSum float64
	for rows.Next() {
		var c float64
		var m int64
		if rows.Scan(&c, &m) == nil {
			u.Samples++
			cpus = append(cpus, c)
			cpuSum += c
			memSum += float64(m)
			u.MemPeak = max(u.MemPeak, m)
		}
	}
	rows.Close()
	if u.Samples > 0 {
		sort.Float64s(cpus)
		u.CPUAvg, u.MemAvg = cpuSum/float64(u.Samples), int64(memSum/float64(u.Samples))
		u.CPUPeak = percentile(cpus, 0.95)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	_, sizes := cachedDiskUsage(ctx)
	writeJSON(w, map[string]any{
		"usage":      u,
		"disk":       sizes[s.Container],
		"suggestion": suggestLimits(u, s.CPU, s.Memory),
	})
}
