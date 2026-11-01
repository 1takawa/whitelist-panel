package nodeinstall

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	_ "embed"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

//go:embed install-node.sh
var installScript []byte

// Script is install-node.sh with Unix line endings.
func Script() []byte { return bytes.ReplaceAll(installScript, []byte("\r\n"), []byte("\n")) }

const remoteDir = "/var/tmp/anypanel-node-install"

// ---- check ----

// Report is what the panel learned about a machine before installing anything.
type Report struct {
	HostKey        string       `json:"hostKey"` // SHA-256 (hex) of the SSH host key: show it, and send it back on install
	OS             string       `json:"os"`
	Arch           string       `json:"arch"`
	Root           bool         `json:"root"`
	Systemd        bool         `json:"systemd"`
	AgentInstalled bool         `json:"agentInstalled"`
	PanelInstalled bool         `json:"panelInstalled"`
	Listening      []int        `json:"listening"`
	BusyPorts      []PortHolder `json:"busyPorts"` // ports the install needs that another program already holds
	DomainIP       string       `json:"domainIp"`  // where the REALITY domain points (when one was asked about)
	ServerIP       string       `json:"serverIp"`  // this machine's public address
	Problems       []string     `json:"problems"`  // the installation cannot proceed
	Warnings       []string     `json:"warnings"`
	OK             bool         `json:"ok"`
}

// PortHolder is a TCP port some program already listens on; Holder is its name when it could be found out.
type PortHolder struct {
	Port   int    `json:"port"`
	Holder string `json:"holder"`
}

const probeScript = `
printf 'uid=%s\n' "$(id -u)"
printf 'arch=%s\n' "$(uname -m)"
if [ -r /etc/os-release ]; then . /etc/os-release; fi
printf 'os=%s:%s\n' "${ID:-}" "${VERSION_CODENAME:-}"
printf 'pretty=%s\n' "${PRETTY_NAME:-}"
if command -v systemctl >/dev/null 2>&1; then echo systemd=1; else echo systemd=0; fi
if [ -e /etc/systemd/system/anypanel-agent.service ]; then echo agent=1; else echo agent=0; fi
if [ -e /etc/systemd/system/anypanel.service ]; then echo panel=1; else echo panel=0; fi
printf 'listen=%s\n' "$( (ss -ltn 2>/dev/null || netstat -ltn 2>/dev/null) | awk 'NR>1 {n=split($4,a,":"); print a[n]}' | sort -un | tr '\n' ',')"
ss -ltnp 2>/dev/null | awk 'NR>1 {n=split($4,a,":"); if (match($0,/\(\("[^"]+"/)) print "holder=" a[n] ":" substr($0,RSTART+3,RLENGTH-4)}'
`

// domainProbe is added when the REALITY domain is known: where does it point, and what is this machine's address.
const domainProbe = `
printf 'domain=%s\n' "$ANYPANEL_DOMAIN"
printf 'domain_ip=%s\n' "$(getent ahostsv4 "$ANYPANEL_DOMAIN" 2>/dev/null | awk 'NR==1{print $1}')"
printf 'server_ip=%s\n' "$(ip route get 8.8.8.8 2>/dev/null | grep -Po -- 'src \K\S*')"
printf 'public_ip=%s\n' "$(curl -fsS -m 6 https://ipv4.icanhazip.com 2>/dev/null | tr -d '[:space:]')"
`

var supportedOS = []string{"ubuntu:jammy", "ubuntu:noble", "debian:bookworm"}

var domainRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$`)

// ValidDomain reports whether s is a host name the installer accepts as a REALITY domain.
func ValidDomain(s string) bool { return len(s) <= 253 && domainRe.MatchString(s) }

// Check connects and inspects the machine. agentPort and connectPort are the ports the install would use; domain,
// when not empty, is the REALITY domain the install will get a certificate for (it must point at this machine, and
// the installer then also needs port 80).
func Check(ctx context.Context, t Target, agentPort, connectPort int, domain string) (*Report, error) {
	script := probeScript
	if domain != "" {
		if !ValidDomain(domain) {
			return nil, errors.New("неверный домен REALITY")
		}
		script = "ANYPANEL_DOMAIN='" + domain + "'\n" + probeScript + domainProbe
	}
	c, err := Dial(ctx, t)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	out, code, err := c.output(ctx, "sh -s", strings.NewReader(script))
	if err != nil || code != 0 {
		return nil, fmt.Errorf("не удалось выполнить проверку на сервере: %v %s", err, strings.TrimSpace(out))
	}
	return parseProbe(out, c.HostKey, agentPort, connectPort), nil
}

func parseProbe(out, hostKey string, agentPort, connectPort int) *Report {
	kv, holders := map[string]string{}, map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			if k == "holder" {
				if p, name, ok := strings.Cut(v, ":"); ok {
					holders[p] = name
				}
				continue
			}
			kv[k] = v
		}
	}
	r := &Report{HostKey: hostKey, Arch: kv["arch"], OS: kv["pretty"], Root: kv["uid"] == "0",
		Systemd: kv["systemd"] == "1", AgentInstalled: kv["agent"] == "1", PanelInstalled: kv["panel"] == "1",
		Problems: []string{}, Warnings: []string{}, Listening: []int{}}
	if r.OS == "" {
		r.OS = strings.TrimSuffix(kv["os"], ":")
	}
	for _, p := range strings.Split(kv["listen"], ",") {
		if n, err := strconv.Atoi(p); err == nil {
			r.Listening = append(r.Listening, n)
		}
	}
	problem := func(s string) { r.Problems = append(r.Problems, s) }
	if !r.Root {
		problem("нужен вход под root: установщик ставит службы и пишет в /etc")
	}
	if r.Arch != "x86_64" {
		problem(fmt.Sprintf("архитектура %q не поддерживается: нужен x86_64", r.Arch))
	}
	if !slices.Contains(supportedOS, kv["os"]) {
		problem("поддерживаются Ubuntu 22.04/24.04 и Debian 12")
	}
	if !r.Systemd {
		problem("на сервере нет systemd")
	}
	if r.AgentInstalled {
		problem("агент уже установлен на этом сервере: добавьте его вручную с токеном или удалите прежнюю установку")
	} else if slices.Contains(r.Listening, agentPort) {
		problem(fmt.Sprintf("порт %d уже занят: выберите другой порт агента", agentPort))
	}
	if r.PanelInstalled {
		r.Warnings = append(r.Warnings, "на сервере уже стоит панель: нода будет работать рядом с ней, порты не должны пересекаться")
	}
	needed := []int{connectPort}
	if kv["domain"] != "" {
		needed = []int{80, connectPort} // 80 is for the certificate, the connection port is where nginx takes clients
	}
	r.BusyPorts = []PortHolder{}
	for _, p := range needed {
		if p <= 0 || !slices.Contains(r.Listening, p) || slices.ContainsFunc(r.BusyPorts, func(h PortHolder) bool { return h.Port == p }) {
			continue
		}
		h := PortHolder{Port: p, Holder: holders[strconv.Itoa(p)]}
		r.BusyPorts = append(r.BusyPorts, h)
		who := "другой программой"
		if h.Holder != "" {
			who = "программой " + h.Holder
		}
		r.Warnings = append(r.Warnings, fmt.Sprintf("порт %d занят %s. Можно освободить его принудительно при установке", p, who))
	}
	if d := kv["domain"]; d != "" {
		r.DomainIP, r.ServerIP = kv["domain_ip"], kv["public_ip"]
		if r.ServerIP == "" {
			r.ServerIP = kv["server_ip"]
		}
		switch {
		case r.DomainIP == "":
			problem(fmt.Sprintf("у домена %s нет A-записи: создайте запись типа A на адрес %s и повторите проверку", d, r.ServerIP))
		case r.DomainIP != kv["server_ip"] && r.DomainIP != kv["public_ip"]:
			problem(fmt.Sprintf("домен %s указывает на %s, а адрес этого сервера %s: исправьте A-запись (после смены DNS может пройти несколько минут)", d, r.DomainIP, r.ServerIP))
		}
	}
	r.OK = len(r.Problems) == 0
	return r
}

// ---- keys ----

// GenerateKey creates an Ed25519 key pair: the private key as PEM and the public one as an authorized_keys line.
func GenerateKey(comment string) (privatePEM, authorizedLine string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	blk, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", "", err
	}
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", err
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp))) + " " + comment
	return string(pem.EncodeToMemory(blk)), line, nil
}

var safeKeyLine = regexp.MustCompile(`^ssh-ed25519 [A-Za-z0-9+/=]+ [A-Za-z0-9._@-]+$`)

func installKeyScript(line string) (string, error) {
	if !safeKeyLine.MatchString(line) {
		return "", errors.New("unexpected public key format")
	}
	return `set -e
