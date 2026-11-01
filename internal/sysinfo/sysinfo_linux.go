//go:build linux

package sysinfo

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

func readCPU() cpuTimes {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuTimes{}
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		var vals []uint64
		for _, v := range fields[1:] {
			n, _ := strconv.ParseUint(v, 10, 64)
			vals = append(vals, n)
		}
		var total uint64
		for _, v := range vals {
			total += v
		}
		idle := vals[3]
		if len(vals) > 4 {
			idle += vals[4] // iowait
		}
		return cpuTimes{busy: total - idle, total: total}
	}
	return cpuTimes{}
}

func read(p *cpuPair) Info {
	info := Info{Hostname: hostname(), CPUs: runtime.NumCPU(), CPUPercent: percent(p)}

	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			up, _ := strconv.ParseFloat(f[0], 64)
			info.UptimeSeconds = int64(up)
		}
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(b)); len(f) >= 3 {
			info.Load1, _ = strconv.ParseFloat(f[0], 64)
			info.Load5, _ = strconv.ParseFloat(f[1], 64)
			info.Load15, _ = strconv.ParseFloat(f[2], 64)
		}
	}
	if f, err := os.Open("/proc/meminfo"); err == nil {
		defer f.Close()
		var total, avail uint64
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 2 {
				continue
			}
			kb, _ := strconv.ParseUint(fields[1], 10, 64)
			switch fields[0] {
			case "MemTotal:":
				total = kb * 1024
			case "MemAvailable:":
				avail = kb * 1024
			}
		}
		info.MemTotal = total
		if total >= avail {
			info.MemUsed = total - avail
		}
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err == nil {
		info.DiskTotal = st.Blocks * uint64(st.Bsize)
		info.DiskUsed = (st.Blocks - st.Bfree) * uint64(st.Bsize)
	}
	if f, err := os.Open("/proc/net/dev"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			name, rest, ok := strings.Cut(sc.Text(), ":")
			if !ok || strings.TrimSpace(name) == "lo" {
				continue
			}
			fields := strings.Fields(rest)
			if len(fields) >= 9 {
				rx, _ := strconv.ParseUint(fields[0], 10, 64)
				tx, _ := strconv.ParseUint(fields[8], 10, 64)
				info.NetRx += rx
				info.NetTx += tx
			}
		}
	}
	return info
}
