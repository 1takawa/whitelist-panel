package nodeinstall

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

// fakeSSH is an in-process SSH server: it accepts a password (and keys the "machine" has been given) and answers
// exec requests through a handler, recording every command and the files "uploaded" with cat.
type fakeSSH struct {
	t        *testing.T
	host     string
	port     int
	hostKey  ssh.PublicKey
	password string
	noKeys   bool // refuse public key logins, like a hardened sshd

	mu         sync.Mutex
	authorized []ssh.PublicKey
	commands   []string
	scripts    []string // stdin of "sh -s" commands
	files      map[string][]byte
	handler    func(f *fakeSSH, cmd, stdin string) (string, int)
}

func startFakeSSH(t *testing.T, handler func(f *fakeSSH, cmd, stdin string) (string, int)) *fakeSSH {
	t.Helper()
	_, hk, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(hk)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSSH{t: t, hostKey: signer.PublicKey(), password: "pw-123", files: map[string][]byte{}, handler: handler}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
			if c.User() == "root" && string(p) == f.password {
				return nil, nil
			}
			return nil, io.ErrUnexpectedEOF
		},
		PublicKeyCallback: func(c ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.noKeys {
				return nil, io.ErrUnexpectedEOF
			}
			for _, a := range f.authorized {
				if bytes.Equal(a.Marshal(), k.Marshal()) {
					return nil, nil
				}
			}
			return nil, io.ErrUnexpectedEOF
		},
	}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	f.host, f.port = "127.0.0.1", ln.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(nc, cfg)
		}
	}()
	return f
}

func (f *fakeSSH) serve(nc net.Conn, cfg *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)
	for nch := range chans {
		if nch.ChannelType() != "session" {
			_ = nch.Reject(ssh.UnknownChannelType, "no")
			continue
		}
		ch, creqs, err := nch.Accept()
		if err != nil {
			return
		}
		go func() {
			for r := range creqs {
				if r.Type != "exec" {
					_ = r.Reply(false, nil)
					continue
				}
				_ = r.Reply(true, nil)
				n := binary.BigEndian.Uint32(r.Payload)
				cmd := string(r.Payload[4 : 4+n])
				stdin, _ := io.ReadAll(ch)
				out, code := f.run(cmd, string(stdin))
				_, _ = ch.Write([]byte(out))
				_ = ch.CloseWrite() // like sshd: EOF first, then the exit status
				_, _ = ch.SendRequest("exit-status", false, binary.BigEndian.AppendUint32(nil, uint32(code)))
				_ = ch.Close()
				return
			}
		}()
	}
}

var fileTarget = regexp.MustCompile(`^cat > (\S+)`)
var keyLine = regexp.MustCompile(`echo '(ssh-ed25519 [^']+)'`)

func (f *fakeSSH) run(cmd, stdin string) (string, int) {
	f.mu.Lock()
	f.commands = append(f.commands, cmd)
	if cmd == "sh -s" {
		f.scripts = append(f.scripts, stdin)
	}
	f.mu.Unlock()
	if m := fileTarget.FindStringSubmatch(cmd); m != nil {
		f.mu.Lock()
		f.files[m[1]] = []byte(stdin)
		f.mu.Unlock()
		return "", 0
	}
	if m := keyLine.FindStringSubmatch(stdin); m != nil && cmd == "sh -s" {
		pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(m[1]))
		if err != nil {
			return err.Error(), 1
		}
		f.mu.Lock()
		f.authorized = append(f.authorized, pk)
		f.mu.Unlock()
		return "", 0
	}
	if f.handler != nil {
		return f.handler(f, cmd, stdin)
	}
	return "", 0
}

func (f *fakeSSH) target() Target {
	return Target{Host: f.host, Port: f.port, User: "root", Password: f.password}
}

func (f *fakeSSH) fingerprint() string { return HostKeyFingerprint(f.hostKey) }

const goodProbe = "uid=0\narch=x86_64\nos=ubuntu:noble\npretty=Ubuntu 24.04.1 LTS\nsystemd=1\nagent=0\npanel=0\nlisten=22,80,\n"