umask 077
mkdir -p "$HOME/.ssh"
touch "$HOME/.ssh/authorized_keys"
chmod 700 "$HOME/.ssh"
chmod 600 "$HOME/.ssh/authorized_keys"
grep -qxF '` + line + `' "$HOME/.ssh/authorized_keys" || echo '` + line + `' >> "$HOME/.ssh/authorized_keys"
`, nil
}

// ---- install and upgrade ----

// Options describes one installation.
type Options struct {
	Target
	Binary    string // path of the anypanel executable to upload
	Token     string // agent token (letters and digits, at least 32)
	AgentPort int
	CertHost  string // goes into the agent certificate
	KeyName   string // comment of the generated SSH key
	// FreePorts are TCP ports to clear before installing: whatever listens on them is stopped (its systemd
	// service is also disabled so it does not come back after a reboot). SSH itself is never touched.
	FreePorts []int
	// RealityDomain, when set, makes the installer build the nginx-SNI scheme on the node: nginx on 443 routing by SNI,
	// a certificate for the domain and a decoy site that REALITY forwards strangers to. The domain must point at the node.
	RealityDomain string
	// OpenPorts are TCP ports the installer opens in ufw for everyone (the connection ports clients use); the agent port
	// is opened by the installer itself, for the panel's address only. Nothing happens when ufw is not active.
	OpenPorts []int
}

// freePortScript stops what listens on a port. Exit codes: 3 — sshd holds it (refused), 4 — still busy afterwards.
func freePortScript(port int) string {
	return fmt.Sprintf(`port=%d
listeners() { ss -H -ltnp "sport = :$port" 2>/dev/null; }
pids=$(listeners | grep -o 'pid=[0-9]*' | cut -d= -f2 | sort -u)
if [ -z "$pids" ]; then echo "Порт $port уже свободен"; exit 0; fi
for pid in $pids; do
  name=$(cat /proc/$pid/comm 2>/dev/null || echo "?")
  if [ "$name" = sshd ]; then echo "Порт $port занят sshd: его освобождать нельзя, доступ к серверу пропадёт. Выберите другой порт"; exit 3; fi
done
for pid in $pids; do
  [ -d /proc/$pid ] || continue # already gone: it was a worker of a service stopped a moment ago
  name=$(cat /proc/$pid/comm 2>/dev/null || echo "?")
  unit=$(sed -n 's|.*/\([^/]*\.service\)$|\1|p' /proc/$pid/cgroup 2>/dev/null | head -n 1)
  if [ -n "$unit" ]; then
    echo "Порт $port занят: $name (служба $unit). Останавливаю и отключаю автозапуск службы"
    systemctl disable --now "$unit" >/dev/null 2>&1 || systemctl stop "$unit" >/dev/null 2>&1 || true
  else
    echo "Порт $port занят: $name (процесс $pid, не служба). Завершаю процесс"
    kill "$pid" 2>/dev/null || true
  fi
done
for i in 1 2 3 4 5 6 7 8; do
  [ -z "$(listeners)" ] && { echo "Порт $port свободен"; exit 0; }
  sleep 1
