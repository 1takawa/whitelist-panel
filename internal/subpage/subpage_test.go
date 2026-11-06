package subpage

import (
	"strings"
	"testing"
	"time"
)

func TestExampleRenders(t *testing.T) {
	if err := Validate(Example()); err != nil {
		t.Fatal(err)
	}
	if e := Example(); e.CSS == "" || e.JS == "" || strings.Contains(e.HTML, "<style") || strings.Contains(e.HTML, "<script") {
		t.Fatal("the example keeps its styles and script apart from the HTML")
	}
	out, err := Render(Example(), Sample())
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"demo@example.com", "Активна", "<svg", "happ://add/https://sub.example.com/sub/demo", "Германия", "12.0 ГБ"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestStylesAndScriptAreAddedToThePage(t *testing.T) {
	d := Data(Input{Name: "alice", Status: "active"})
	out, err := Render(Page{HTML: "<!doctype html><HEAD><title>t</title></HEAD><body><p>{{.name}}</p></body>", CSS: "p{color:red}</style><b>", JS: "console.log(sub.name);</script><b>"}, d)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	head, body := strings.Index(s, "</HEAD>"), strings.Index(s, "</body>")
	style, script := strings.Index(s, "<style>"), strings.Index(s, "<script>")
	if style < 0 || style > head || script < head || script > body {
		t.Fatalf("styles go into the head, the script at the end of the body:\n%s", s)
	}
	if strings.Count(s, "</style>") != 1 || strings.Count(s, "</script>") != 1 || !strings.Contains(s, "window.sub = {") || !strings.Contains(s, `"name":"alice"`) {
		t.Fatalf("typed closing tags must not end the elements, the script gets the subscription:\n%s", s)
	}

	// A page without head or body still gets both.
	bare, err := Render(Page{HTML: "<p>x</p>", CSS: "p{}", JS: "1"}, d)
	if err != nil || !strings.HasSuffix(strings.TrimSpace(string(bare)), "</script>") || !strings.Contains(string(bare), "<style>") {
		t.Fatalf("%v\n%s", err, bare)
	}
	// Nothing is added when there is nothing to add.
	if plain, _ := Render(Page{HTML: "<p>x</p>"}, d); strings.Contains(string(plain), "<style") || strings.Contains(string(plain), "<script") {
		t.Fatalf("%s", plain)
	}
}

func TestValuesAreEscapedForTheirPlace(t *testing.T) {
	in := Input{Name: `<img src=x onerror=alert(1)>`, Title: "t", Status: "active", Announce: `"><script>alert(2)</script>`,
		URL: "https://s.example/sub/x?a=1&b=2", Links: []Link{{Remark: "</script><script>alert(3)</script>"}}}
	out, err := Render(Page{HTML: `<p>{{.name}}</p><a title="{{.announce}}" href="{{.url}}">x</a><script>var s = {{.json}};</script>`}, Data(in))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "<img") || strings.Contains(s, "<script>alert") || strings.Count(s, "<script>") != 1 || strings.Count(s, "</script>") != 1 {
		t.Fatalf("a value broke out of its place:\n%s", s)
	}
}

func TestDataFields(t *testing.T) {
	end := time.Date(2030, 1, 10, 12, 0, 0, 0, time.UTC)
	now := time.Date(2030, 1, 7, 18, 0, 0, 0, time.UTC)
	d := Data(Input{Name: "u", Status: "exhausted", Up: 1 << 30, Down: 1 << 30, Total: 4 << 30, ExpiryAt: &end, Now: now})
	if d["percent"] != 50 || d["leftText"] != "2.0 ГБ" || d["totalText"] != "4.0 ГБ" || d["daysLeft"] != 3 || d["statusLabel"] != "Трафик исчерпан" || d["statusTone"] != "warn" || d["unlimited"] != false {
		t.Fatalf("%v", d)
	}
	free := Data(Input{Name: "u", Status: "active", Now: now})
	if free["totalText"] != "∞" || free["expiryText"] != "Бессрочно" || free["daysLeft"] != -1 || free["unlimited"] != true || free["hasUrl"] != false {
		t.Fatalf("%v", free)
	}
	if _, err := Render(Page{HTML: strings.Repeat("x", MaxSize+1)}, free); err == nil {
		t.Fatal("a page over the limit must be refused")
	}
	if _, err := Render(Page{HTML: "<p>x</p>", JS: strings.Repeat("x", MaxSize+1)}, free); err == nil {
		t.Fatal("a script over the limit must be refused")
	}
	if err := Validate(Page{HTML: "{{range}}"}); err == nil {
		t.Fatal("a broken template must be refused")
	}
}

