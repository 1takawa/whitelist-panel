// Package sysinfo reports basic host metrics for the dashboard. On Linux it reads /proc; elsewhere only the
// hostname is known (enough for local development).
package sysinfo

import (
	"context"
	"os"
	"sync"
	"time"
)

// Info is a snapshot of the host.
type Info struct {
	Hostname      string  `json:"hostname"`
	UptimeSeconds int64   `json:"uptimeSeconds"`
	CPUPercent    float64 `json:"cpuPercent"` // averaged over the last sampling interval
	Load1         float64 `json:"load1"`
	Load5         float64 `json:"load5"`
	Load15        float64 `json:"load15"`
	MemTotal      uint64  `json:"memTotal"`
	MemUsed       uint64  `json:"memUsed"`
	DiskTotal     uint64  `json:"diskTotal"`
	DiskUsed      uint64  `json:"diskUsed"`
	NetRx         uint64  `json:"netRx"` // cumulative bytes since boot, all interfaces except loopback
	NetTx         uint64  `json:"netTx"`
	CPUs          int     `json:"cpus"`
}

// Sampler refreshes the snapshot in the background so that the CPU figure is a real interval average.
type Sampler struct {
	mu   sync.RWMutex
	info Info
}

func NewSampler() *Sampler {
	s := &Sampler{}
	s.info = read(nil)
	return s
}

// Get returns the latest snapshot.
func (s *Sampler) Get() Info {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.info
}

// Run samples every interval until ctx is cancelled.
func (s *Sampler) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	prev := readCPU()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cur := readCPU()
			info := read(&cpuPair{prev: prev, cur: cur})
			prev = cur
			s.mu.Lock()
			s.info = info
			s.mu.Unlock()
		}
	}
}

type cpuTimes struct{ busy, total uint64 }

type cpuPair struct{ prev, cur cpuTimes }

func percent(p *cpuPair) float64 {
	if p == nil || p.cur.total <= p.prev.total {
		return 0
	}
	busy := float64(p.cur.busy-p.prev.busy) / float64(p.cur.total-p.prev.total) * 100
	switch {
	case busy < 0:
		return 0
	case busy > 100:
		return 100
	}
	return busy
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}
