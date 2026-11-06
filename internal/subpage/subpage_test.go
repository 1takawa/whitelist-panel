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

func TestValuesAreEscapedForTheirPlace(t *testing.T) {
	in := Input{Name: `<img src=x onerror=alert(1)>`, Title: "t", Status: "active", Announce: `"><script>alert(2)</script>`,
		URL: "https://s.example/sub/x?a=1&b=2", Links: []Link{{Remark: "</script><script>alert(3)</script>"}}}
	out, err := Render(`<p>{{.name}}</p><a title="{{.announce}}" href="{{.url}}">x</a><script>var s = {{.json}};</script>`, Data(in))
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
	if _, err := Render(strings.Repeat("x", MaxSize+1), free); err == nil {
		t.Fatal("a page over the limit must be refused")
	}
	if err := Validate("{{range}}"); err == nil {
		t.Fatal("a broken template must be refused")
	}
}
