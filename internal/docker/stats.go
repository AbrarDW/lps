package docker

import (
	"context"
	"encoding/json"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// Usage is one resource-usage sample of a container (or of an app's processes
// inside a VPS). CPU is cumulative, so CPU % comes from two samples.
type Usage struct {
	At       time.Time
	CPUNanos uint64 // total CPU time used so far
	MemBytes uint64 // memory in use, without reclaimable page cache
	MemLimit uint64 // the container's limit (the Docker VM's memory when unlimited)
}

// HostInfo describes the machine Docker runs containers on (on Windows, the
// Docker Desktop VM).
type HostInfo struct {
	CPUs     int    `json:"cpus"`
	MemBytes uint64 `json:"memBytes"`
}

// Usage implements Engine. It takes a single sample without waiting.
func (c *Client) Usage(ctx context.Context, id string) (Usage, error) {
	res, err := c.cli.ContainerStats(ctx, id, client.ContainerStatsOptions{})
	if err != nil {
		return Usage{}, err
	}
	defer res.Body.Close()
	var st container.StatsResponse
	if err := json.NewDecoder(res.Body).Decode(&st); err != nil {
		return Usage{}, err
	}
	return usageFrom(st), nil
}

func usageFrom(st container.StatsResponse) Usage {
	u := Usage{At: st.Read, CPUNanos: st.CPUStats.CPUUsage.TotalUsage, MemLimit: st.MemoryStats.Limit}
	if u.At.IsZero() {
		u.At = time.Now()
	}
	// Match `docker stats`: leave out page cache the kernel can reclaim
	// (cgroup v2 "inactive_file", cgroup v1 "total_inactive_file").
	cache := st.MemoryStats.Stats["inactive_file"]
	if v, ok := st.MemoryStats.Stats["total_inactive_file"]; ok {
		cache = v
	}
	if st.MemoryStats.Usage > cache {
		u.MemBytes = st.MemoryStats.Usage - cache
	}
	return u
}

// HostInfo implements Engine.
func (c *Client) HostInfo(ctx context.Context) (HostInfo, error) {
	res, err := c.cli.Info(ctx, client.InfoOptions{})
	if err != nil {
		return HostInfo{}, err
	}
	return HostInfo{CPUs: res.Info.NCPU, MemBytes: uint64(res.Info.MemTotal)}, nil
}
