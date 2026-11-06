package panel

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"anypanel2/internal/formats"
	"anypanel2/internal/model"
	"anypanel2/internal/sub"
	"anypanel2/internal/subpage"
	"anypanel2/internal/xray"
)

// InvalidError is a problem with user-supplied data (HTTP 400).
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &InvalidError{Msg: fmt.Sprintf(format, a...)} }

var (
	emailRe    = regexp.MustCompile(`^[A-Za-z0-9_.@+-]{1,64}$`)
	uuidRe     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	grpcNameRe = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,128}$`)
	shortIDRe  = regexp.MustCompile(`^([0-9a-fA-F]{2}){1,8}$`)
	subIDRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{4,64}$`)
	hostRe     = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.\-]{0,251}[A-Za-z0-9])?$`)
)

var (
	protocols  = []string{model.ProtoVLESS, model.ProtoTrojan}
	networks   = []string{model.NetTCP, model.NetWS, model.NetGRPC, model.NetXHTTP, model.NetHTTPUpgrade, model.NetKCP}
	securities = []string{model.SecNone, model.SecTLS, model.SecReality}
	xhttpModes = []string{"", "auto", "packet-up", "stream-up", "stream-one"}
	sniffKinds = []string{"http", "tls", "quic", "fakedns"}
)

func cleanPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/", nil
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return "", invalid("path contains control characters")
		}
	}
	if len(p) > 256 {
		return "", invalid("path is too long")
	}
	return p, nil
}

func isHost(h string) bool {
	if net.ParseIP(h) != nil {
		return true
	}
	return hostRe.MatchString(h)
}

// normalizeInbound validates an inbound and fills defaults. Generated REALITY keys/short ids are written back.
// An inbound that carries its Xray documents (Settings, StreamSettings) is read from them; one that does not (an
// older release, or an API client that only knows the typed fields) gets them built from the typed fields.
func normalizeInbound(in *model.Inbound, reserved []int) error {
	in.Remark = strings.TrimSpace(in.Remark)
	if n := len([]rune(in.Remark)); n < 1 || n > 64 {
		return invalid("remark must be 1–64 characters")
	}
	if !slices.Contains(protocols, in.Protocol) {
		return invalid("protocol must be vless or trojan")
	}
	if in.Port < 1 || in.Port > 65535 {
		return invalid("port must be 1–65535")
	}
	if slices.Contains(reserved, in.Port) {
		return invalid("port %d is reserved by the panel", in.Port)
	}
	in.Listen = strings.TrimSpace(in.Listen)
	if in.Listen == "" {
		in.Listen = "0.0.0.0"
	}
	if net.ParseIP(in.Listen) == nil {
		return invalid("listen must be an IP address")
	}

	if in.HasRaw() {
		if err := checkDocSize(in); err != nil {
			return err
		}
		in.Derive()
	} else if in.Network = model.NormalizeNetwork(in.Network); in.Security == "" {
		in.Security = model.SecNone
	}
	if !slices.Contains(networks, in.Network) {
		return invalid("network must be one of %s", strings.Join(networks, ", "))
	}
	if !slices.Contains(securities, in.Security) {
		return invalid("security must be one of %s", strings.Join(securities, ", "))
	}
	delete(in.Settings, "clients") // clients are the panel's business, not part of an inbound's own settings

	if err := normalizeTransport(in); err != nil {
		return err
	}
	switch in.Security {
	case model.SecReality:
		if in.Protocol != model.ProtoVLESS {
			return invalid("REALITY is only available for VLESS")
		}
		if !slices.Contains([]string{model.NetTCP, model.NetGRPC, model.NetXHTTP}, in.Network) {
			return invalid("REALITY works with tcp, grpc or xhttp")
		}
		if err := normalizeReality(&in.Reality); err != nil {
			return err
		}
	case model.SecTLS:
		in.TLS.CertFile, in.TLS.KeyFile = strings.TrimSpace(in.TLS.CertFile), strings.TrimSpace(in.TLS.KeyFile)
		if !in.HasRaw() && (in.TLS.CertFile == "" || in.TLS.KeyFile == "") {
			return invalid("TLS needs certificate and key file paths (or use security none behind the front proxy)")
		}
	}
	if err := normalizePublic(in); err != nil {
		return err
	}
	if err := normalizeLimits(in); err != nil {
		return err
	}

	in.EnsureRaw()
	in.Sync() // the normalised typed fields go back into the documents
	return normalizeXrayDocs(in)
}

// normalizeLimits validates the inbound-wide options of the "Basics" tab.
func normalizeLimits(in *model.Inbound) error {
	if in.TotalBytes < 0 {
		return invalid("traffic limit cannot be negative")
	}
	switch in.TrafficReset {
	case "":
		in.TrafficReset = model.ResetNever
	case model.ResetNever, model.ResetHourly, model.ResetDaily, model.ResetWeekly, model.ResetMonthly:
	default:
		return invalid("traffic reset must be never, hourly, daily, weekly or monthly")
	}
	switch in.ShareAddress {
	case "", "listen":
	default:
		return invalid("share address strategy must be empty or listen")
	}
	if in.SortOrder < -100000 || in.SortOrder > 100000 {
		return invalid("subscription sort order is out of range")
	}
	return nil
}

func normalizeTransport(in *model.Inbound) error {
	var err error
	switch in.Network {
	case model.NetWS:
		if in.WS.Path, err = cleanPath(in.WS.Path); err != nil {
			return err
		}
	case model.NetHTTPUpgrade:
		if in.HTTPUpgrade.Path, err = cleanPath(in.HTTPUpgrade.Path); err != nil {
			return err
		}
	case model.NetXHTTP:
		if in.XHTTP.Path, err = cleanPath(in.XHTTP.Path); err != nil {
			return err
		}
		if !slices.Contains(xhttpModes, in.XHTTP.Mode) {
			return invalid("xhttp mode must be auto, packet-up, stream-up or stream-one")
		}
		if in.XHTTP.Mode == "" {
			in.XHTTP.Mode = "auto"
		}
	case model.NetGRPC:
		in.GRPC.ServiceName = strings.TrimSpace(in.GRPC.ServiceName)
		if !grpcNameRe.MatchString(in.GRPC.ServiceName) {
			return invalid("gRPC service name is required (letters, digits, . _ -)")
		}
	}
	if !in.Sniffing.Enabled {
		in.Sniffing = model.Sniffing{}
		return nil
	}
	if len(in.Sniffing.DestOverride) == 0 {
		in.Sniffing.DestOverride = []string{"http", "tls", "quic"}
	}
	for _, d := range in.Sniffing.DestOverride {
		if !slices.Contains(sniffKinds, d) {
			return invalid("sniffing destOverride may contain only %s", strings.Join(sniffKinds, ", "))
		}
	}
	in.Sniffing.DomainsExcluded = model.SanitizeStrings(in.Sniffing.DomainsExcluded)
	in.Sniffing.IPsExcluded = model.SanitizeStrings(in.Sniffing.IPsExcluded)
	return nil
}

func normalizeReality(r *model.Reality) error {
	r.Dest = strings.TrimSpace(r.Dest)
	host, port, err := net.SplitHostPort(r.Dest)
	if err != nil || host == "" {
		return invalid("REALITY dest must look like example.com:443")
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return invalid("REALITY dest has an invalid port")
	}
	var names []string
	for _, n := range r.ServerNames {
		if n = strings.TrimSpace(n); n != "" {
			if !isHost(n) {
				return invalid("invalid REALITY server name %q", n)
			}
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return invalid("REALITY needs at least one server name (SNI)")
	}
	r.ServerNames = names

	if strings.TrimSpace(r.PrivateKey) == "" {
		priv, pub, err := xray.GenerateRealityKeys()
		if err != nil {
			return err
		}
		r.PrivateKey, r.PublicKey = priv, pub
	} else {
		pub, err := xray.PublicFromPrivate(strings.TrimSpace(r.PrivateKey))
		if err != nil {
			return invalid("%v", err)
		}
		r.PrivateKey, r.PublicKey = strings.TrimSpace(r.PrivateKey), pub // the public key is always derived, never trusted
	}

	var ids []string
	for _, id := range r.ShortIDs {
		if id = strings.TrimSpace(id); id != "" {
			if !shortIDRe.MatchString(id) {
				return invalid("short id %q must be 2–16 hex characters (even length)", id)
			}
			ids = append(ids, strings.ToLower(id))
		}
	}
	if len(ids) == 0 {
		id, err := xray.NewShortID()
		if err != nil {
			return err
		}
		ids = []string{id}
	}
	r.ShortIDs = ids
	if r.SpiderX != "" {
		if r.SpiderX, err = cleanPath(r.SpiderX); err != nil {
			return err
		}
	}
	return nil
}

func normalizePublic(in *model.Inbound) error {
	p := &in.Public
	p.Host = strings.TrimSpace(p.Host)
	if p.Host != "" && !isHost(p.Host) {
		return invalid("public host %q is not a valid hostname or IP", p.Host)
	}
	if p.Port < 0 || p.Port > 65535 {
		return invalid("public port must be 0–65535")
	}
	switch p.Security {
	case "", "same":
		p.Security = ""
	case model.SecTLS, model.SecNone:
	default:
		return invalid("public security must be same, tls or none")
	}
	if p.Security == model.SecTLS && in.Security == model.SecReality {
		return invalid("a REALITY inbound cannot be advertised as TLS")
	}
	p.SNI = strings.TrimSpace(p.SNI)
	if p.SNI != "" && !isHost(p.SNI) {
		return invalid("public SNI %q is not a valid hostname", p.SNI)
	}
	p.Fingerprint = strings.TrimSpace(p.Fingerprint)
	return nil
}

// normalizeClient validates a client's editable fields (credentials are generated by the service).
func normalizeClient(c *model.Client) error {
	c.Email = strings.TrimSpace(c.Email)
	if !emailRe.MatchString(c.Email) {
		return invalid("name must be 1–64 characters: letters, digits and _ . @ + -")
	}
	if c.TotalBytes < 0 {
		return invalid("traffic limit cannot be negative")
	}
	switch c.Flow {
	case "", model.FlowVision:
	default:
		return invalid("flow must be empty or %s", model.FlowVision)
	}
	if c.UUID != "" && !uuidRe.MatchString(c.UUID) {
		return invalid("uuid is not valid")
	}
	if len(c.Password) > 128 {
		return invalid("password is too long")
	}
	if c.SubID != "" && !subIDRe.MatchString(c.SubID) {
		return invalid("subscription id must be 4–64 characters: letters, digits, _ -")
	}
	if len([]rune(c.Comment)) > 256 {
		return invalid("comment is too long")
	}
	c.InboundIDs = dedupe(c.InboundIDs)
	return nil
}

func dedupe(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func normalizeSettings(s *model.Settings) error {
	// A body without subscription settings (UpdateHours 0) gets the defaults instead of silently disabling them.
	*s = s.WithDefaults()
	for _, d := range []*string{&s.PanelDomain, &s.RealityDomain} {
		*d = strings.ToLower(strings.TrimSpace(*d))
		if *d != "" && !isHost(*d) {
			return invalid("%q is not a valid domain", *d)
		}
	}
	if err := normalizeTelegram(&s.Telegram); err != nil {
		return err
	}
	return normalizeSubSettings(&s.Subscription)
}

func normalizeTelegram(t *model.TelegramSettings) error {
	seen := map[int64]bool{}
	ids := []int64{}
	for _, id := range t.AdminIDs {
		if id <= 0 {
			return invalid("Telegram user IDs are positive numbers (ask @userinfobot for yours)")
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) > 20 {
		return invalid("at most 20 administrators")
	}
	t.AdminIDs = ids
	return nil
}

func normalizePreview(p *model.LinkPreview) error {
	switch p.Mode {
	case "", model.PreviewDefault:
		p.Mode = model.PreviewDefault
	case model.PreviewCustom:
	default:
		return invalid("link preview mode must be default or custom")
	}
	for _, f := range []struct {
		name string
		v    *string
		max  int
	}{{"preview site name", &p.SiteName, 60}, {"preview title", &p.Title, 100}, {"preview description", &p.Description, 300}} {
		*f.v = strings.TrimSpace(*f.v)
		if n := len([]rune(*f.v)); n > f.max {
			return invalid("%s is too long (max %d characters)", f.name, f.max)
		}
	}
	p.ImageURL = strings.TrimSpace(p.ImageURL)
	if p.ImageURL != "" {
		u, err := url.Parse(p.ImageURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(p.ImageURL) > 512 {
			return invalid("preview picture must be a valid http(s) address")
		}
	}
	return nil
}

func normalizeSubSettings(s *model.SubSettings) error {
	if err := normalizePreview(&s.Preview); err != nil {
		return err
	}
	s.Title = strings.TrimSpace(s.Title)
	s.Announce = strings.TrimSpace(s.Announce)
	if n := len([]rune(s.Title)); n > 100 {
		return invalid("subscription title is too long (max 100 characters)")
	}
	if n := len([]rune(s.Announce)); n > 200 {
		return invalid("announcement is too long (max 200 characters)")
	}
	for _, f := range []struct {
		name string
		v    *string
	}{{"support link", &s.SupportURL}, {"profile page link", &s.ProfileURL}} {
		*f.v = strings.TrimSpace(*f.v)
		if *f.v == "" {
			continue
		}
		u, err := url.Parse(*f.v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(*f.v) > 512 {
			return invalid("%s must be a valid http(s) address", f.name)
		}
	}
	if s.UpdateHours < 1 || s.UpdateHours > 8760 {
		return invalid("update interval must be 1–8760 hours")
	}

	s.Version = model.SubSettingsVersion
	path, err := normalizeSubPath(s.Path)
	if err != nil {
		return err
	}
	s.Path = path
	s.Domain = strings.ToLower(strings.TrimSpace(s.Domain))
	if s.Domain != "" && !isHost(s.Domain) {
		return invalid("subscription domain %q is not a valid hostname", s.Domain)
	}
	s.URI = strings.TrimSpace(s.URI)
	if s.URI != "" {
		u, err := url.Parse(s.URI)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || len(s.URI) > 512 {
			return invalid("subscription URI must be an http(s) address without query or fragment")
		}
	}
	for _, f := range []struct {
		name string
		v    *string
	}{{"link name template", &s.RemarkTemplate}, {"expired template", &s.ExpiredTemplate}, {"depleted template", &s.DepletedTemplate}} {
		*f.v = strings.TrimSpace(*f.v)
		if len([]rune(*f.v)) > 256 {
			return invalid("%s is too long (max 256 characters)", f.name)
		}
	}
	switch s.ProfileMode {
	case model.ProfileModeNone, model.ProfileModeBuiltin:
	case model.ProfileModeCustom:
		if s.ProfileURL == "" {
			return invalid("profile mode \"custom\" needs a profile page link")
		}
	default:
		return invalid("profile mode must be none, builtin or custom")
	}
	s.TimeZone = strings.TrimSpace(s.TimeZone)
	if s.TimeZone != "" {
		if _, err := time.LoadLocation(s.TimeZone); err != nil {
			return invalid("unknown time zone %q (use a name like Europe/Moscow)", s.TimeZone)
		}
	}
	if err := normalizeHapp(&s.Happ); err != nil {
		return err
	}
	if err := normalizeIncy(&s.Incy); err != nil {
		return err
	}
	if err := normalizeAutoNode(&s.AutoNode); err != nil {
		return err
	}
	if err := normalizePage(&s.Page); err != nil {
		return err
	}
	return normalizeFormats(s)
}

var (
	tokenRe    = regexp.MustCompile(`^[A-Za-z0-9_.:-]{0,40}$`)
	emailAddr  = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{3,8}$`)
	rangeRe    = regexp.MustCompile(`^\d+(-\d+)?$`)
)

