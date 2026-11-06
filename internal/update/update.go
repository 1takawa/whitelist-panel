// Package update lets the panel update itself from the releases of its GitHub repository: it looks for a newer
// release, downloads the archive, checks its SHA-256 and that the binary inside runs and reports the right version, and
// then hands the installation to the installer's own "upgrade" (which keeps the previous version and puts it back when
// the new one does not come up). The installer runs in a unit of its own, because it restarts the panel that started it.
package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultRepo is where the releases live.
	DefaultRepo = "1takawa/whitelist-panel"

	asset      = "whitelist-panel-linux-amd64.tar.gz"
	cacheTTL   = 10 * time.Minute
	minRefresh = 20 * time.Second
	maxArchive = 200 << 20
	maxFile    = 100 << 20
	unitName   = "wl-selfupdate"
)

var (
	tagRe = regexp.MustCompile(`^v\d{1,4}\.\d{1,4}\.\d{1,4}$`)
	// The files a release archive must hold, and nothing else is unpacked.
	wanted = []string{"anypanel", "install.sh", "install-node.sh"}
)

// Release is one published version.
type Release struct {
	Tag       string    `json:"tag"`
	Version   string    `json:"version"`
	Published time.Time `json:"published"`
	Notes     string    `json:"notes"`
	URL       string    `json:"url"`
}

// State is what the update is doing now, for the settings page.
type State struct {
	State   string    `json:"state"` // idle | downloading | verifying | installing | failed
	Message string    `json:"message"`
	Version string    `json:"version"`
	Started time.Time `json:"started"`
}

// Updater finds and installs new releases.
type Updater struct {
	Repo         string // owner/name; "" = DefaultRepo
	APIBase      string // "" = https://api.github.com (replaced in tests)
	DownloadBase string // "" = https://github.com (replaced in tests)
	HTTP         *http.Client
	Dir          string // where archives are unpacked and the installer's log goes
	// Run starts a program and returns its output; nil runs it for real (replaced in tests).
	Run func(ctx context.Context, name string, args ...string) ([]byte, error)
	Now func() time.Time

	mu     sync.Mutex
	cached *Release
	at     time.Time
	state  State
}

func (u *Updater) repo() string {
	if u.Repo != "" {
		return u.Repo
	}
	return DefaultRepo
}

func (u *Updater) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

