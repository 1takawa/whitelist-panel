// Package remark expands the {{TOKEN}} templates used for link names, info nodes and profile metadata.
//
// A template is split on "|" into segments. A segment that contains tokens but where none of them produced a real
// value (empty, or "∞" for an unlimited quota or expiry) is dropped whole, so an unlimited client gets "host" instead
// of "host|📊∞|⏳∞D". A hyphen standing alone between two adjacent tokens is a separator and is elided when the token
// before it produced nothing or the token after it is empty ("{{INBOUND}}-{{EMAIL}}" gives "john" without an inbound).
// Unknown tokens expand to the empty string.
package remark

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"anypanel2/internal/model"
)

// Unlimited is what quota and expiry tokens render for an unlimited client.
const Unlimited = "∞"

var tokenRe = regexp.MustCompile(`\{\{([A-Z_]+)\}\}`)

// Context is the data a template can use. Client must already carry the effective expiry.
type Context struct {
	Client   model.Client
	Inbound  *model.Inbound // optional: enables INBOUND, PROTOCOL, TRANSPORT, SECURITY
	Server   *model.Server  // optional: the extra server the inbound runs on (SERVER, COUNTRY, FLAG)
	Security string         // security advertised to clients (may differ from Inbound.Security behind a front proxy)
	Now      time.Time
	Loc      *time.Location // for EXPIRE_DATE; nil means UTC
}

// Token describes one placeholder for the settings UI.
type Token struct {
	Name string `json:"name"`
	Help string `json:"help"`
}

// Tokens lists every supported token, grouped by usefulness for the UI.
var Tokens = []Token{
	{"EMAIL", "Имя клиента"},
	{"INBOUND", "Название подключения"},
	{"PROTOCOL", "Протокол: VLESS или TROJAN"},
	{"TRANSPORT", "Транспорт: tcp, ws, grpc…"},
	{"SECURITY", "Безопасность: TLS, REALITY, NONE"},
	{"SERVER", "Название дополнительного сервера (пусто для главного)"},
	{"COUNTRY", "Код страны сервера: DE, NL…"},
	{"FLAG", "Флаг страны сервера"},
	{"TRAFFIC_LEFT", "Остаток трафика («∞» без лимита)"},
	{"TRAFFIC_USED", "Использованный трафик"},
	{"TRAFFIC_TOTAL", "Лимит трафика («∞» без лимита)"},
	{"USAGE_PERCENTAGE", "Использовано, %"},
	{"UP", "Отправлено"},
	{"DOWN", "Получено"},
	{"DAYS_LEFT", "Осталось дней («∞» бессрочно)"},
	{"TIME_LEFT", "Осталось времени: 3d 4h 5m"},
	{"EXPIRE_DATE", "Дата окончания ГГГГ-ММ-ДД"},
	{"EXPIRE_UNIX", "Окончание, unix-время"},
	{"STATUS", "Статус: active, expired, depleted, disabled"},
	{"STATUS_EMOJI", "Значок статуса: ✅ ⏳ 🚫"},
	{"COMMENT", "Комментарий клиента"},
	{"SUB_ID", "Идентификатор подписки"},
	{"ID", "Внутренний идентификатор клиента"},
	{"SHORT_ID", "Первые 8 символов идентификатора"},
	{"TRAFFIC_USED_BYTES", "Использовано, байт"},
	{"TRAFFIC_LEFT_BYTES", "Осталось, байт"},
	{"TRAFFIC_TOTAL_BYTES", "Лимит, байт"},
	{"CREATED_UNIX", "Создан, unix-время"},
}

// dropWhenUnlimited are the tokens whose "∞" value carries no information on its own.
var dropWhenUnlimited = map[string]bool{"TRAFFIC_LEFT": true, "TRAFFIC_TOTAL": true, "DAYS_LEFT": true, "TIME_LEFT": true}

// Expand renders a full template with all tokens.
func Expand(template string, ctx Context) string {
	if !strings.Contains(template, "{{") {
		return template
	}
	segments := strings.Split(template, "|")
	kept := make([]string, 0, len(segments))
	for _, seg := range segments {
		if out, drop := expandSegment(seg, ctx); !drop {
			kept = append(kept, out)
		}
	}
	return strings.Join(kept, "|")
}

type tokenLoc struct {
	name       string
	start, end int
}

func tokensIn(seg string) []tokenLoc {
	locs := tokenRe.FindAllStringSubmatchIndex(seg, -1)
	out := make([]tokenLoc, len(locs))
	for i, l := range locs {
		out[i] = tokenLoc{name: seg[l[2]:l[3]], start: l[0], end: l[1]}
	}
	return out
}

func expandSegment(seg string, ctx Context) (string, bool) {
	toks := tokensIn(seg)
	hasToken, hasValue := len(toks) > 0, false
	vals := make([]string, len(toks))
	for i, t := range toks {
		v := value(t.name, ctx)
		vals[i] = v
		if v != "" && (!dropWhenUnlimited[t.name] || v != Unlimited) {
			hasValue = true
		}
	}

	var b strings.Builder
	start, wrote := 0, false
	for i, t := range toks {
		b.WriteString(seg[start:t.start])
		b.WriteString(vals[i])
		wrote = wrote || vals[i] != ""
		start = t.end
		if i+1 < len(toks) {
			between := seg[start:toks[i+1].start]
			if strings.TrimSpace(between) == "-" && (!wrote || vals[i+1] == "") {
				start = toks[i+1].start
			}
		}
	}
	b.WriteString(seg[start:])
	return b.String(), hasToken && !hasValue
}

