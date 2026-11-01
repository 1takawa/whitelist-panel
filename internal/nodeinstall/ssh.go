// Package nodeinstall puts the agent on an extra server over SSH: it checks the machine, creates an SSH key for later
// maintenance, uploads this binary together with install-node.sh and runs the installer.
package nodeinstall

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Target is the machine to connect to. Either Password or PrivateKey (PEM) authenticates; HostKey, when set, is the
// SHA-256 (hex) of the SSH host key seen earlier and a different key is refused.
type Target struct {
	Host       string
	Port       int
	User       string
	Password   string
	PrivateKey string
	HostKey    string
}

// Conn is an open SSH connection.
type Conn struct {
	cli     *ssh.Client
	HostKey string // SHA-256 (hex) of the host key presented
}

const dialTimeout = 15 * time.Second

// HostKeyFingerprint is how host keys are shown and compared: the SHA-256 of the wire-format public key, in hex.
func HostKeyFingerprint(k ssh.PublicKey) string {
	sum := sha256.Sum256(k.Marshal())
	return hex.EncodeToString(sum[:])
}

// Dial connects and authenticates. The error text is meant for the admin.
func Dial(ctx context.Context, t Target) (*Conn, error) {
	if t.Port == 0 {
		t.Port = 22
	}
	var methods []ssh.AuthMethod
	if t.PrivateKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(t.PrivateKey))
		if err != nil {
			return nil, fmt.Errorf("не удалось прочитать SSH-ключ: %w", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if t.Password != "" {
		methods = append(methods, ssh.Password(t.Password),
			// Many servers only offer keyboard-interactive for password logins.
			ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
				ans := make([]string, len(qs))
				for i := range ans {
					ans[i] = t.Password
				}
				return ans, nil
			}))
	}
	if len(methods) == 0 {
		return nil, errors.New("нужен пароль или SSH-ключ")
	}

	var seen string
	cfg := &ssh.ClientConfig{
		User: t.User,
		Auth: methods,
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			seen = HostKeyFingerprint(k)
			if t.HostKey != "" && t.HostKey != seen {
				return errors.New("ключ SSH-сервера не совпадает с проверенным ранее (возможна подмена сервера)")
			}
			return nil
		},
		Timeout: dialTimeout,
	}
	addr := net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
	d := net.Dialer{Timeout: dialTimeout}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("не удалось подключиться к %s: %w", addr, unwrapNet(err))
	}
	_ = raw.SetDeadline(time.Now().Add(dialTimeout + 10*time.Second)) // the handshake must not hang forever
	cc, chans, reqs, err := ssh.NewClientConn(raw, addr, cfg)
	if err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("SSH: %s", explain(err))
	}
	_ = raw.SetDeadline(time.Time{})
	return &Conn{cli: ssh.NewClient(cc, chans, reqs), HostKey: seen}, nil
}

func unwrapNet(err error) error {
	var oe *net.OpError
	if errors.As(err, &oe) && oe.Err != nil {
		return oe.Err
	}
	return err
}

func explain(err error) string {
	msg := err.Error()
	switch {
	// "message type 51" is what the library reports when a server that has password login switched off rejects the
	// keyboard-interactive attempt.
	case strings.Contains(msg, "unable to authenticate"), strings.Contains(msg, "unexpected message type 51"):
		return "неверный логин или пароль (или вход по паролю отключён на сервере)"
	case strings.Contains(msg, "ключ SSH-сервера не совпадает"):
		return "ключ SSH-сервера не совпадает с проверенным ранее (возможна подмена сервера)"
	}
	return msg
}

func (c *Conn) Close() { _ = c.cli.Close() }

// Run executes a command, feeding stdin when given and writing stdout and stderr to out. The returned exit code is
// meaningful when err is nil or an exit error; ctx cancellation closes the session.
func (c *Conn) Run(ctx context.Context, cmd string, stdin io.Reader, out io.Writer) (int, error) {
	sess, err := c.cli.NewSession()
	if err != nil {
		return -1, err
	}
	defer sess.Close()
	// stdout and stderr are read through separate pipes and merged under a lock: handing the same writer to both
	// can lose output.
	stdout, err := sess.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderr, err := sess.StderrPipe()
	if err != nil {
		return -1, err
	}
	sess.Stdin = stdin
	if err := sess.Start(cmd); err != nil {
		return -1, err
	}
	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)
	for _, r := range []io.Reader{stdout, stderr} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 32<<10)
			for {
				n, err := r.Read(buf)
				if n > 0 {
					mu.Lock()
					_, _ = out.Write(buf[:n])
					mu.Unlock()
				}
				if err != nil {
					return
				}
			}
		}()
	}
	done := make(chan error, 1)
	go func() {
		wg.Wait() // everything the command printed has been read
		done <- sess.Wait()
	}()
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = sess.Close()
		_ = c.cli.Close()
		return -1, ctx.Err()
	}
	var ee *ssh.ExitError
	if errors.As(err, &ee) {
		return ee.ExitStatus(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

// output runs a command and returns everything it printed.
func (c *Conn) output(ctx context.Context, cmd string, stdin io.Reader) (string, int, error) {
	var b bytes.Buffer
	code, err := c.Run(ctx, cmd, stdin, &b)
	return b.String(), code, err
}

// lineWriter turns a byte stream into log lines.
type lineWriter struct {
	mu  sync.Mutex
	buf []byte
	fn  func(string)
	all bytes.Buffer
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.all.Write(p)
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		if line := strings.TrimRight(string(w.buf[:i]), "\r"); line != "" && w.fn != nil {
			w.fn(line)
		}
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 && w.fn != nil {
		w.fn(strings.TrimRight(string(w.buf), "\r\n"))
	}
	w.buf = nil
}
