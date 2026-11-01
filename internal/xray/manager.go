package xray

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Status is a snapshot of the Xray process.
type Status struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	PID       int    `json:"pid"`
	Version   string `json:"version"`
	Uptime    int64  `json:"uptimeSeconds"`
	LastError string `json:"lastError"`
}

// Manager runs and supervises one Xray process driven by a config file.
type Manager struct {
	Bin        string
	ConfigPath string
	APIAddr    string // host:port of the local API inbound, used for statistics
	Log        *slog.Logger

	mu        sync.Mutex
	cmd       *exec.Cmd
	stopping  bool
	startedAt time.Time
	lastErr   string
	version   string
	logs      *ring
}

func NewManager(bin, configPath, apiAddr string, log *slog.Logger) *Manager {
	return &Manager{Bin: bin, ConfigPath: configPath, APIAddr: apiAddr, Log: log, logs: newRing(500)}
}

// Installed reports whether the Xray binary exists.
func (m *Manager) Installed() bool {
	st, err := os.Stat(m.Bin)
	return err == nil && !st.IsDir()
}

// Check validates a config with `xray run -test` without touching the running process.
func (m *Manager) Check(ctx context.Context, cfg []byte) error {
	if !m.Installed() {
		return ErrNotInstalled
	}
	dir := filepath.Dir(m.ConfigPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "check-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(cfg); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return m.test(ctx, tmp.Name())
}

func (m *Manager) test(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, m.Bin, "run", "-test", "-config", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("xray rejected the configuration: %s", tail(string(out), 1500))
	}
	return nil
}

// Apply validates and installs the config atomically, then (re)starts Xray.
func (m *Manager) Apply(ctx context.Context, cfg []byte) error {
	if err := m.Check(ctx, cfg); err != nil {
		return err
	}
	tmp := m.ConfigPath + ".new"
	if err := os.WriteFile(tmp, cfg, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, m.ConfigPath); err != nil {
		return err
	}
	return m.Restart(ctx)
}

// Restart stops Xray if it is running and starts it again with the current config file.
func (m *Manager) Restart(context.Context) error {
	if !m.Installed() {
		return ErrNotInstalled
	}
	if _, err := os.Stat(m.ConfigPath); err != nil {
		return fmt.Errorf("xray config not found: %w", err)
	}
	m.stop()
	return m.start()
}

func (m *Manager) start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopping = false
	return m.spawnLocked()
}

func (m *Manager) spawnLocked() error {
	cmd := exec.Command(m.Bin, "run", "-config", m.ConfigPath)
	w := &lineWriter{ring: m.logs}
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Start(); err != nil {
		m.lastErr = err.Error()
		return err
	}
	m.cmd, m.startedAt, m.lastErr = cmd, time.Now(), ""
	go m.watch(cmd)
	return nil
}

// watch restarts Xray if it exits without being asked to.
func (m *Manager) watch(cmd *exec.Cmd) {
	err := cmd.Wait()
	m.mu.Lock()
	if m.cmd != cmd { // replaced by a newer process
		m.mu.Unlock()
		return
	}
	m.cmd = nil
	if m.stopping {
		m.mu.Unlock()
		return
	}
	m.lastErr = fmt.Sprintf("xray exited unexpectedly: %v", err)
	m.mu.Unlock()
	m.Log.Error("xray exited unexpectedly, restarting in 3s", "err", err)

	time.Sleep(3 * time.Second)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopping || m.cmd != nil {
		return
	}
	if err := m.spawnLocked(); err != nil {
		m.Log.Error("xray restart failed", "err", err)
	}
}

func (m *Manager) stop() {
	m.mu.Lock()
	cmd := m.cmd
	m.stopping = true
	m.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		_ = cmd.Process.Kill()
	} else {
		_ = cmd.Process.Signal(os.Interrupt)
	}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ { // wait up to 5s for a clean exit
			m.mu.Lock()
			gone := m.cmd != cmd
			m.mu.Unlock()
			if gone {
				close(done)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		_ = cmd.Process.Kill()
		close(done)
	}()
	<-done
}

// Stop shuts Xray down for good (until Restart is called).
func (m *Manager) Stop() { m.stop() }

// Status returns a snapshot of the process state.
func (m *Manager) Status(ctx context.Context) Status {
	st := Status{Installed: m.Installed()}
	if st.Installed {
		st.Version = m.versionString(ctx)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st.LastError = m.lastErr
	if m.cmd != nil && m.cmd.Process != nil {
		st.Running, st.PID = true, m.cmd.Process.Pid
		st.Uptime = int64(time.Since(m.startedAt).Seconds())
	}
	return st
}

func (m *Manager) versionString(ctx context.Context) string {
	m.mu.Lock()
	v := m.version
	m.mu.Unlock()
	if v != "" {
		return v
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, m.Bin, "version").Output()
	if err != nil {
		return ""
	}
	first, _, _ := strings.Cut(string(out), "\n")
	fields := strings.Fields(first) // "Xray 25.10.15 (Xray, Penetrates Everything.) ..."
	if len(fields) >= 2 {
		v = fields[1]
		m.mu.Lock()
		m.version = v
		m.mu.Unlock()
	}
	return v
}

// Logs returns the last n lines of Xray output.
func (m *Manager) Logs(n int) []string { return m.logs.last(n) }

// QueryStats reads per-user traffic counters from the running Xray; with reset the counters are zeroed.
func (m *Manager) QueryStats(ctx context.Context, reset bool) (map[string]Traffic, error) {
	if !m.Installed() {
		return nil, ErrNotInstalled
	}
	args := []string{"api", "statsquery", "--server=" + m.APIAddr}
	if reset {
		args = append(args, "-reset")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, m.Bin, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("xray statsquery: %w: %s", err, tail(stderr.String(), 300))
	}
	return ParseStatsAll(stdout.Bytes())
}

// ---- log ring buffer ----

type ring struct {
	mu    sync.Mutex
	lines []string
	max   int
}

func newRing(max int) *ring { return &ring{max: max} }

func (r *ring) add(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, line)
	if len(r.lines) > r.max {
		r.lines = r.lines[len(r.lines)-r.max:]
	}
}

func (r *ring) last(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n <= 0 || n > len(r.lines) {
		n = len(r.lines)
	}
	return append([]string(nil), r.lines[len(r.lines)-n:]...)
}

// lineWriter splits process output into lines for the ring buffer.
type lineWriter struct {
	ring *ring
	mu   sync.Mutex
	buf  []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		if line := strings.TrimRight(string(w.buf[:i]), "\r"); line != "" {
			w.ring.add(line)
		}
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

// Unchanged reports whether Xray is already running with exactly this config, so applying it would only cause a
// pointless restart (used by agents when the panel re-sends its config after a reconnect).
func (m *Manager) Unchanged(ctx context.Context, cfg []byte) bool {
	cur, err := os.ReadFile(m.ConfigPath)
	return err == nil && bytes.Equal(cur, cfg) && m.Status(ctx).Running
}