// metaTokens are the only tokens allowed in profile metadata (title, links, announcement): identity only.
var metaTokens = map[string]bool{"EMAIL": true, "ID": true, "SHORT_ID": true, "SUB_ID": true}

// ExpandMeta renders profile metadata. Only identity tokens are replaced; others stay as typed. With escape the
// values are URL-query-escaped, which is what link fields (support and profile URLs) need.
func ExpandMeta(template string, c model.Client, escape bool) string {
	if !strings.Contains(template, "{{") {
		return strings.TrimSpace(template)
	}
	out := tokenRe.ReplaceAllStringFunc(template, func(m string) string {
		name := m[2 : len(m)-2]
		if !metaTokens[name] {
			return m
		}
		v := value(name, Context{Client: c})
		if escape {
			return url.QueryEscape(v)
		}
		return v
	})
	return strings.TrimSpace(out)
}

func value(name string, ctx Context) string {
	c := ctx.Client
	used := c.Up + c.Down
	switch name {
	case "EMAIL":
		return c.Email
	case "INBOUND":
		if ctx.Inbound != nil {
			return ctx.Inbound.Remark
		}
	case "PROTOCOL":
		if ctx.Inbound != nil {
			return strings.ToUpper(ctx.Inbound.Protocol)
		}
	case "TRANSPORT":
		if ctx.Inbound != nil {
			return ctx.Inbound.Network
		}
	case "SECURITY":
		sec := ctx.Security
		if sec == "" && ctx.Inbound != nil {
			sec = ctx.Inbound.Security
		}
		return strings.ToUpper(sec)
	case "SERVER":
		if ctx.Server != nil {
			return ctx.Server.Name
		}
	case "COUNTRY":
		if ctx.Server != nil {
			return ctx.Server.Country
		}
	case "FLAG":
		if ctx.Server != nil {
			return ctx.Server.Flag()
		}
	case "ID":
		return c.ID
	case "SHORT_ID":
		if len(c.ID) >= 8 {
			return c.ID[:8]
		}
		return c.ID
	case "SUB_ID":
		return c.SubID
	case "COMMENT":
		return c.Comment
	case "STATUS":
		return Status(c, ctx.Now)
	case "STATUS_EMOJI":
		return map[string]string{"active": "✅", "expired": "⏳", "depleted": "🚫", "disabled": "🚫"}[Status(c, ctx.Now)]
	case "TRAFFIC_USED":
		return FormatBytes(used)
	case "TRAFFIC_LEFT":
		if c.TotalBytes <= 0 {
			return Unlimited
		}
		return FormatBytes(max(c.TotalBytes-used, 0))
	case "TRAFFIC_TOTAL":
		if c.TotalBytes <= 0 {
			return Unlimited
		}
		return FormatBytes(c.TotalBytes)
	case "TRAFFIC_USED_BYTES":
		return strconv.FormatInt(used, 10)
	case "TRAFFIC_LEFT_BYTES":
		if c.TotalBytes <= 0 {
			return "0"
		}
		return strconv.FormatInt(max(c.TotalBytes-used, 0), 10)
	case "TRAFFIC_TOTAL_BYTES":
		return strconv.FormatInt(c.TotalBytes, 10)
	case "UP":
		return FormatBytes(c.Up)
	case "DOWN":
		return FormatBytes(c.Down)
	case "USAGE_PERCENTAGE":
		if c.TotalBytes <= 0 {
			return ""
		}
		pct := min(float64(used)/float64(c.TotalBytes)*100, 100)
		// U+FF05 (fullwidth percent): an ASCII % becomes %25 in a link fragment, which some apps reject.
		return fmt.Sprintf("%.1f％", pct)
	case "DAYS_LEFT":
		if c.ExpiryAt == nil {
			return Unlimited
		}
		secs := int64(c.ExpiryAt.Sub(ctx.Now).Seconds())
		if secs < 0 {
			return "0"
		}
		return strconv.FormatInt(secs/86400, 10)
	case "TIME_LEFT":
		if c.ExpiryAt == nil {
			return Unlimited
		}
		return timeLeft(int64(c.ExpiryAt.Sub(ctx.Now).Seconds()))
	case "EXPIRE_DATE":
		if c.ExpiryAt == nil {
			return ""
		}
		loc := ctx.Loc
		if loc == nil {
			loc = time.UTC
		}
		return c.ExpiryAt.In(loc).Format("2006-01-02")
	case "EXPIRE_UNIX":
		if c.ExpiryAt == nil {
			return "0"
		}
		return strconv.FormatInt(c.ExpiryAt.Unix(), 10)
	case "CREATED_UNIX":
		if c.CreatedAt.IsZero() {
			return ""
		}
		return strconv.FormatInt(c.CreatedAt.Unix(), 10)
	}
	return ""
}

// Status collapses enabled / expiry / quota into one word.
func Status(c model.Client, now time.Time) string {
	switch {
	case !c.Enable:
		return "disabled"
	case c.Expired(now):
		return "expired"
	case c.Exhausted():
		return "depleted"
	}
	return "active"
}

func timeLeft(secs int64) string {
	if secs <= 0 {
		return "0"
	}
	d, h, m := secs/86400, (secs%86400)/3600, (secs%3600)/60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd %dh %dm", d, h, m)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// FormatBytes renders a byte count compactly: "512B", "1.50KB", "3.20GB".
func FormatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	v, i := float64(n), -1
	for v >= unit && i < 3 {
		v /= unit
		i++
	}
	return fmt.Sprintf("%.2f%s", v, []string{"KB", "MB", "GB", "TB"}[i])
}