done
echo "Порт всё ещё занят, завершаю процессы принудительно"
for pid in $pids; do kill -9 "$pid" 2>/dev/null || true; done
sleep 1
if [ -n "$(listeners)" ]; then echo "Не удалось освободить порт $port"; exit 4; fi
echo "Порт $port свободен"
`, port)
}

// Result is what a successful installation leaves for the panel to remember.
type Result struct {
	Fingerprint string // SHA-256 (hex) of the agent's certificate
	HostKey     string // SSH host key seen
	PrivateKey  string // PEM, empty when key login could not be set up
}

var (
	tokenRe       = regexp.MustCompile(`^[A-Za-z0-9]{32,128}$`)
	hostRe        = regexp.MustCompile(`^[A-Za-z0-9.:-]{1,253}$`)
	fingerprintRe = regexp.MustCompile(`Отпечаток сертификата:\s*([0-9a-f]{64})`)
)

// Install runs the whole installation; progress lines go to log.
func Install(ctx context.Context, o Options, log func(string)) (*Result, error) {
	if !tokenRe.MatchString(o.Token) {
		return nil, errors.New("внутренняя ошибка: токен агента имеет неверный вид")
	}
	if !hostRe.MatchString(o.CertHost) || o.AgentPort < 1 || o.AgentPort > 65535 {
		return nil, errors.New("внутренняя ошибка: неверный адрес или порт агента")
	}
	if o.RealityDomain != "" && !ValidDomain(o.RealityDomain) {
		return nil, errors.New("внутренняя ошибка: неверный домен REALITY")
	}
	log("Подключаюсь к " + o.Host + " по SSH…")
	c, err := Dial(ctx, o.Target)
	if err != nil {
		return nil, err
	}
	defer func() { c.Close() }()
	res := &Result{HostKey: c.HostKey}
	log("SSH-ключ сервера: " + c.HostKey[:16] + "…")

	// A key of our own for later maintenance (upgrades); the password is not kept anywhere.
	if priv, line, err := GenerateKey(o.KeyName); err != nil {
		log("Ключ создать не удалось, обслуживание по SSH будет недоступно: " + err.Error())
	} else if script, err := installKeyScript(line); err != nil {
		log("Ключ создать не удалось: " + err.Error())
	} else if out, code, err := c.output(ctx, "sh -s", strings.NewReader(script)); err != nil || code != 0 {
		log("Ключ на сервер записать не удалось, обслуживание по SSH будет недоступно: " + strings.TrimSpace(out))
	} else if kc, err := Dial(ctx, Target{Host: o.Host, Port: o.Port, User: o.User, PrivateKey: priv, HostKey: c.HostKey}); err != nil {
		log("Вход по новому ключу не сработал (на сервере отключён вход по ключам?), оставляю доступ только на время установки")
	} else {
		log("Создан SSH-ключ для панели, вход по ключу проверен. Пароль нигде не сохраняется.")
		c.Close()
		c, res.PrivateKey = kc, priv
	}

	defer func() {
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), dialTimeout)
		defer cancel()
		_, _, _ = c.output(bg, "rm -rf "+remoteDir, nil)
	}()
	if err := upload(ctx, c, o.Binary, log); err != nil {
		return nil, err
	}

	if err := freePorts(ctx, c, o.Port, o.FreePorts, log); err != nil {
		return nil, err
	}

	log("Запускаю установщик на сервере (загрузка Xray может занять минуту)…")
	lw := &lineWriter{fn: log}
	run := fmt.Sprintf("set -e\ncd %s\nexport ANYPANEL_AGENT_TOKEN='%s'\nexport ANYPANEL_OPEN_PORTS='%s'\nexport ANYPANEL_REALITY_DOMAIN='%s'\nbash install-node.sh --port %d --host '%s' --yes </dev/null\n", remoteDir, o.Token, portList(o.OpenPorts), o.RealityDomain, o.AgentPort, o.CertHost)
	code, err := c.Run(ctx, "sh -s", strings.NewReader(run), lw)
	lw.flush()
	if err != nil {
		return nil, fmt.Errorf("установщик прерван: %w", err)
	}
	if code != 0 {
		return nil, fmt.Errorf("установщик завершился с ошибкой (код %d), подробности выше", code)
	}
	m := fingerprintRe.FindStringSubmatch(lw.all.String())
	if m == nil {
		return nil, errors.New("установщик не напечатал отпечаток сертификата агента")
	}
	res.Fingerprint = m[1]
	return res, nil
}

// Upgrade replaces the agent binary on a node the panel can log in to.
func Upgrade(ctx context.Context, t Target, binary string, log func(string)) error {
	log("Подключаюсь к " + t.Host + " по SSH…")
	c, err := Dial(ctx, t)
	if err != nil {
		return err
	}
	defer c.Close()
	defer func() {
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), dialTimeout)
		defer cancel()
		_, _, _ = c.output(bg, "rm -rf "+remoteDir, nil)
	}()
	if err := upload(ctx, c, binary, log); err != nil {
		return err
	}
	lw := &lineWriter{fn: log}
	code, err := c.Run(ctx, "sh -s", strings.NewReader("set -e\ncd "+remoteDir+"\nbash install-node.sh upgrade </dev/null\n"), lw)
	lw.flush()
	if err != nil {
		return fmt.Errorf("обновление прервано: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("обновление завершилось с ошибкой (код %d), подробности выше", code)
	}
	return nil
}

func upload(ctx context.Context, c *Conn, binary string, log func(string)) error {
	f, err := os.Open(binary)
	if err != nil {
		return fmt.Errorf("не удалось открыть файл панели для загрузки: %w", err)
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil {
		log(fmt.Sprintf("Загружаю на сервер агент (%.1f МБ)…", float64(st.Size())/(1<<20)))
	}
	prep := "set -e; rm -rf " + remoteDir + "; umask 077; mkdir -p " + remoteDir
	if out, code, err := c.output(ctx, prep, nil); err != nil || code != 0 {
		return fmt.Errorf("не удалось создать каталог на сервере: %v %s", err, strings.TrimSpace(out))
	}
	if out, code, err := c.output(ctx, "cat > "+remoteDir+"/anypanel && chmod 755 "+remoteDir+"/anypanel", io.Reader(f)); err != nil || code != 0 {
		return fmt.Errorf("загрузка агента не удалась: %v %s", err, strings.TrimSpace(out))
	}
	if out, code, err := c.output(ctx, "cat > "+remoteDir+"/install-node.sh", bytes.NewReader(Script())); err != nil || code != 0 {
		return fmt.Errorf("загрузка установщика не удалась: %v %s", err, strings.TrimSpace(out))
	}
	return nil
}

// portList renders valid ports as "443,8443" for the installer's environment; anything out of range is dropped.
func portList(ports []int) string {
	var out []string
	for _, p := range ports {
		if p >= 1 && p <= 65535 {
			out = append(out, strconv.Itoa(p))
		}
	}
	return strings.Join(out, ",")
}

// openFirewallScript opens the agent port for the panel and the connection ports for everybody in ufw, when ufw is on.
func openFirewallScript(agentPort int, ports []int) string {
	return fmt.Sprintf(`agent=%d