// checkText trims a free-text field and enforces a length limit.
func checkText(name string, v *string, max int) error {
	*v = strings.TrimSpace(*v)
	if len([]rune(*v)) > max {
		return invalid("%s is too long (max %d characters)", name, max)
	}
	if strings.ContainsAny(*v, "\r\n") && max < 1000 {
		return invalid("%s must be a single line", name)
	}
	return nil
}

// checkLink accepts an empty value or an address with one of the allowed schemes.
func checkLink(name string, v *string, schemes ...string) error {
	*v = strings.TrimSpace(*v)
	if *v == "" {
		return nil
	}
	u, err := url.Parse(*v)
	if err != nil || u.Scheme == "" || len(*v) > 512 || strings.ContainsAny(*v, "\r\n ") || !slices.Contains(schemes, u.Scheme) {
		return invalid("%s must be an address starting with %s://", name, strings.Join(schemes, "://, "))
	}
	return nil
}

func checkToken(name string, v *string) error {
	*v = strings.TrimSpace(*v)
	if !tokenRe.MatchString(*v) {
		return invalid("%s may contain only letters, digits and . _ : - (up to 40 characters)", name)
	}
	return nil
}

func normalizeHapp(h *model.HappSettings) error {
	for _, f := range []struct {
		name string
		v    *string
		max  int
	}{{"Happ provider id", &h.ProviderID, 100},
		{"Happ color profile", &h.ColorProfile, 100}, {"Happ excluded routes", &h.ExcludeRoutes, 1000}, {"Happ app list", &h.PerAppList, 4000}} {
		if err := checkText(f.name, f.v, f.max); err != nil {
			return err
		}
	}
	for _, f := range []struct {
		name    string
		v       *string
		schemes []string
	}{{"Happ new address", &h.NewURL, []string{"http", "https"}}, {"Happ fallback address", &h.FallbackURL, []string{"http", "https"}},
		{"Happ expiry button link", &h.SubExpireButtonLink, []string{"http", "https", "tg"}}} {
		if err := checkLink(f.name, f.v, f.schemes...); err != nil {
			return err
		}
	}
	for _, f := range []struct {
		name string
		v    *string
	}{{"Happ tun mode", &h.TunMode}, {"Happ tun type", &h.TunType}, {"Happ ping type", &h.PingType}, {"Happ auto-connect type", &h.AutoConnectType},
		{"Happ per-app mode", &h.PerAppMode}, {"Happ local proxy auth", &h.LocalProxyAuth}} {
		if err := checkToken(f.name, f.v); err != nil {
			return err
		}
	}
	if err := normalizeBanner("Happ banner", &h.SubInfoColor, &h.SubInfoText, &h.SubInfoButtonText, &h.SubInfoButtonLink); err != nil {
		return err
	}
	for _, b := range []struct {
		name string
		v    *model.HappBanner
	}{{"Happ banner for active", &h.Banners.Active}, {"Happ banner for test subscription", &h.Banners.Trial},
		{"Happ banner for expired", &h.Banners.Expired}, {"Happ banner for depleted", &h.Banners.Depleted}} {
		if err := normalizeBanner(b.name, &b.v.Color, &b.v.Text, &b.v.ButtonText, &b.v.ButtonLink); err != nil {
			return err
		}
	}
	h.RoutingRules = strings.TrimSpace(h.RoutingRules)
	if len(h.RoutingRules) > 100000 {
		return invalid("Happ routing profile is too large")
	}
	if _, err := sub.HappRoutingLink(h.RoutingRules); err != nil {
		return invalid("%v", err)
	}
	if h.EnableRouting && h.RoutingRules == "" {
		return invalid("Happ routing is switched on but no profile is set")
	}
	return nil
}

