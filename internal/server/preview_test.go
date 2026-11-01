package server

import (
	"net/http"
	"strings"
	"testing"

	"anypanel2/internal/model"
)

const sampleIndex = `<!doctype html>
<html lang="ru">
  <head>
    <meta charset="UTF-8" />
    <meta name="description" content="Whitelist Panel — управление Xray: VLESS и Trojan" />
    <title>Whitelist Panel</title>
  </head>
  <body></body>
</html>
`

func TestPreviewPageDefaultIsUntouched(t *testing.T) {
	if got := string(previewPage([]byte(sampleIndex), model.LinkPreview{Mode: model.PreviewDefault, Title: "x"}, model.Client{}, "https://h.example")); got != sampleIndex {
		t.Fatalf("default mode keeps the page as it is:\n%s", got)
	}
}

func TestPreviewPageCustom(t *testing.T) {
	p := model.LinkPreview{Mode: model.PreviewCustom, SiteName: "Мой VPN", Title: "Подписка {{EMAIL}}", Description: `Быстро & "надёжно" <b>`, ImageURL: "https://example.com/logo.png"}
	got := string(previewPage([]byte(sampleIndex), p, model.Client{Email: "alice"}, "https://h.example"))
	for _, want := range []string{
		"<title>Подписка alice</title>",
		`<meta property="og:title" content="Подписка alice" />`,
		`<meta property="og:site_name" content="Мой VPN" />`,
		`<meta property="og:description" content="Быстро &amp; &#34;надёжно&#34; &lt;b&gt;" />`,
		`<meta name="description" content="Быстро &amp; &#34;надёжно&#34; &lt;b&gt;" />`,
		`<meta property="og:image" content="https://example.com/logo.png" />`,
		`<meta name="twitter:card" content="summary_large_image" />`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "управление Xray") || strings.Count(got, `name="description"`) != 1 || strings.Count(got, "</head>") != 1 || strings.Contains(got, "<b>") {
		t.Fatalf("the old description is gone, nothing is injected unescaped:\n%s", got)
	}

	// Empty fields are left out, so the card shows only what the admin wants.
	bare := string(previewPage([]byte(sampleIndex), model.LinkPreview{Mode: model.PreviewCustom, Title: "Только название"}, model.Client{}, "https://h.example"))
	if strings.Contains(bare, "og:description") || strings.Contains(bare, "og:image") || strings.Contains(bare, "og:site_name") || strings.Contains(bare, `name="description"`) ||
		!strings.Contains(bare, `<meta property="og:title" content="Только название" />`) {
		t.Fatalf("%s", bare)
	}
}

func TestSubscriptionLinkPreviewSetting(t *testing.T) {
	h := newHandler(t)
	subID, _ := subFixture(t, h, "")
	c := loggedIn(t, h)
	browser := map[string]string{"Accept": "text/html"}

	putSettings(t, h, c, `{"preview":{"mode":"custom","siteName":"Мой VPN","title":"Подписка для {{EMAIL}}","description":"Откройте ссылку"}}`)
	page := do(h, http.MethodGet, "/sub/"+subID, "", nil, browser).Body.String()
	if !strings.Contains(page, `og:site_name" content="Мой VPN"`) || !strings.Contains(page, "<title>Подписка для alice</title>") || strings.Contains(page, "управление Xray") {
		t.Fatalf("a messenger sees the chosen card:\n%s", page)
	}
	// A made-up id gets the plain page: the preview must not tell anything about subscriptions that do not exist.
	if other := do(h, http.MethodGet, "/sub/no-such-subscription", "", nil, browser).Body.String(); strings.Contains(other, "Мой VPN") {
		t.Fatalf("unknown ids get the default page:\n%s", other)
	}

	for name, body := range map[string]string{
		"bad mode":    `{"preview":{"mode":"fancy"}}`,
		"bad image":   `{"preview":{"mode":"custom","imageUrl":"javascript:alert(1)"}}`,
		"long title":  `{"preview":{"mode":"custom","title":"` + strings.Repeat("я", 101) + `"}}`,
		"long descr.": `{"preview":{"mode":"custom","description":"` + strings.Repeat("я", 301) + `"}}`,
	} {
		if rec := do(h, http.MethodPut, "/api/settings", `{"panelDomain":"panel.example.com","subscription":`+body+`}`, c, nil); rec.Code != 400 {
			t.Errorf("%s must be refused: %d %s", name, rec.Code, rec.Body)
		}
	}
	putSettings(t, h, c, `{"preview":{"mode":"default"}}`)
	if page := do(h, http.MethodGet, "/sub/"+subID, "", nil, browser).Body.String(); !strings.Contains(page, "<title>Whitelist Panel</title>") {
		t.Fatalf("back to the default:\n%s", page)
	}
}

// A one-pixel PNG.
var tinyPNG = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89,
	0, 0, 0, 0x0d, 'I', 'D', 'A', 'T', 0x78, 0x9c, 0x63, 0xf8, 0xff, 0xff, 0x3f, 0, 5, 0xfe, 2, 0xfe, 0xa7, 0x35, 0x81, 0x84, 0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82}

func TestPreviewImageUploadServeAndCard(t *testing.T) {
	h := newHandler(t)
	subID, _ := subFixture(t, h, "")
	c := loggedIn(t, h)
	hdr := map[string]string{"Content-Type": "application/octet-stream"}

	if rec := do(h, http.MethodGet, "/api/sub-preview", "", nil, nil); rec.Code != 404 {
		t.Fatalf("no picture yet: %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/api/settings/preview-image", string(tinyPNG), nil, hdr); rec.Code != 401 {
		t.Fatalf("only a logged-in admin uploads: %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/api/settings/preview-image", "<script>alert(1)</script>", c, hdr); rec.Code != 400 {
		t.Fatalf("not a picture: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodPost, "/api/settings/preview-image", string(tinyPNG)+strings.Repeat("x", 2<<20), c, hdr); rec.Code == 200 {
		t.Fatalf("too big must be refused")
	}
	rec := do(h, http.MethodPost, "/api/settings/preview-image", string(tinyPNG), c, hdr)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"type":"image/png"`) || !strings.Contains(rec.Body.String(), `"width":1`) || strings.Contains(rec.Body.String(), "iVBOR") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	got := do(h, http.MethodGet, "/api/sub-preview", "", nil, nil)
	if got.Code != 200 || got.Header().Get("Content-Type") != "image/png" || got.Body.String() != string(tinyPNG) {
		t.Fatalf("the picture is public: %d %v", got.Code, got.Header())
	}

	// The settings form keeps the picture it did not touch, and the card points at it.
	putSettings(t, h, c, `{"preview":{"mode":"custom","title":"Привет"}}`)
	page := do(h, http.MethodGet, "/sub/"+subID, "", nil, map[string]string{"Accept": "text/html"}).Body.String()
	if !strings.Contains(page, `og:image" content="https://panel.example.com/api/sub-preview?v=`) || !strings.Contains(page, `og:image:width" content="1"`) || !strings.Contains(page, "summary_large_image") {
		t.Fatalf("the card carries the uploaded picture:\n%s", page)
	}

	if rec := do(h, http.MethodDelete, "/api/settings/preview-image", "", c, nil); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/api/sub-preview", "", nil, nil); rec.Code != 404 {
		t.Fatalf("gone: %d", rec.Code)
	}
}
