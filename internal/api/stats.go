package api

// Live CPU and memory usage of VPSes and apps.
//
// A background sampler records one sample per running VPS and app: every
// statsFast while someone has a dashboard open, else every statsSlow. It keeps
// statsWindow of history per item for the sparklines.

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abrardw/lps/internal/docker"
	"github.com/abrardw/lps/internal/store"
)

var (
	statsFast   = 3 * time.Second
	statsSlow   = 15 * time.Second
	statsActive = 2 * time.Minute // dashboard counts as open this long after a /api/stats call
	statsWindow = 5 * time.Minute
)

// statPoint is one point of a sparkline.
type statPoint struct {
	T   int64   `json:"t"`   // unix milliseconds
	CPU float64 `json:"cpu"` // percent of one CPU core (200 = two cores busy)
	Mem uint64  `json:"mem"` // bytes
}

type statSeries struct {
	last     docker.Usage
	points   []statPoint
	memLimit uint64
	cpuLimit float64 // cores available to it
}

// statView is what the API returns for one VPS or app.
type statView struct {
	CPU      float64     `json:"cpu"`      // percent of one core, latest
	CPULimit float64     `json:"cpuLimit"` // cores it may use
	Mem      uint64      `json:"mem"`      // bytes, latest
	MemLimit uint64      `json:"memLimit"` // bytes it may use
	Points   []statPoint `json:"points"`
}

type statsState struct {
	mu       sync.Mutex
	series   map[string]*statSeries // "vps:1", "app:3"
	host     docker.HostInfo
	lastView time.Time
	kick     chan struct{}
}

func newStatsState() *statsState {
	return &statsState{series: map[string]*statSeries{}, kick: make(chan struct{}, 1)}
}

func (s *Server) statsLoop() {
	defer s.wg.Done()
	if h, err := s.engine.HostInfo(s.ctx); err == nil {
		s.stats.mu.Lock()
		s.stats.host = h
		s.stats.mu.Unlock()
	}
	for {
		s.sampleAll(s.ctx)
		s.stats.mu.Lock()
		wait := statsSlow
		if time.Since(s.stats.lastView) < statsActive {
			wait = statsFast
		}
		s.stats.mu.Unlock()
		select {
		case <-s.ctx.Done():
			return
		case <-s.stats.kick:
		case <-time.After(wait):
		}
	}
}

// target is one thing to sample.
type statTarget struct {
	key      string
	cpuLimit float64
	memLimit uint64 // 0 = take it from the sample
	sample   func(ctx context.Context) (docker.Usage, error)
}

// sampleAll takes one sample of every running VPS and app.
func (s *Server) sampleAll(ctx context.Context) {
	targets := s.statTargets(ctx)
	seen := map[string]bool{}
	for _, t := range targets {
		sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		u, err := t.sample(sctx)
		cancel()
		if err != nil {
			continue
		}
		seen[t.key] = true
		s.addSample(t, u)
	}
	// Forget what stopped or was deleted: its history no longer applies.
	s.stats.mu.Lock()
	for k := range s.stats.series {
		if !seen[k] {
			delete(s.stats.series, k)
		}
	}
	s.stats.mu.Unlock()
}

func (s *Server) statTargets(ctx context.Context) []statTarget {
	var out []statTarget
	s.stats.mu.Lock()
	hostCPUs := float64(s.stats.host.CPUs)
	s.stats.mu.Unlock()
	limitCPUs := func(c float64) float64 {
		if c > 0 {
			return c
		}
		return hostCPUs
	}
	running := map[int64]*store.VPS{}
	vpses, _ := s.store.ListVPS(ctx)
	for _, v := range vpses {
		if st, err := s.engine.State(ctx, v.ContainerID); err != nil || st != docker.StateRunning {
			continue
		}
		running[v.ID] = v
		id := v.ContainerID
		out = append(out, statTarget{key: "vps:" + strconv.FormatInt(v.ID, 10), cpuLimit: limitCPUs(v.CPUs),
			sample: func(ctx context.Context) (docker.Usage, error) { return s.engine.Usage(ctx, id) }})
	}
	apps, _ := s.store.ListApps(ctx)
	for _, a := range apps {
		key := "app:" + strconv.FormatInt(a.ID, 10)
		if !a.InVPS() {
			if a.ContainerID == "" {
				continue
			}
			if st, err := s.engine.State(ctx, a.ContainerID); err != nil || st != docker.StateRunning {
				continue
			}
			id := a.ContainerID
			out = append(out, statTarget{key: key, cpuLimit: limitCPUs(a.CPUs),
				sample: func(ctx context.Context) (docker.Usage, error) { return s.engine.Usage(ctx, id) }})
			continue
		}
		v := running[a.VPSID]
		if v == nil || a.ContainerID == "" {
			continue
		}
		var memLimit uint64
		if v.MemoryMB > 0 {
			memLimit = uint64(v.MemoryMB) << 20
		} else {
			s.stats.mu.Lock()
			memLimit = s.stats.host.MemBytes
			s.stats.mu.Unlock()
		}
		app := *a
		out = append(out, statTarget{key: key, cpuLimit: limitCPUs(v.CPUs), memLimit: memLimit,
			sample: func(ctx context.Context) (docker.Usage, error) { return s.vpsAppUsage(ctx, v, &app) }})
	}
	return out
}

