package subpage

import (
	"embed"
	"errors"
	"net/url"
	"path"
	"strings"
)

// Built-in pages that come with the panel. They are not edited: the administrator fills in a few fields (a site, a Telegram
// link…) that the page puts in. The page itself, its pictures and its script are part of the program.

//go:embed presets
var presetFS embed.FS

// PresetPrefix starts the id of a built-in page in PageSettings.Selected, so it cannot collide with a saved theme's id.
const PresetPrefix = "preset:"

// PresetField is one value the administrator can change in a built-in page.
type PresetField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder"`
	Default     string `json:"default"`
}

// Preset describes a built-in page for the list in the settings.
type Preset struct {
	ID     string        `json:"id"`
	Name   string        `json:"name"`
	Tag    string        `json:"tag"`  // a short label shown next to the name, e.g. "Спонсор"
	Icon   string        `json:"icon"` // address of the picture shown in the list
	Fields []PresetField `json:"fields"`
}

var presets = []Preset{
	{
		ID: "hidden-pool", Name: "Hidden Pool", Tag: "Спонсор", Icon: "/api/theme-assets/hidden-pool/mark.png",
		Fields: []PresetField{
			{Key: "site", Label: "Сайт", Placeholder: "https://example.com"},
			{Key: "telegram", Label: "Telegram", Placeholder: "@name или https://t.me/name", Default: "https://t.me/HiddenPoolVPNBot"},
			{Key: "support", Label: "Поддержка", Placeholder: "@name или https://t.me/name", Default: "https://t.me/HiddenPool_supp"},
		},
	},
}

// Presets is the list of built-in pages, in the order the settings show them.
func Presets() []Preset { return append([]Preset(nil), presets...) }

// FindPreset looks a built-in page up by the id without the prefix.
func FindPreset(id string) (Preset, bool) {
	for _, p := range presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

// Page is the code of the built-in page.
func (p Preset) Page() Page {
	read := func(name string) string {
		b, _ := presetFS.ReadFile("presets/" + p.ID + "/" + name)
		return string(b)
	}
	return Page{HTML: read("page.html"), CSS: read("page.css"), JS: read("page.js")}
}

// Values are the values the page is filled with: what the administrator saved, and the preset's own for what was never
// saved. A field saved empty stays empty, which hides the link.
func (p Preset) Values(saved map[string]string) map[string]string {
	out := make(map[string]string, len(p.Fields))
	for _, f := range p.Fields {
		if v, ok := saved[f.Key]; ok {
			out[f.Key] = v
		} else {
			out[f.Key] = f.Default
		}
	}
	return out
}

// NormalizeField checks a link typed into a field. A Telegram name (@name) becomes its t.me address, an address without
// a scheme gets https://, and only http and https links are kept. An empty value is allowed.
func (p Preset) NormalizeField(key, v string) (string, error) {
	known := false
	for _, f := range p.Fields {
		known = known || f.Key == key
	}
	if !known {
		return "", errors.New("unknown field " + key)
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if len(v) > 300 || strings.ContainsAny(v, " \t\r\n\x00") {
		return "", errors.New("the link is too long or has spaces")
	}
	if strings.HasPrefix(v, "@") && key != "site" {
		v = "https://t.me/" + strings.TrimPrefix(v, "@")
	} else if !strings.Contains(v, "://") {
		v = "https://" + v
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || !strings.Contains(u.Hostname(), ".") {
		return "", errors.New("it must be a web address such as https://example.com")
	}
	return v, nil
}

// assetTypes are the files a built-in page may serve, by extension.
var assetTypes = map[string]string{".png": "image/png", ".jpg": "image/jpeg"}

// Asset returns a picture of a built-in page and its content type. Only pictures of the registered pages are served.
func Asset(preset, file string) ([]byte, string, bool) {
	if _, ok := FindPreset(preset); !ok || file != path.Base(file) {
		return nil, "", false
	}
	ct, ok := assetTypes[strings.ToLower(path.Ext(file))]
	if !ok {
		return nil, "", false
	}
	b, err := presetFS.ReadFile("presets/" + preset + "/" + file)
	if err != nil {
		return nil, "", false
	}
	return b, ct, true
}
