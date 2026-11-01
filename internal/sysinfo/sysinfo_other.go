//go:build !linux

package sysinfo

import "runtime"

func readCPU() cpuTimes { return cpuTimes{} }

func read(*cpuPair) Info { return Info{Hostname: hostname(), CPUs: runtime.NumCPU()} }