// vpsAppUsage samples an app's process group inside its VPS via lps-app.
func (s *Server) vpsAppUsage(ctx context.Context, v *store.VPS, a *store.App) (docker.Usage, error) {
	var out bytes.Buffer
	cmd := []string{docker.AppHelperPath, "usage", a.Name}
	if err := s.vpsExec(ctx, v, v.SSHUser, "/", cmd, userEnv(v), &out); err != nil {
		return docker.Usage{}, err
	}
	var ticks, mem, hz, unit uint64
	if _, err := fmt.Sscan(strings.TrimSpace(out.String()), &ticks, &mem, &hz, &unit); err != nil || hz == 0 {
		return docker.Usage{}, fmt.Errorf("lps-app usage: unexpected output %q", out.String())
	}
	return docker.Usage{At: time.Now(), CPUNanos: ticks * uint64(time.Second) / hz, MemBytes: mem * unit}, nil
}

func (s *Server) addSample(t statTarget, u docker.Usage) {
	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()
	sr := s.stats.series[t.key]
	if sr == nil {
		sr = &statSeries{}
		s.stats.series[t.key] = sr
	}
	sr.cpuLimit = t.cpuLimit
	sr.memLimit = u.MemLimit
	if t.memLimit > 0 {
		sr.memLimit = t.memLimit
	}
	prev := sr.last
	sr.last = u
	// CPU % needs two samples; a counter that went backwards means a restart.
	if prev.At.IsZero() || !u.At.After(prev.At) || u.CPUNanos < prev.CPUNanos {
		return
	}
	cpu := float64(u.CPUNanos-prev.CPUNanos) / float64(u.At.Sub(prev.At).Nanoseconds()) * 100
	sr.points = append(sr.points, statPoint{T: u.At.UnixMilli(), CPU: cpu, Mem: u.MemBytes})
	cutoff := u.At.Add(-statsWindow).UnixMilli()
	i := 0
	for i < len(sr.points) && sr.points[i].T < cutoff {
		i++
	}
	sr.points = sr.points[i:]
}

// handleStats returns live usage: {host, intervalMs, vps: {id: …}, apps: {id: …}}.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	s.stats.mu.Lock()
	wasIdle := time.Since(s.stats.lastView) >= statsActive
	s.stats.lastView = time.Now()
	resp := struct {
		Host       docker.HostInfo     `json:"host"`
		IntervalMS int64               `json:"intervalMs"`
		VPS        map[string]statView `json:"vps"`
		Apps       map[string]statView `json:"apps"`
	}{Host: s.stats.host, IntervalMS: statsFast.Milliseconds(), VPS: map[string]statView{}, Apps: map[string]statView{}}
	for k, sr := range s.stats.series {
		if len(sr.points) == 0 {
			continue
		}
		last := sr.points[len(sr.points)-1]
		view := statView{CPU: last.CPU, CPULimit: sr.cpuLimit, Mem: last.Mem, MemLimit: sr.memLimit,
			Points: append([]statPoint(nil), sr.points...)}
		kind, id, _ := strings.Cut(k, ":")
		if kind == "vps" {
			resp.VPS[id] = view
		} else {
			resp.Apps[id] = view
		}
	}
	s.stats.mu.Unlock()
	if wasIdle {
		// Someone just opened the dashboard: switch to fast sampling now.
		select {
		case s.stats.kick <- struct{}{}:
		default:
		}
	}
	writeJSON(w, 200, resp)
}
