package server

import (
	"bytes"
	"errors"
	"html"
	"io"
	"io/fs"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"anypanel2/internal/model"
	"anypanel2/internal/panel"
	"anypanel2/internal/remark"
	"anypanel2/internal/store"
)

var (
	titleTag = regexp.MustCompile(`(?is)<title>.*?</title>`)
	descTag  = regexp.MustCompile(`(?is)\s*<meta\s+name="description"[^>]*>`)
)

// previewPage returns the page a browser (or a messenger fetching the link for its preview card) gets for a
// subscription address: the panel's index.html with the title, description and Open Graph tags the admin chose.
// A messenger reads only these tags; browsers go on to run the page's script as usual.
func previewPage(index []byte, p model.LinkPreview, c model.Client, base string) []byte {
	if p.Mode != model.PreviewCustom {
		return index
	}
	site := remark.ExpandMeta(p.SiteName, c, false)
	title := remark.ExpandMeta(p.Title, c, false)
	desc := remark.ExpandMeta(p.Description, c, false)
	out := index
	if title != "" {
		out = titleTag.ReplaceAllLiteral(out, []byte("<title>"+html.EscapeString(title)+"</title>"))
	}
	out = descTag.ReplaceAll(out, nil)

	var tags bytes.Buffer
	meta := func(attr, name, value string) {
		if value != "" {
			tags.WriteString("    <meta " + attr + "=\"" + name + "\" content=\"" + html.EscapeString(value) + "\" />\n")
		}
	}
	meta("name", "description", desc)
	meta("property", "og:type", "website")
	meta("property", "og:site_name", site)
	meta("property", "og:title", title)
	meta("property", "og:description", desc)
	image := p.ImageURL
	if p.Image.Type != "" {
		image = base + "/api/sub-preview?v=" + p.Image.Sum // the uploaded picture wins over an address
	}
	meta("property", "og:image", image)
	if image != "" {
		meta("property", "og:image:type", p.Image.Type)
		if p.Image.Type != "" && p.Image.Width > 0 && p.Image.Height > 0 {
			meta("property", "og:image:width", strconv.Itoa(p.Image.Width))
			meta("property", "og:image:height", strconv.Itoa(p.Image.Height))
		}
		meta("name", "twitter:card", "summary_large_image")
	}
	if bytes.Contains(out, []byte("</head>")) {
		tags.WriteString("  </head>")
		return bytes.Replace(out, []byte("</head>"), tags.Bytes(), 1)
	}
	// A page without a head (a test stub): the tags go right after the title.
	return bytes.Replace(out, []byte("</title>"), append([]byte("</title>\n"), tags.Bytes()...), 1)
}

// servePage serves the SPA for a subscription address. The preview is custom only for a real subscription id: for
// an unknown one (or with subscriptions off) the plain page is served, so the preview does not tell anything about it.
func (s *Server) servePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")
	sb, err := s.svc.PublicSubscription(r.Context(), r.PathValue("subId"))
	if err == nil {
		// The administrator's own page, with the link card tags of the settings added to it as to the standard page.
		if body, ok := s.customPage(sb); ok {
			sendPage(w, previewPage(body, sb.Settings.Subscription.Preview, sb.Client, s.publicBase(r, sb.Settings)), false)
			return
		}
	}
	if err == nil && sb.Settings.Subscription.Preview.Mode == model.PreviewCustom {
		if index, rerr := fs.ReadFile(s.ui, "index.html"); rerr == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(previewPage(index, sb.Settings.Subscription.Preview, sb.Client, s.publicBase(r, sb.Settings)))
			return
		}
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Error("subscription page failed", "err", err)
	}
	http.ServeFileFS(w, r, s.ui, "index.html")
}

// publicBase is the address messengers can fetch the picture from: the subscription domain, else the panel's.
func (s *Server) publicBase(r *http.Request, set model.Settings) string {
	host := set.Subscription.Domain
	if host == "" {
		host = set.PanelDomain
	}
	if host == "" {
		host = r.Host
	}
	return "https://" + strings.TrimRight(host, "/")
}

// previewImage serves the uploaded picture of the link card. It is public: messengers fetch it without logging in.
func (s *Server) previewImage(w http.ResponseWriter, r *http.Request) {
	img, err := s.svc.PreviewImage(r.Context())
	if err != nil || img == nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", img.Type)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(img.Data)
}

// uploadPreviewImage takes the picture as the raw request body.
func (s *Server) uploadPreviewImage(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, panel.MaxPreviewImage+1))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "the picture is too big (max 2 MB)")
		return
	}
	v, err := s.svc.SetPreviewImage(r.Context(), data)
	s.respond(w, v, err)
}

func (s *Server) deletePreviewImage(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.ClearPreviewImage(r.Context())
	s.respond(w, v, err)
}
