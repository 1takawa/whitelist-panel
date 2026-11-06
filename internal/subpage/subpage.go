// Package subpage renders the page a subscriber sees at the subscription address when the administrator wrote the page
// themselves: an HTML document that is a Go html/template (the values are escaped for the place they are put in) and
// gets the subscription as data.
package subpage

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"regexp"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

// MaxSize is the longest page the settings accept.
const MaxSize = 256 << 10

//go:embed example.html
var exampleHTML string

//go:embed example.css
var exampleCSS string

//go:embed example.js
var exampleJS string

// Page is a page of the administrator's own design: the HTML (a template), its styles and its script. The styles and the
// script are put into the page as they are; the script finds the subscription in window.sub.
type Page struct {
	HTML, CSS, JS string
}

// Example is a complete page to start from: the same fields the standard page shows.
func Example() Page { return Page{HTML: exampleHTML, CSS: exampleCSS, JS: exampleJS} }

// Link is one connection of the subscriber.
type Link struct {
	Remark, Name, Protocol, Network, Security, Link string
}

// Input is what the page is about.
type Input struct {
	Name, Title, Status                   string
	Up, Down, Total                       int64
	ExpiryAt                              *time.Time
	URL, SupportURL, ProfileURL, Announce string
	Links                                 []Link
	Now                                   time.Time
}

type app struct {
	name, platforms string
	open            func(url string) string
}

// Apps that take a subscription; the first three can add it with one tap, INCY only by copying the link.
var apps = []app{
	{"Happ", "iOS · Android · macOS · Windows", func(u string) string { return "happ://add/" + u }},
	{"INCY", "iOS · Android · macOS", nil},
	{"Hiddify", "Android · iOS · Windows · macOS · Linux", func(u string) string { return "hiddify://import/" + u }},
	{"v2rayTun", "iOS · Android · Windows · macOS", func(u string) string { return "v2raytun://import/" + u }},
}

var statusText = map[string][2]string{
	"active":    {"Активна", "ok"},
	"disabled":  {"Отключена", "neutral"},
	"expired":   {"Срок истёк", "warn"},
	"exhausted": {"Трафик исчерпан", "warn"},
}

// Data turns the subscription into the values the template can use.
func Data(in Input) map[string]any {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	st, ok := statusText[in.Status]
	if !ok {
		st = [2]string{in.Status, "neutral"}
	}
	used := in.Up + in.Down
	percent := 0
	left := ""
	if in.Total > 0 {
		percent = int(min(100, used*100/in.Total))
		left = bytesText(max(0, in.Total-used))
	}
	totalText := "∞"
	if in.Total > 0 {
		totalText = bytesText(in.Total)
	}
	expiryAt, expiryText, daysLeft := "", "Бессрочно", -1
	if in.ExpiryAt != nil {
		expiryAt = in.ExpiryAt.UTC().Format(time.RFC3339)
		expiryText = in.ExpiryAt.Format("02.01.2006")
		if d := in.ExpiryAt.Sub(now); d > 0 {
			daysLeft = int((d + 24*time.Hour - 1) / (24 * time.Hour)) // started days count, as on the standard page
		} else {
			daysLeft = 0
		}
	}

	links := make([]map[string]any, 0, len(in.Links))
	for _, l := range in.Links {
		links = append(links, map[string]any{"remark": l.Remark, "name": l.Name, "protocol": l.Protocol, "network": l.Network, "security": l.Security, "link": l.Link})
	}
	appList := make([]map[string]any, 0, len(apps))
	for _, a := range apps {
		m := map[string]any{"name": a.name, "platforms": a.platforms, "openUrl": template.URL(""), "copyOnly": a.open == nil}
		if a.open != nil && in.URL != "" {
			m["openUrl"] = template.URL(a.open(in.URL)) // an app link has its own scheme, which the escaper would otherwise refuse
		}
		appList = append(appList, m)
	}

	d := map[string]any{
		"name": in.Name, "title": in.Title, "status": in.Status, "statusLabel": st[0], "statusTone": st[1],
		"up": in.Up, "down": in.Down, "used": used, "total": in.Total, "unlimited": in.Total <= 0,
		"usedText": bytesText(used), "totalText": totalText, "leftText": left, "percent": percent,
		"expiryAt": expiryAt, "expiryText": expiryText, "daysLeft": daysLeft, "hasExpiry": in.ExpiryAt != nil,
		"url": in.URL, "hasUrl": in.URL != "", "supportUrl": in.SupportURL, "profileUrl": in.ProfileURL, "announce": in.Announce,
		"links": links, "apps": appList, "qrSvg": template.HTML(qrSVG(in.URL)),
	}
	// The same values for a script: {{.json}} inside <script> is a ready object, e.g. const sub = {{.json}};
	pub := map[string]any{}
	for _, k := range []string{"name", "title", "status", "statusLabel", "up", "down", "used", "total", "usedText", "totalText", "percent", "expiryAt", "expiryText", "daysLeft", "url", "supportUrl", "profileUrl", "announce", "links"} {
		pub[k] = d[k]
	}
	b, _ := json.Marshal(pub)
	d["json"] = template.JS(escapeJS(b))
	return d
}