// normalizeBanner checks one Happ banner. Unlike other header texts the banner text may have several lines.
func normalizeBanner(name string, color, text, buttonText, buttonLink *string) error {
	*text = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(*text), "\r\n", "\n"), "\r", "\n")
	if n := len([]rune(*text)); n > 200 {
		return invalid("%s text is too long (max 200 characters)", name)
	}
	if strings.Count(*text, "\n") > 9 {
		return invalid("%s text has too many lines (max 10)", name)
	}
	if err := checkText(name+" button text", buttonText, 60); err != nil {
		return err
	}
	if err := checkLink(name+" button link", buttonLink, "http", "https", "tg"); err != nil {
		return err
	}
	*color = strings.ToLower(strings.TrimSpace(*color))
	if !slices.Contains([]string{"", "blue", "green", "red", "primary", "info", "success", "warning", "danger"}, *color) {
		return invalid("%s color must be blue, green or red", name)
	}
	return nil
}

func normalizeIncy(c *model.IncySettings) error {
	for _, f := range []struct {
		name string
		v    *string
		max  int
	}{{"INCY description", &c.ProfileDescription, 300}, {"INCY banner text", &c.BannerText, 200}, {"INCY banner button text", &c.BannerButtonText, 60},
		{"INCY app list", &c.PerAppList, 4000}, {"INCY noise packet", &c.NoisesPacket, 200}, {"INCY DNS domain", &c.ResolveDNSDomain, 253}, {"INCY DNS address", &c.ResolveDNSIP, 100}} {
		if err := checkText(f.name, f.v, f.max); err != nil {
			return err
		}
	}
	for _, f := range []struct {
		name string
		v    *string
	}{{"INCY announcement link", &c.AnnounceURL}, {"INCY premium link", &c.PremiumURL}, {"INCY banner button link", &c.BannerButtonURL}} {
		if err := checkLink(f.name, f.v, "http", "https", "tg"); err != nil {
			return err
		}
	}
	c.SupportEmail = strings.TrimSpace(c.SupportEmail)
	if c.SupportEmail != "" && (len(c.SupportEmail) > 200 || !emailAddr.MatchString(c.SupportEmail)) {
		return invalid("INCY support e-mail is not valid")
	}
	for _, f := range []struct {
		name string
		v    *string
		ok   []string
	}{{"INCY sort order", &c.SortOrder, []string{"", "none", "ping", "name"}}, {"INCY per-app mode", &c.PerAppMode, []string{"", "bypass", "proxy"}},
		{"INCY noise type", &c.NoisesType, []string{"", "rand", "str", "hex"}}} {
		*f.v = strings.ToLower(strings.TrimSpace(*f.v))
		if !slices.Contains(f.ok, *f.v) {
			return invalid("%s must be one of: %s", f.name, strings.Join(f.ok[1:], ", "))
		}
	}
	for _, f := range []struct {
		name string
		v    *string
	}{{"INCY hide address", &c.HideURL}, {"INCY hide check", &c.HideCheck}, {"INCY no-limit", &c.NoLimitEnabled}, {"INCY per-app switch", &c.PerAppEnable},
		{"INCY fragmentation switch", &c.FragmentationEnable}, {"INCY noises switch", &c.NoisesEnable}, {"INCY resolve switch", &c.ResolveEnable}} {
		*f.v = strings.TrimSpace(*f.v)
		if *f.v != "" && *f.v != "0" && *f.v != "1" {
			return invalid("%s must be empty (not set), 0 or 1", f.name)
		}
	}
	for _, f := range []struct {
		name string
		v    *string
	}{{"INCY banner color", &c.BannerBgColor}, {"INCY banner button color", &c.BannerButtonColor}} {
		*f.v = strings.TrimSpace(*f.v)
		if *f.v != "" && !hexColorRe.MatchString(*f.v) {
			return invalid("%s must look like #1a2b3c", f.name)
		}
	}
	for _, f := range []struct {
		name string
		v    *string
	}{{"INCY fragment length", &c.FragmentationLength}, {"INCY fragment interval", &c.FragmentationInterval}, {"INCY noise delay", &c.NoisesDelay}} {
		*f.v = strings.TrimSpace(*f.v)
		if *f.v != "" && !rangeRe.MatchString(*f.v) {
			return invalid("%s must be a number or a range like 10-20", f.name)
		}
	}
	c.FragmentationPackets = strings.ToLower(strings.TrimSpace(c.FragmentationPackets))
	if p := c.FragmentationPackets; p != "" && p != "tlshello" && p != "all" && p != "1" && !regexp.MustCompile(`^\d+-\d+$`).MatchString(p) {
		return invalid("INCY fragment packets must be tlshello, all, 1 or a range like 1-3")
	}
	c.RoutingRules = strings.TrimSpace(c.RoutingRules)
	if _, err := sub.IncyRoutingLine(c.RoutingRules); err != nil {
		return invalid("%v", err)
	}
	if c.EnableRouting && c.RoutingRules == "" {
		return invalid("INCY routing is switched on but no link is set")
	}
	return nil
}