func (u *Updater) client() *http.Client {
	if u.HTTP != nil {
		return u.HTTP
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

func (u *Updater) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if u.Run != nil {
		return u.Run(ctx, name, args...)
	}
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// ParseVersion reads "0.32.1" or "v0.32.1" (anything after a "-" or "+" is ignored).
func ParseVersion(s string) ([3]int, bool) {
	var v [3]int
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if s == "" || len(parts) > 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// Newer reports whether latest is a higher version than current. Versions that cannot be read (a development build) are
// never "older".
func Newer(current, latest string) bool {
	c, ok1 := ParseVersion(current)
	l, ok2 := ParseVersion(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := range c {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// Latest is the newest published release. The answer is kept for ten minutes; refresh asks again (at most every 20 s).
func (u *Updater) Latest(ctx context.Context, refresh bool) (*Release, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	ttl := cacheTTL
	if refresh {
		ttl = minRefresh
	}
	if u.cached != nil && u.now().Sub(u.at) < ttl {
		return u.cached, nil
	}
	base := u.APIBase
	if base == "" {
		base = "https://api.github.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/repos/"+u.repo()+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "whitelist-panel")
	res, err := u.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub недоступен: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub ответил %d: релизов не найдено или превышен лимит запросов", res.StatusCode)
	}
	var j struct {
		Tag       string    `json:"tag_name"`
		Body      string    `json:"body"`
		URL       string    `json:"html_url"`
		Published time.Time `json:"published_at"`
		Draft     bool      `json:"draft"`
		Pre       bool      `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&j); err != nil {
		return nil, fmt.Errorf("ответ GitHub не разобран: %w", err)
	}
	if j.Draft || j.Pre || !tagRe.MatchString(j.Tag) {
		return nil, fmt.Errorf("последний релиз (%q) не подходит для установки", j.Tag)
	}
	notes := strings.TrimSpace(j.Body)
	if len(notes) > 2000 {
		notes = notes[:2000] + "…"
	}
	u.cached = &Release{Tag: j.Tag, Version: strings.TrimPrefix(j.Tag, "v"), Published: j.Published, Notes: notes, URL: j.URL}
	u.at = u.now()
	return u.cached, nil
}

// Status is what the update is doing now.
func (u *Updater) Status() State {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.state.State == "" {
		return State{State: "idle"}
	}
	return u.state
}

func (u *Updater) set(state, msg string) {
	u.mu.Lock()
	u.state.State, u.state.Message = state, msg
	u.mu.Unlock()
}

// LogTail is the end of the installer's log of the last update.
func (u *Updater) LogTail(lines int) string {
	b, err := os.ReadFile(filepath.Join(u.Dir, "last.log"))
	if err != nil {
		return ""
	}
	all := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n")
}

// Apply installs the release in the background: the caller polls Status, and then the panel itself goes away for a
// moment when the installer restarts it. Only one update runs at a time.
func (u *Updater) Apply(rel *Release) error {
	// The directory goes into the installer's command line inside single quotes.
	if u.Dir == "" || strings.ContainsAny(u.Dir, "'\n\r\x00") {
		return errors.New("каталог для обновления не задан")
	}
	if !tagRe.MatchString(rel.Tag) {
		return errors.New("неверный тег релиза")
	}
	u.mu.Lock()
	if s := u.state.State; s == "downloading" || s == "verifying" || s == "installing" {
		u.mu.Unlock()
		return errors.New("обновление уже идёт")
	}
	u.state = State{State: "downloading", Message: "Скачиваю " + rel.Tag, Version: rel.Version, Started: u.now()}
	u.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := u.install(ctx, rel); err != nil {
			u.set("failed", err.Error())
		}
	}()
	return nil
}

func (u *Updater) install(ctx context.Context, rel *Release) error {
	dir := filepath.Join(u.Dir, rel.Tag)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	pkg := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		return err
	}
	base := u.DownloadBase
	if base == "" {
		base = "https://github.com"
	}
	url := base + "/" + u.repo() + "/releases/download/" + rel.Tag + "/" + asset
	archive := filepath.Join(dir, asset)
	sum, err := u.download(ctx, url, archive)
	if err != nil {
		return err
	}

	u.set("verifying", "Проверяю контрольную сумму")
	want, err := u.fetchSum(ctx, url+".sha256")
	if err != nil {
		return err
	}
	if sum != want {
		return errors.New("контрольная сумма архива не совпала: обновление отменено")
	}
	if err := unpack(archive, pkg); err != nil {
		return err
	}
	out, err := u.run(ctx, filepath.Join(pkg, "anypanel"), "version")
	if err != nil {
		return fmt.Errorf("новый бинарник не запускается: %w", err)
	}
	if got := strings.TrimSpace(string(out)); got != rel.Version {
		return fmt.Errorf("в архиве версия %q, а ожидалась %q: обновление отменено", got, rel.Version)
	}

	u.set("installing", "Устанавливаю: панель перезапустится")
	logPath := filepath.Join(u.Dir, "last.log")
	script := "cd '" + pkg + "' && bash install.sh upgrade > '" + logPath + "' 2>&1"
	if out, err := u.run(ctx, "systemd-run", "--unit="+unitName, "--collect", "--quiet", "bash", "-c", script); err != nil {
		return fmt.Errorf("не удалось запустить установщик (нужен systemd): %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (u *Updater) download(ctx context.Context, url, to string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "whitelist-panel")
	res, err := u.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("не удалось скачать релиз: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("не удалось скачать релиз: ответ %d", res.StatusCode)
	}
	f, err := os.Create(to)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(res.Body, maxArchive+1))
	if err != nil {
		return "", err
	}
	if n > maxArchive {
		return "", errors.New("архив слишком большой")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (u *Updater) fetchSum(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "whitelist-panel")
	res, err := u.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("не удалось скачать контрольную сумму: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("контрольной суммы релиза нет (ответ %d): обновление отменено", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 4096))
	if err != nil {
		return "", err
	}
	f := strings.Fields(string(b))
	if len(f) == 0 || len(f[0]) != 64 {
		return "", errors.New("файл контрольной суммы не разобран")
	}
	return strings.ToLower(f[0]), nil
}

// unpack takes the three files of a release out of the archive and nothing else: no directories, no links, no paths.
func unpack(archive, to string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("архив не открывается: %w", err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	got := map[string]bool{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("архив повреждён: %w", err)
		}
		name := path.Clean(strings.TrimPrefix(h.Name, "./"))
		ok := false
		for _, w := range wanted {
			ok = ok || name == w
		}
		if !ok || h.Typeflag != tar.TypeReg {
			return fmt.Errorf("в архиве лишний файл %q: обновление отменено", h.Name)
		}
		out, err := os.OpenFile(filepath.Join(to, name), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		n, err := io.Copy(out, io.LimitReader(tr, maxFile+1))
		out.Close()
		if err != nil {
			return err
		}
		if n > maxFile {
			return errors.New("файл в архиве слишком большой")
		}
		got[name] = true
	}
	for _, w := range wanted {
		if !got[w] {
			return fmt.Errorf("в архиве нет файла %s", w)
		}
	}
	return nil
}