// escapeJS makes JSON safe inside a <script> element: nothing in it can close the element or open a comment.
func escapeJS(b []byte) string {
	r := strings.NewReplacer("<", `<`, ">", `>`, "&", `&`, " ", ` `, " ", ` `)
	return r.Replace(string(b))
}

// bytesText is a size the way the standard page writes it: 40.0 ГБ.
func bytesText(n int64) string {
	units := []string{"Б", "КБ", "МБ", "ГБ", "ТБ"}
	v, i := float64(n), 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d Б", n)
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

// qrSVG draws the link as a black-on-white QR code, "" when it does not fit in one.
func qrSVG(text string) string {
	if text == "" {
		return ""
	}
	q, err := qrcode.New(text, qrcode.Medium)
	if err != nil {
		return ""
	}
	q.DisableBorder = true
	bm := q.Bitmap()
	n := len(bm)
	const margin = 2
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges" role="img" aria-label="QR-код"><rect width="100%%" height="100%%" fill="#fff"/><path fill="#000" d="`, n+2*margin, n+2*margin)
	for y, row := range bm {
		for x, on := range row {
			if on {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+margin, y+margin)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String()
}

func parse(p Page) (*template.Template, error) {
	for name, part := range map[string]string{"HTML": p.HTML, "CSS": p.CSS, "JavaScript": p.JS} {
		if len(part) > MaxSize {
			return nil, fmt.Errorf("%s is too long (max %d KB)", name, MaxSize>>10)
		}
	}
	if strings.TrimSpace(p.HTML) == "" {
		return nil, errors.New("the page is empty")
	}
	return template.New("page").Option("missingkey=zero").Parse(p.HTML)
}

// inject puts text before the first closing tag of the given name (any case), or at the end when the page has none.
func inject(page []byte, tag, text string) []byte {
	i := bytes.Index(bytes.ToLower(page), []byte(tag))
	if i < 0 {
		return append(page, text...)
	}
	return append(page[:i:i], append([]byte(text), page[i:]...)...)
}

// closeSafe keeps a closing tag typed inside the text from ending the element early.
func closeSafe(text, tag string) string {
	re := regexp.MustCompile(`(?i)</` + tag)
	return re.ReplaceAllString(text, `<\/`+tag)
}

// Render fills the HTML with the data and adds the styles (before </head>) and the script (before </body>).
func Render(p Page, data map[string]any) ([]byte, error) {
	t, err := parse(p)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	if strings.TrimSpace(p.CSS) != "" {
		out = inject(out, "</head>", "<style>\n"+closeSafe(p.CSS, "style")+"\n</style>\n")
	}
	if strings.TrimSpace(p.JS) != "" {
		pub, _ := data["json"].(template.JS)
		out = inject(out, "</body>", "<script>\nwindow.sub = "+string(pub)+";\n"+closeSafe(p.JS, "script")+"\n</script>\n")
	}
	return out, nil
}

// Sample is a made-up subscription for the preview and for checking a page before it is saved.
func Sample() map[string]any {
	end := time.Now().Add(23 * 24 * time.Hour)
	return Data(Input{
		Name: "demo@example.com", Title: "Whitelist", Status: "active", Up: 3 << 30, Down: 9 << 30, Total: 40 << 30, ExpiryAt: &end,
		URL: "https://sub.example.com/sub/demo", SupportURL: "https://t.me/example", ProfileURL: "https://example.com",
		Announce: "Это пример: так выглядит объявление.",
		Links: []Link{
			{Remark: "🇩🇪 Германия", Name: "Германия", Protocol: "vless", Network: "tcp", Security: "reality", Link: "vless://demo@de.example.com:443#de"},
			{Remark: "🇳🇱 Нидерланды", Name: "Нидерланды", Protocol: "trojan", Network: "ws", Security: "tls", Link: "trojan://demo@nl.example.com:443#nl"},
		},
	})
}

// Validate checks that the page is a template that renders. A page that fails here would show nothing to a subscriber.
func Validate(p Page) error {
	_, err := Render(p, Sample())
	return err
}