// reservedSubPaths are first path segments the panel itself uses.
var reservedSubPaths = []string{"api", "assets", "login", "inbounds", "clients", "subscription", "settings", "favicon.svg"}

var subPathRe = regexp.MustCompile(`^/([A-Za-z0-9._~-]+/)+$`)

func normalizeSubPath(p string) (string, error) {
	return normalizePrefix(p, model.DefaultSubPath, "subscription")
}

// normalizeFormats validates the Clash and Xray JSON format settings and checks that the three prefixes
// (subscription, Clash, JSON) do not overlap, which would make routing ambiguous.
func normalizeFormats(s *model.SubSettings) error {
	f := &s.Formats
	var err error
	if f.ClashPath, err = normalizePrefix(f.ClashPath, model.DefaultClashPath, "Clash"); err != nil {
		return err
	}
	if f.JSONPath, err = normalizePrefix(f.JSONPath, model.DefaultJSONPath, "JSON"); err != nil {
		return err
	}
	paths := []string{s.Path, f.ClashPath, f.JSONPath}
	for i := range paths {
		for j := i + 1; j < len(paths); j++ {
			if strings.HasPrefix(paths[i], paths[j]) || strings.HasPrefix(paths[j], paths[i]) {
				return invalid("the subscription, Clash and JSON paths must differ and none may start with another (%s, %s)", paths[i], paths[j])
			}
		}
	}
	for _, r := range []struct {
		name string
		v    *string
		auto bool
	}{{"Clash User-Agent pattern", &f.ClashUARegex, f.ClashAutoDetect}, {"JSON User-Agent pattern", &f.JSONUARegex, f.JSONAutoDetect}} {
		*r.v = strings.TrimSpace(*r.v)
		if len(*r.v) > 200 {
			return invalid("%s is too long", r.name)
		}
		if *r.v == "" {
			if r.auto {
				return invalid("%s is required when auto-detection is on", r.name)
			}
			continue
		}
		if _, err := regexp.Compile(*r.v); err != nil {
			return invalid("%s is not a valid regular expression: %v", r.name, err)
		}
	}
	if _, err := formats.ParseClashRules(f.ClashRules); err != nil {
		return invalid("%v", err)
	}
	f.ClashRules = strings.TrimSpace(f.ClashRules)
	if f.ClashEnableRouting && f.ClashRules == "" {
		return invalid("Clash routing is switched on but no rules are set")
	}
	opt := formats.JSONOptions{Mux: f.JSONMux, Fragment: f.JSONFragment, Noises: f.JSONNoises, Rules: f.JSONRules, DNS: f.JSONDNS}
	if err := formats.ValidateJSONOptions(opt); err != nil {
		return invalid("%v", err)
	}
	return nil
}