const fp = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func okInstallHandler(_ *fakeSSH, cmd, stdin string) (string, int) {
	switch {
	case strings.Contains(stdin, "uid=%s"):
		return goodProbe, 0
	case strings.Contains(stdin, "install-node.sh --port"):
		return "==> Установка: curl unzip\n==> Xray v26.10.31: загрузка\n\nАгент установлен.\n  Порт агента:            8765\n  Отпечаток сертификата:  " + fp + "\n", 0
	case strings.Contains(stdin, "install-node.sh upgrade"):
		return "==> Обновление агента: 0.7.0 -> 0.8.0\n==> Готово\n", 0
	}
	return "", 0
}

func TestDialChecksPasswordAndHostKey(t *testing.T) {
	f := startFakeSSH(t, nil)
	ctx := context.Background()

	c, err := Dial(ctx, f.target())
	if err != nil {
		t.Fatal(err)
	}
	if c.HostKey != f.fingerprint() || len(c.HostKey) != 64 {
		t.Fatalf("the host key is reported: %q vs %q", c.HostKey, f.fingerprint())
	}
	c.Close()

	bad := f.target()
	bad.Password = "wrong"
	if _, err := Dial(ctx, bad); err == nil || !strings.Contains(err.Error(), "неверный логин или пароль") {
		t.Fatalf("a wrong password gets a clear message: %v", err)
	}

	pinned := f.target()
	pinned.HostKey = strings.Repeat("00", 32)
	if _, err := Dial(ctx, pinned); err == nil || !strings.Contains(err.Error(), "не совпадает") {
		t.Fatalf("a different host key than the one verified earlier is refused: %v", err)
	}
	pinned.HostKey = f.fingerprint()
	if c, err := Dial(ctx, pinned); err != nil {
		t.Fatalf("the right key passes: %v", err)
	} else {
		c.Close()
	}

	if _, err := Dial(ctx, Target{Host: f.host, Port: f.port, User: "root"}); err == nil {
		t.Fatal("neither password nor key")
	}
	closed := f.target()
	closed.Port = 1 // nothing listens there
	if _, err := Dial(ctx, closed); err == nil || !strings.Contains(err.Error(), "не удалось подключиться") {
		t.Fatalf("%v", err)
	}
}

func TestCheckReportsTheMachine(t *testing.T) {
	f := startFakeSSH(t, okInstallHandler)
	rep, err := Check(context.Background(), f.target(), 8765, 443, "")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK || rep.HostKey != f.fingerprint() || rep.Arch != "x86_64" || !rep.Root || !rep.Systemd || rep.OS != "Ubuntu 24.04.1 LTS" {
		t.Fatalf("%+v", rep)
	}
	if len(rep.Problems) != 0 || len(rep.Warnings) != 0 || len(rep.Listening) != 2 {
		t.Fatalf("%+v", rep)
	}
}

func TestParseProbeProblemsAndWarnings(t *testing.T) {
	cases := []struct {
		name, probe string
		problem     string
		warning     string
	}{
		{"not root", strings.Replace(goodProbe, "uid=0", "uid=1000", 1), "root", ""},
		{"arm", strings.Replace(goodProbe, "x86_64", "aarch64", 1), "x86_64", ""},
		{"centos", strings.Replace(goodProbe, "ubuntu:noble", "centos:", 1), "Ubuntu 22.04/24.04", ""},
		{"no systemd", strings.Replace(goodProbe, "systemd=1", "systemd=0", 1), "systemd", ""},
		{"agent there", strings.Replace(goodProbe, "agent=0", "agent=1", 1), "уже установлен", ""},
		{"agent port busy", strings.Replace(goodProbe, "listen=22,80,", "listen=22,8765,", 1), "8765", ""},
		{"443 busy", strings.Replace(goodProbe, "listen=22,80,", "listen=22,443,", 1), "", "443"},
		{"panel there", strings.Replace(goodProbe, "panel=0", "panel=1", 1), "", "уже стоит панель"},
	}
	for _, c := range cases {
		r := parseProbe(c.probe, "hk", 8765, 443)
		if c.problem != "" {
			if r.OK || len(r.Problems) != 1 || !strings.Contains(r.Problems[0], c.problem) {
				t.Errorf("%s: %+v", c.name, r)
			}
		} else if !r.OK || len(r.Problems) != 0 {
			t.Errorf("%s must still be installable: %+v", c.name, r)
		}
		if c.warning != "" && (len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], c.warning)) {
			t.Errorf("%s: %+v", c.name, r)
		}
	}
	for _, os := range []string{"ubuntu:jammy", "ubuntu:noble", "debian:bookworm"} {
		if r := parseProbe(strings.Replace(goodProbe, "ubuntu:noble", os, 1), "", 8765, 443); !r.OK {
			t.Errorf("%s is supported: %+v", os, r)
		}
	}
	if r := parseProbe(strings.Replace(goodProbe, "ubuntu:noble", "debian:bullseye", 1), "", 8765, 443); r.OK {
		t.Error("Debian 11 is not supported")
	}
}