func TestHiddenPoolPresetRendersWithItsFields(t *testing.T) {
	pr, ok := FindPreset("hidden-pool")
	if !ok || pr.Tag != "Спонсор" || len(pr.Fields) != 3 {
		t.Fatalf("%+v", pr)
	}
	vals := pr.Values(nil)
	if vals["telegram"] != "https://t.me/HiddenPoolVPNBot" || vals["support"] != "https://t.me/HiddenPool_supp" || vals["site"] != "" {
		t.Fatalf("the preset starts with its own values: %v", vals)
	}
	out, err := Render(pr.Page(), SampleWith(vals))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"demo@example.com", "https://t.me/HiddenPoolVPNBot", "https://t.me/HiddenPool_supp", "happ://add/https://sub.example.com/sub/demo", "/api/theme-assets/hidden-pool/banner.jpg", "Доступ <em>открыт</em>"} {
		if !strings.Contains(s, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if strings.Contains(s, ">Сайт<") {
		t.Error("an empty site field hides the link")
	}
	// A saved value wins, an empty saved value hides the link, and a value never saved falls back to the preset's.
	vals = pr.Values(map[string]string{"site": "https://example.org", "telegram": ""})
	out, _ = Render(pr.Page(), SampleWith(vals))
	s = string(out)
	if !strings.Contains(s, `href="https://example.org"`) || strings.Contains(s, ">Telegram<") || !strings.Contains(s, "HiddenPool_supp") {
		t.Fatalf("%s", s)
	}
	// With no support field the support link of the subscription settings is used.
	vals["support"] = ""
	out, _ = Render(pr.Page(), SampleWith(vals))
	if !strings.Contains(string(out), `href="https://t.me/example"`) {
		t.Fatal("the support address of the settings is the fallback")
	}
}

func TestPresetFieldsAreNormalized(t *testing.T) {
	pr, _ := FindPreset("hidden-pool")
	for in, want := range map[string]string{"@name": "https://t.me/name", " https://t.me/x ": "https://t.me/x", "example.com/a": "https://example.com/a", "": ""} {
		if got, err := pr.NormalizeField("telegram", in); err != nil || got != want {
			t.Errorf("%q -> %q, %v", in, got, err)
		}
	}
	if got, _ := pr.NormalizeField("site", "@name"); got == "https://t.me/name" {
		t.Error("a site is not a Telegram name")
	}
	for _, bad := range []string{"javascript:alert(1)", "ftp://x.example", "a b", "http://localhost", strings.Repeat("a", 301) + ".com"} {
		if _, err := pr.NormalizeField("support", bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	if _, err := pr.NormalizeField("nope", "https://a.example"); err == nil {
		t.Error("an unknown field is refused")
	}
}

func TestPresetAssetsAreServedOnlyForRegisteredFiles(t *testing.T) {
	if b, ct, ok := Asset("hidden-pool", "banner.jpg"); !ok || ct != "image/jpeg" || len(b) < 1000 {
		t.Fatal("the banner is part of the program")
	}
	if _, ct, ok := Asset("hidden-pool", "mark.png"); !ok || ct != "image/png" {
		t.Fatal("the mark")
	}
	for _, c := range [][2]string{{"hidden-pool", "page.html"}, {"hidden-pool", "../presets.go"}, {"hidden-pool", "a/b.png"}, {"nope", "mark.png"}, {"hidden-pool", "nothing.png"}} {
		if _, _, ok := Asset(c[0], c[1]); ok {
			t.Errorf("%v must not be served", c)
		}
	}
}