// normalizePrefix makes the prefix start and end with "/" and rejects paths that collide with the panel.
func normalizePrefix(p, def, label string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return def, nil
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	if len(p) > 100 || !subPathRe.MatchString(p) {
		return "", invalid("%s path must look like /sub/ or /s/abc123/ (letters, digits, . _ ~ -)", label)
	}
	first := strings.SplitN(strings.TrimPrefix(p, "/"), "/", 2)[0]
	if slices.Contains(reservedSubPaths, first) {
		return "", invalid("%s path cannot start with /%s/: the panel uses it", label, first)
	}
	return p, nil
}

func asInvalid(err error, target **InvalidError) bool { return errors.As(err, target) }

// normalizeAutoNode validates the automatic node settings. Probing more often than every 2 seconds only burns the subscriber's battery and data.
func normalizeAutoNode(a *model.AutoNodeSettings) error {
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" {
		a.Name = model.DefaultAutoNodeName
	}
	if n := len([]rune(a.Name)); n > 60 {
		return invalid("automatic node name is too long (max 60 characters)")
	}
	if strings.ContainsAny(a.Name, "\r\n") {
		return invalid("automatic node name must be a single line")
	}
	a.ProbeURL = strings.TrimSpace(a.ProbeURL)
	if a.ProbeURL == "" {
		a.ProbeURL = model.DefaultAutoNodeProbeURL
	}
	if err := checkLink("automatic node check address", &a.ProbeURL, "http", "https"); err != nil {
		return err
	}
	if a.IntervalSec < 2 || a.IntervalSec > 3600 {
		return invalid("automatic node check interval must be 2–3600 seconds")
	}
	if a.TimeoutSec < 1 || a.TimeoutSec > 60 {
		return invalid("automatic node timeout must be 1–60 seconds")
	}
	if a.TimeoutSec > a.IntervalSec {
		return invalid("automatic node timeout cannot be longer than the check interval")
	}
	if a.ToleranceMs < 0 || a.ToleranceMs > 5000 {
		return invalid("automatic node tolerance must be 0–5000 ms")
	}
	return normalizeAutoGroups(a)
}