func TestGenerateKeyRoundTrip(t *testing.T) {
	priv, line, err := GenerateKey("anypanel-test")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey([]byte(priv))
	if err != nil {
		t.Fatalf("the private key parses: %v", err)
	}
	pk, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil || comment != "anypanel-test" || !bytes.Equal(pk.Marshal(), signer.PublicKey().Marshal()) {
		t.Fatalf("the authorized_keys line matches the private key: %v %q", err, comment)
	}
	if !safeKeyLine.MatchString(line) {
		t.Fatalf("the line is safe to put into a shell script: %q", line)
	}
	if _, err := installKeyScript("ssh-ed25519 AAAA'; rm -rf / #"); err == nil {
		t.Fatal("anything that is not a plain key line is refused before it reaches a shell")
	}
}

func tempBinary(t *testing.T) (string, []byte) {
	t.Helper()
	data := bytes.Repeat([]byte("ELF-binary-"), 100000)
	p := filepath.Join(t.TempDir(), "anypanel")
	if err := os.WriteFile(p, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return p, data
}

func options(f *fakeSSH, bin string) Options {
	return Options{Target: f.target(), Binary: bin, Token: strings.Repeat("aB3", 16), AgentPort: 8765, CertHost: "203.0.113.20", KeyName: "anypanel-test"}
}

func TestInstallEndToEnd(t *testing.T) {
	f := startFakeSSH(t, okInstallHandler)
	bin, data := tempBinary(t)
	var log []string
	res, err := Install(context.Background(), options(f, bin), func(s string) { log = append(log, s) })
	if err != nil {
		t.Fatalf("%v\n%s", err, strings.Join(log, "\n"))
	}
	if res.Fingerprint != fp || res.HostKey != f.fingerprint() || res.PrivateKey == "" {
		t.Fatalf("%+v", res)
	}

	// The panel's key now logs in without a password.
	c, err := Dial(context.Background(), Target{Host: f.host, Port: f.port, User: "root", PrivateKey: res.PrivateKey})
	if err != nil {
		t.Fatalf("the generated key was installed on the server: %v", err)
	}
	c.Close()

	f.mu.Lock()
	defer f.mu.Unlock()
	if got := f.files[remoteDir+"/anypanel"]; !bytes.Equal(got, data) {
		t.Fatalf("the whole binary is uploaded intact (%d of %d bytes)", len(got), len(data))
	}
	if got := f.files[remoteDir+"/install-node.sh"]; !bytes.Equal(got, Script()) || bytes.Contains(got, []byte("\r")) {
		t.Fatal("the installer script is uploaded with Unix line endings")
	}
	for _, cmd := range f.commands {
		if strings.Contains(cmd, strings.Repeat("aB3", 16)) {
			t.Fatalf("the agent token must not appear on a command line: %s", cmd)
		}
	}
	run := ""
	for _, s := range f.scripts {
		if strings.Contains(s, "install-node.sh --port") {
			run = s
		}
	}
	if !strings.Contains(run, "ANYPANEL_AGENT_TOKEN='"+strings.Repeat("aB3", 16)+"'") ||
		!strings.Contains(run, "--port 8765 --host '203.0.113.20' --yes </dev/null") {
		t.Fatalf("the installer gets the token through its environment: %q", run)
	}
	if last := f.commands[len(f.commands)-1]; last != "rm -rf "+remoteDir {
		t.Fatalf("the uploaded files are removed at the end, last command: %q", last)
	}
	joined := strings.Join(log, "\n")
	if !strings.Contains(joined, "Отпечаток сертификата") || !strings.Contains(joined, "вход по ключу проверен") {
		t.Fatalf("the log shows what happened:\n%s", joined)
	}
	if strings.Contains(joined, f.password) {
		t.Fatal("the password is never logged")
	}
}

func TestInstallWorksWhenKeyLoginIsDisabled(t *testing.T) {
	f := startFakeSSH(t, okInstallHandler)
	f.noKeys = true
	bin, _ := tempBinary(t)
	var log []string
	res, err := Install(context.Background(), options(f, bin), func(s string) { log = append(log, s) })
	if err != nil {
		t.Fatal(err)
	}
	if res.PrivateKey != "" || res.Fingerprint != fp {
		t.Fatalf("without key login the node is installed but has no stored key: %+v", res)
	}
	if !strings.Contains(strings.Join(log, "\n"), "Вход по новому ключу не сработал") {
		t.Fatalf("%v", log)
	}
}

func TestInstallReportsInstallerFailure(t *testing.T) {
	f := startFakeSSH(t, func(f *fakeSSH, cmd, stdin string) (string, int) {
		if strings.Contains(stdin, "install-node.sh --port") {
			return "Ошибка: контрольная сумма Xray не совпала\n", 1
		}
		return "", 0
	})
	bin, _ := tempBinary(t)
	var log []string
	_, err := Install(context.Background(), options(f, bin), func(s string) { log = append(log, s) })
	if err == nil || !strings.Contains(err.Error(), "код 1") {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(strings.Join(log, "\n"), "контрольная сумма Xray") {
		t.Fatalf("the installer's own output reaches the admin:\n%v", log)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.commands[len(f.commands)-1] != "rm -rf "+remoteDir {
		t.Fatal("cleanup also runs after a failure")
	}
}

func TestInstallNeedsAFingerprintFromTheInstaller(t *testing.T) {
	f := startFakeSSH(t, func(f *fakeSSH, cmd, stdin string) (string, int) { return "всё хорошо\n", 0 })
	bin, _ := tempBinary(t)
	if _, err := Install(context.Background(), options(f, bin), func(string) {}); err == nil || !strings.Contains(err.Error(), "отпечаток") {
		t.Fatalf("%v", err)
	}
}

func TestInstallRefusesBadInput(t *testing.T) {
	f := startFakeSSH(t, okInstallHandler)
	bin, _ := tempBinary(t)
	for name, mod := range map[string]func(*Options){
		"short token":  func(o *Options) { o.Token = "short" },
		"token quote":  func(o *Options) { o.Token = strings.Repeat("a", 31) + "'" },
		"cert host":    func(o *Options) { o.CertHost = "x'; reboot #" },
		"port":         func(o *Options) { o.AgentPort = 0 },
		"missing file": func(o *Options) { o.Binary = filepath.Join(t.TempDir(), "nope") },
	} {
		o := options(f, bin)
		mod(&o)
		if _, err := Install(context.Background(), o, func(string) {}); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.scripts {
		if strings.Contains(s, "reboot") {
			t.Fatal("hostile values never reach the server")
		}
	}
}

func TestUpgradeUploadsAndRunsUpgrade(t *testing.T) {
	f := startFakeSSH(t, okInstallHandler)
	bin, data := tempBinary(t)
	priv, line, _ := GenerateKey("k")
	pk, _, _, _, _ := ssh.ParseAuthorizedKey([]byte(line))
	f.authorized = append(f.authorized, pk)
	var log []string
	err := Upgrade(context.Background(), Target{Host: f.host, Port: f.port, User: "root", PrivateKey: priv, HostKey: f.fingerprint()}, bin, func(s string) { log = append(log, s) })
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !bytes.Equal(f.files[remoteDir+"/anypanel"], data) {
		t.Fatal("the new binary is uploaded")
	}
	if len(f.scripts) == 0 || !strings.Contains(f.scripts[len(f.scripts)-1], "install-node.sh upgrade") {
		t.Fatalf("%v", f.scripts)
	}
	if !strings.Contains(strings.Join(log, "\n"), "0.7.0 -> 0.8.0") {
		t.Fatalf("%v", log)
	}
	if f.commands[len(f.commands)-1] != "rm -rf "+remoteDir {
		t.Fatal("cleanup")
	}
}

func TestLineWriterSplitsOutputIntoLines(t *testing.T) {
	var got []string
	w := &lineWriter{fn: func(s string) { got = append(got, s) }}
	_, _ = w.Write([]byte("one\r\ntw"))
	_, _ = w.Write([]byte("o\n\nthree"))
	w.flush()
	if strings.Join(got, "|") != "one|two|three" {
		t.Fatalf("%q", got)
	}
	if w.all.String() != "one\r\ntwo\n\nthree" {
		t.Fatal(w.all.String())
	}
}

func TestEmbeddedScriptIsTheNodeInstaller(t *testing.T) {
	s := string(Script())
	if !strings.HasPrefix(s, "#!/usr/bin/env bash") || !strings.Contains(s, "do_install()") || strings.Contains(s, "\r") {
		t.Fatal("the embedded script must be install-node.sh with LF line endings")
	}
	if _, err := strconv.Atoi("1"); err != nil {
		t.Fatal(err)
	}
}

func TestExplainTurnsLibraryErrorsIntoAdminText(t *testing.T) {
	for _, in := range []string{
		"ssh: handshake failed: ssh: unable to authenticate, attempted methods [none password], no supported methods remain",
		"ssh: handshake failed: ssh: unexpected message type 51 (expected 60)",
	} {
		if got := explain(errors.New(in)); !strings.Contains(got, "неверный логин или пароль") {
			t.Errorf("%q -> %q", in, got)
		}
	}
	if got := explain(errors.New("something else")); got != "something else" {
		t.Fatalf("%q", got)
	}
}

func TestProbeNamesWhoHoldsTheNeededPorts(t *testing.T) {
	probe := strings.Replace(goodProbe, "listen=22,80,", "listen=22,443,", 1) + "holder=22:sshd\nholder=443:nginx\n"
	r := parseProbe(probe, "hk", 8765, 443)
	if !r.OK || len(r.BusyPorts) != 1 || r.BusyPorts[0] != (PortHolder{443, "nginx"}) || len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "nginx") {
		t.Fatalf("%+v", r)
	}
	// Without the holder name the warning still says the port is busy.
	r = parseProbe(strings.Replace(goodProbe, "listen=22,80,", "listen=22,443,", 1), "hk", 8765, 443)
	if len(r.BusyPorts) != 1 || r.BusyPorts[0].Holder != "" || !strings.Contains(r.Warnings[0], "443") {
		t.Fatalf("%+v", r)
	}
	if r := parseProbe(goodProbe, "hk", 8765, 443); len(r.BusyPorts) != 0 {
		t.Fatalf("a free port is not busy: %+v", r.BusyPorts)
	}
}

func TestProbeWithADomainNeedsPort80AndARecordPointingHere(t *testing.T) {
	withDomain := func(domainIP string) string {
		return goodProbe + "domain=node.example.com\ndomain_ip=" + domainIP + "\nserver_ip=10.0.0.5\npublic_ip=203.0.113.20\n"
	}
	// Port 80 is in goodProbe's listen list: with a domain it is needed (certificate), without one it is not.
	r := parseProbe(withDomain("203.0.113.20"), "hk", 8765, 443)
	if !r.OK || len(r.BusyPorts) != 1 || r.BusyPorts[0].Port != 80 || r.DomainIP != "203.0.113.20" || r.ServerIP != "203.0.113.20" {
		t.Fatalf("%+v", r)
	}
	if r := parseProbe(goodProbe, "hk", 8765, 443); len(r.BusyPorts) != 0 {
		t.Fatalf("port 80 only matters with a domain: %+v", r.BusyPorts)
	}
	// The address may also be the one the machine itself sees (behind NAT the public one differs).
	if r := parseProbe(withDomain("10.0.0.5"), "hk", 8765, 443); !r.OK {
		t.Fatalf("%+v", r)
	}
	if r := parseProbe(withDomain(""), "hk", 8765, 443); r.OK || !strings.Contains(r.Problems[0], "нет A-записи") {
		t.Fatalf("no A record: %+v", r)
	}
	r = parseProbe(withDomain("198.51.100.7"), "hk", 8765, 443)
	if r.OK || !strings.Contains(r.Problems[0], "198.51.100.7") || !strings.Contains(r.Problems[0], "203.0.113.20") {
		t.Fatalf("a domain pointing elsewhere is a problem that names both addresses: %+v", r)
	}
}

func TestCheckSendsOnlyAValidDomainToTheServer(t *testing.T) {
	f := startFakeSSH(t, func(_ *fakeSSH, _ string, stdin string) (string, int) {
		if strings.Contains(stdin, "uid=%s") {
			return goodProbe + "domain=node.example.com\ndomain_ip=203.0.113.20\npublic_ip=203.0.113.20\n", 0
		}
		return "", 0
	})
	rep, err := Check(context.Background(), f.target(), 8765, 443, "node.example.com")
	if err != nil || !rep.OK || rep.DomainIP != "203.0.113.20" {
		t.Fatalf("%+v %v", rep, err)
	}
	f.mu.Lock()
	var script string
	for _, s := range f.scripts {
		if strings.Contains(s, "uid=%s") {
			script = s
		}
	}
	f.mu.Unlock()
	if !strings.HasPrefix(script, "ANYPANEL_DOMAIN='node.example.com'\n") {
		t.Fatalf("the domain reaches the probe as a variable: %q", script[:60])
	}
	for _, bad := range []string{"a'; rm -rf /; '.com", "no_dots", "x y.com", "-a.com", strings.Repeat("a", 300) + ".com"} {
		if _, err := Check(context.Background(), f.target(), 8765, 443, bad); err == nil {
			t.Errorf("%q must be refused before anything is sent", bad)
		}
	}
}

func TestInstallFreesThePortFirstWhenAsked(t *testing.T) {
	f := startFakeSSH(t, okInstallHandler)
	bin, _ := tempBinary(t)
	o := options(f, bin)
	o.FreePorts = []int{443}
	var log []string
	if _, err := Install(context.Background(), o, func(s string) { log = append(log, s) }); err != nil {
		t.Fatalf("%v\n%s", err, strings.Join(log, "\n"))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	free, run := -1, -1
	for i, s := range f.scripts {
		if strings.Contains(s, `port=443`) && strings.Contains(s, "systemctl disable --now") {
			free = i
		}
		if strings.Contains(s, "install-node.sh --port") {
			run = i
		}
	}
	if free < 0 || run < 0 || free > run {
		t.Fatalf("the port is cleared before the installer runs (free=%d run=%d)", free, run)
	}
	if !strings.Contains(strings.Join(log, "\n"), "Освобождаю порт 443") {
		t.Fatalf("the log says so:\n%s", strings.Join(log, "\n"))
	}
	if !strings.Contains(freePortScript(443), `[ "$name" = sshd ]`) {
		t.Fatal("sshd must never be stopped")
	}
}

func TestInstallWithoutForceDoesNotTouchThePort(t *testing.T) {
	f := startFakeSSH(t, okInstallHandler)
	bin, _ := tempBinary(t)
	if _, err := Install(context.Background(), options(f, bin), func(string) {}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.scripts {
		if strings.Contains(s, "systemctl disable --now") {
			t.Fatal("nothing is stopped unless the admin asked for it")
		}
	}
}

func TestInstallStopsWhenThePortCannotBeFreed(t *testing.T) {
	f := startFakeSSH(t, func(f *fakeSSH, cmd, stdin string) (string, int) {
		if strings.Contains(stdin, "listeners()") {
			return "Порт 443 занят sshd\n", 3
		}
		return okInstallHandler(f, cmd, stdin)
	})
	bin, _ := tempBinary(t)
	o := options(f, bin)
	o.FreePorts = []int{443}
	if _, err := Install(context.Background(), o, func(string) {}); err == nil || !strings.Contains(err.Error(), "не освобождён") {
		t.Fatalf("the installation stops: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.scripts {
		if strings.Contains(s, "install-node.sh --port") {
			t.Fatal("the installer must not run after a failed release")
		}
	}
	o.FreePorts = []int{f.port}
	f.mu.Unlock()
	_, err := Install(context.Background(), o, func(string) {})
	f.mu.Lock()
	if err == nil || !strings.Contains(err.Error(), "SSH") {
		t.Fatalf("the SSH port itself cannot be freed: %v", err)
	}
}

// The probe's awk must cut the program name cleanly out of real `ss -ltnp` output (it once kept a trailing quote).
func TestProbeAwkExtractsTheProgramName(t *testing.T) {
	if _, err := exec.LookPath("awk"); err != nil {
		t.Skip("no awk")
	}
	var awk string
	for _, l := range strings.Split(probeScript, "\n") {
		if strings.HasPrefix(l, "ss -ltnp") {
			awk = l[strings.Index(l, "awk "):]
		}
	}
	if awk == "" {
		t.Fatal("the probe has no holder line")
	}
	ss := `State  Recv-Q Send-Q Local Address:Port Peer Address:Port Process
LISTEN 0      511          0.0.0.0:443        0.0.0.0:*    users:(("nginx",pid=812,fd=6),("nginx",pid=811,fd=6))
LISTEN 0      128             [::]:22             [::]:*    users:(("sshd",pid=700,fd=4))
`
	cmd := exec.Command("sh", "-c", awk)
	cmd.Stdin = strings.NewReader(ss)
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("awk not usable here: %v", err)
	}
	if got := string(out); got != "holder=443:nginx\nholder=22:sshd\n" {
		t.Fatalf("got %q", got)
	}
}

func TestInstallTellsTheInstallerWhichPortsToOpen(t *testing.T) {
	f := startFakeSSH(t, okInstallHandler)
	bin, _ := tempBinary(t)
	o := options(f, bin)
	o.OpenPorts = []int{443, 0, 70000, 8443}
	if _, err := Install(context.Background(), o, func(string) {}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	found := false
	for _, s := range f.scripts {
		if strings.Contains(s, "install-node.sh --port") {
			found = true
			if !strings.Contains(s, "export ANYPANEL_OPEN_PORTS='443,8443'\n") {
				t.Fatalf("only valid ports are passed: %q", s)
			}
		}
	}
	if !found {
		t.Fatal("the installer did not run")
	}
}

func TestNodeInstallerOpensUfwOnlyForThePanelAndRespectsSetU(t *testing.T) {
	s := string(Script())
	for _, want := range []string{`ufw allow from "$src" to any port "$PORT" proto tcp`, `SSH_CLIENT:-`, "ANYPANEL_OPEN_PORTS"} {
		if !strings.Contains(s, want) {
			t.Errorf("the installer lacks %q", want)
		}
	}
}

func TestOpenFirewallRunsTheScriptOverSSH(t *testing.T) {
	f := startFakeSSH(t, func(_ *fakeSSH, _ string, stdin string) (string, int) {
		if strings.Contains(stdin, "agent=8765") {
			return "ufw: открываю порт агента 8765/tcp для панели (203.0.113.9)\nПорты открыты\n", 0
		}
		return "", 0
	})
	var log []string
	if err := OpenFirewall(context.Background(), f.target(), 8765, []int{443}, func(s string) { log = append(log, s) }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(log, "\n"), "Порты открыты") {
		t.Fatalf("%v", log)
	}
	f.mu.Lock()
	var script string
	for _, s := range f.scripts {
		if strings.Contains(s, "agent=8765") {
			script = s
		}
	}
	f.mu.Unlock()
	for _, want := range []string{`ports="443"`, `ufw allow from "$src" to any port "$agent" proto tcp`, `src="${src%% *}"`, "Status: active"} {
		if !strings.Contains(script, want) {
			t.Errorf("the script lacks %q:\n%s", want, script)
		}
	}
	if err := OpenFirewall(context.Background(), f.target(), 0, nil, func(string) {}); err == nil {
		t.Fatal("a bad agent port is refused")
	}

	bad := startFakeSSH(t, func(*fakeSSH, string, string) (string, int) { return "ufw error", 5 })
	if err := OpenFirewall(context.Background(), bad.target(), 8765, nil, func(string) {}); err == nil || !strings.Contains(err.Error(), "код 5") {
		t.Fatalf("a failing ufw is reported: %v", err)
	}
}

func TestInstallPassesTheRealityDomainAndFreesPorts(t *testing.T) {
	f := startFakeSSH(t, okInstallHandler)
	bin, _ := tempBinary(t)
	o := options(f, bin)
	o.RealityDomain = "node.example.com"
	o.FreePorts = []int{80, 443}
	if _, err := Install(context.Background(), o, func(string) {}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	freed := 0
	for _, s := range f.scripts {
		if strings.Contains(s, "listeners()") {
			freed++
		}
		if strings.Contains(s, "install-node.sh --port") && !strings.Contains(s, "export ANYPANEL_REALITY_DOMAIN='node.example.com'\n") {
			t.Fatalf("the installer gets the domain: %q", s)
		}
	}
	if freed != 2 {
		t.Fatalf("ports 80 and 443 are both cleared, got %d scripts", freed)
	}

	o.RealityDomain = "bad domain"
	if _, err := Install(context.Background(), o, func(string) {}); err == nil {
		t.Fatal("a malformed domain is refused before any connection")
	}
}

func TestSetupRealityUploadsOnlyTheScriptAndRunsTheRealityMode(t *testing.T) {
	f := startFakeSSH(t, func(_ *fakeSSH, _ string, stdin string) (string, int) {
		if strings.Contains(stdin, "install-node.sh reality") {
			return "==> REALITY-сайт готов\n", 0
		}
		return "", 0
	})
	var log []string
	if err := SetupReality(context.Background(), f.target(), "node.example.com", []int{80, 443}, func(s string) { log = append(log, s) }); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.files[remoteDir+"/anypanel"]; ok {
		t.Fatal("the agent binary is not needed for this step")
	}
	if !bytes.Equal(f.files[remoteDir+"/install-node.sh"], Script()) {
		t.Fatal("the installer script is uploaded")
	}
	ran := false
	for _, s := range f.scripts {
		if strings.Contains(s, "bash install-node.sh reality --reality-domain 'node.example.com' --yes") {
			ran = true
		}
	}
	if !ran {
		t.Fatalf("the reality mode is started: %v", f.scripts)
	}
	if last := f.commands[len(f.commands)-1]; last != "rm -rf "+remoteDir {
		t.Fatalf("the upload is removed at the end: %q", last)
	}
	if err := SetupReality(context.Background(), f.target(), "bad domain", nil, func(string) {}); err == nil {
		t.Fatal("bad domain")
	}
}

// What the node installer builds must stay the nginx-SNI scheme: these are the lines that make it so.
func TestNodeScriptBuildsTheThreeXUIProScheme(t *testing.T) {
	s := string(Script())
	for _, want := range []string{
		"hostnames;", "server 127.0.0.1:${XRAY_REALITY_PORT};", "proxy_protocol on;", "ssl_preread     on;", "listen          443;", // nginx stream
		"listen ${DECOY_PORT} ssl http2;", "XRAY_REALITY_PORT=8443", "DECOY_PORT=9443", // decoy site and ports
		"certbot certonly --standalone", "authenticator = nginx", "mozaroc/3x-ui-pro", "assets/fake-sites", // certificate and site content
		"ufw allow 80/tcp", "ufw allow 443/tcp",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the node installer lacks %q", want)
		}
	}
	// The site is set up before the agent is written, so a failure leaves nothing behind and the install can be repeated.
	site := strings.Index(s, `if [[ -n "$REALITY_DOMAIN" ]]; then setup_reality; fi`)
	agent := strings.Index(s, "write_files\n  systemctl enable --now anypanel-agent")
	if site < 0 || agent < 0 || site > agent {
		t.Fatalf("order: site at %d, agent at %d", site, agent)
	}
}