ports="%s"
if ! command -v ufw >/dev/null 2>&1 || ! ufw status 2>/dev/null | grep -q "Status: active"; then
  echo "На сервере нет включённого ufw, открывать порты в нём не нужно. Если связи всё равно нет, откройте порты в файрволе провайдера"
  exit 0
fi
src="${SSH_CLIENT:-}"; src="${src%%%% *}"
case "$src" in ""|*[!0-9a-fA-F:.]*) src="" ;; esac
if [ -n "$src" ]; then
  echo "ufw: открываю порт агента $agent/tcp для панели ($src)"
  ufw allow from "$src" to any port "$agent" proto tcp >/dev/null || exit 5
else
  echo "ufw: открываю порт агента $agent/tcp"
  ufw allow "$agent/tcp" >/dev/null || exit 5
fi
for p in $ports; do
  echo "ufw: открываю порт подключений $p/tcp"
  ufw allow "$p/tcp" >/dev/null || exit 5
done
echo "Порты открыты"
`, agentPort, portList(ports))
}

// OpenFirewall logs in (with the key the panel keeps for the node) and opens the agent and connection ports in ufw.
func OpenFirewall(ctx context.Context, t Target, agentPort int, ports []int, log func(string)) error {
	if agentPort < 1 || agentPort > 65535 {
		return errors.New("внутренняя ошибка: неверный порт агента")
	}
	log("Подключаюсь к " + t.Host + " по SSH…")
	c, err := Dial(ctx, t)
	if err != nil {
		return err
	}
	defer c.Close()
	lw := &lineWriter{fn: log}
	code, err := c.Run(ctx, "sh -s", strings.NewReader(openFirewallScript(agentPort, ports)), lw)
	lw.flush()
	if err != nil {
		return fmt.Errorf("не удалось открыть порты: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("открыть порты не удалось (код %d), подробности выше", code)
	}
	return nil
}

// freePorts stops whatever listens on the ports (see freePortScript). sshPort is never freed.
func freePorts(ctx context.Context, c *Conn, sshPort int, ports []int, log func(string)) error {
	for _, p := range ports {
		if p == sshPort {
			return errors.New("нельзя освобождать порт, по которому панель подключается к серверу по SSH")
		}
		log(fmt.Sprintf("Освобождаю порт %d принудительно…", p))
		fw := &lineWriter{fn: log}
		code, err := c.Run(ctx, "sh -s", strings.NewReader(freePortScript(p)), fw)
		fw.flush()
		if err != nil {
			return fmt.Errorf("освободить порт не удалось: %w", err)
		}
		if code != 0 {
			return fmt.Errorf("порт %d не освобождён (код %d), установка остановлена", p, code)
		}
	}
	return nil
}

// SetupReality builds the nginx-SNI scheme (nginx + certificate + decoy site) on a node that already has the agent:
// the same step the installer does for a new node. freePorts are cleared first when asked for.
func SetupReality(ctx context.Context, t Target, domain string, freePortList []int, log func(string)) error {
	if !ValidDomain(domain) {
		return errors.New("неверный домен REALITY")
	}
	log("Подключаюсь к " + t.Host + " по SSH…")
	c, err := Dial(ctx, t)
	if err != nil {
		return err
	}
	defer c.Close()
	defer func() {
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), dialTimeout)
		defer cancel()
		_, _, _ = c.output(bg, "rm -rf "+remoteDir, nil)
	}()
	if out, code, err := c.output(ctx, "set -e; rm -rf "+remoteDir+"; umask 077; mkdir -p "+remoteDir, nil); err != nil || code != 0 {
		return fmt.Errorf("не удалось создать каталог на сервере: %v %s", err, strings.TrimSpace(out))
	}
	if out, code, err := c.output(ctx, "cat > "+remoteDir+"/install-node.sh", bytes.NewReader(Script())); err != nil || code != 0 {
		return fmt.Errorf("загрузка установщика не удалась: %v %s", err, strings.TrimSpace(out))
	}
	if err := freePorts(ctx, c, t.Port, freePortList, log); err != nil {
		return err
	}
	log("Настраиваю REALITY-сайт (nginx, сертификат, заглушка)…")
	lw := &lineWriter{fn: log}
	run := fmt.Sprintf("set -e\ncd %s\nexport ANYPANEL_OPEN_PORTS='%s'\nbash install-node.sh reality --reality-domain '%s' --yes </dev/null\n", remoteDir, "", domain)
	code, err := c.Run(ctx, "sh -s", strings.NewReader(run), lw)
	lw.flush()
	if err != nil {
		return fmt.Errorf("установщик прерван: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("настройка REALITY-сайта завершилась с ошибкой (код %d), подробности выше", code)
	}
	return nil
}