const maxPageThemes = 20

var themeIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)

// normalizePage checks the saved page themes: each needs a name, and one that has code has to be a template that
// renders, or a subscriber would see an error instead of their subscription. The selected theme cannot be empty.
func normalizePage(p *model.PageSettings) error {
	p.OldEnable, p.OldHTML, p.OldCSS, p.OldJS = false, "", "", ""
	if len(p.Themes) > maxPageThemes {
		return invalid("too many page themes (max %d)", maxPageThemes)
	}
	if p.Themes == nil {
		p.Themes = []model.PageTheme{}
	}
	ids, names := map[string]bool{}, map[string]bool{}
	for i := range p.Themes {
		t := &p.Themes[i]
		t.Name = strings.TrimSpace(t.Name)
		if t.Name == "" {
			return invalid("a page theme needs a name")
		}
		if len([]rune(t.Name)) > 60 || strings.ContainsAny(t.Name, "\r\n") {
			return invalid("page theme name must be a single line of at most 60 characters")
		}
		if names[strings.ToLower(t.Name)] {
			return invalid("there are two page themes named %q", t.Name)
		}
		names[strings.ToLower(t.Name)] = true
		t.ID = strings.TrimSpace(t.ID)
		if t.ID == "" {
			t.ID = newID()
		}
		if !themeIDRe.MatchString(t.ID) || ids[t.ID] {
			return invalid("page theme id %q is not valid or is used twice", t.ID)
		}
		ids[t.ID] = true
		if strings.TrimSpace(t.HTML) == "" {
			t.HTML = ""
			continue
		}
		if err := subpage.Validate(subpage.Page{HTML: t.HTML, CSS: t.CSS, JS: t.JS}); err != nil {
			return invalid("the page %q does not work: %v", t.Name, err)
		}
	}
	if p.Selected != "" {
		t := p.Theme(p.Selected)
		if t == nil {
			return invalid("the selected page theme does not exist")
		}
		if t.HTML == "" {
			return invalid("the selected page %q has no HTML", t.Name)
		}
	}
	return nil
}

