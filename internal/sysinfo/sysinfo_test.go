package sysinfo

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func TestPercent(t *testing.T) {
	if percent(nil) != 0 {
		t.Fatal("no previous sample means 0")
	}
	p := &cpuPair{prev: cpuTimes{busy: 100, total: 1000}, cur: cpuTimes{busy: 150, total: 1100}}
	if got := percent(p); got != 50 {
		t.Fatalf("50 of 100 ticks busy = 50%%, got %v", got)
	}
	if percent(&cpuPair{prev: cpuTimes{total: 5}, cur: cpuTimes{total: 5}}) != 0 {
		t.Fatal("no elapsed ticks must not divide by zero")
	}
	if percent(&cpuPair{prev: cpuTimes{busy: 0, total: 0}, cur: cpuTimes{busy: 500, total: 100}}) != 100 {
		t.Fatal("the figure is clamped to 100")
	}
}

func TestSnapshotHasBasics(t *testing.T) {
	s := NewSampler()
	info := s.Get()
	if info.Hostname == "" || info.CPUs < 1 {
		t.Fatalf("hostname and CPU count are always known: %+v", info)
	}
	if runtime.GOOS == "linux" && (info.MemTotal == 0 || info.DiskTotal == 0 || info.UptimeSeconds == 0) {
		t.Fatalf("on Linux memory, disk and uptime must be read: %+v", info)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	s.Run(ctx, 30*time.Millisecond) // must return when the context ends, without panicking
}