const (
	maxAutoGroups       = 10
	maxAutoGroupMembers = 100
)

// normalizeAutoGroups validates the extra automatic nodes: a name, a flag and the connections each one chooses among.
func normalizeAutoGroups(a *model.AutoNodeSettings) error {
	if len(a.Groups) > maxAutoGroups {
		return invalid("too many automatic nodes (max %d)", maxAutoGroups)
	}
	if a.Groups == nil {
		a.Groups = []model.AutoGroup{}
	}
	ids := map[string]bool{}
	for i := range a.Groups {
		g := &a.Groups[i]
		g.Name = strings.TrimSpace(g.Name)
		if g.Name == "" {
			g.Name = "Авто"
		}
		if len([]rune(g.Name)) > 60 {
			return invalid("automatic node name is too long (max 60 characters)")
		}
		if strings.ContainsAny(g.Name, "\r\n") {
			return invalid("automatic node name must be a single line")
		}
		g.Country = strings.ToUpper(strings.TrimSpace(g.Country))
		if g.Country != "" && (!countryRe.MatchString(g.Country) || model.FlagEmoji(g.Country) == "") {
			return invalid("automatic node country must be a two-letter code such as RU")
		}
		if g.ID == "" {
			g.ID = newID()
			g.Last = true // a new node goes to the end of the list
		}
		if ids[g.ID] {
			return invalid("automatic node id %q is used twice", g.ID)
		}
		ids[g.ID] = true
		if len(g.Members) > maxAutoGroupMembers {
			return invalid("too many connections in the automatic node %q (max %d)", g.Name, maxAutoGroupMembers)
		}
		seen := map[string]bool{}
		members := make([]string, 0, len(g.Members))
		for _, m := range g.Members {
			m = strings.TrimSpace(m)
			if m != "" && !seen[m] {
				seen[m] = true
				members = append(members, m)
			}
		}
		g.Members = members
		if g.Position < 0 {
			g.Position = 0
		}
	}
	return nil
}
