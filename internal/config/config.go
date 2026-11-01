// Package config reads the panel configuration from the environment.
package config

import (
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Listen        string
	MongoURI      string
	MongoDatabase string
	CookieSecure  bool
	SessionTTL    time.Duration
	PublicURL     string

	StateDir        string // panel data: Xray config lives in StateDir/xray
	XrayBin         string
	XrayAPIPort     int
	BlockBittorrent bool

	// CaddyRoutesFile enables front-proxy route generation for inbounds served behind Caddy (empty = disabled).
	CaddyRoutesFile string
	CaddyReloadCmd  string

	// NginxSNIDir enables extra REALITY domains on port 443 (one per cascade): the panel then keeps the SNI map of nginx
	// and a decoy site per domain in Caddy up to date (empty = disabled, see front.SNI).
	NginxSNIDir    string
	CaddySitesDir  string
	DecoyDir       string
	NginxConf      string
	Caddyfile      string
	NginxReloadCmd string
}

// FromEnv builds a Config from environment variables, falling back to safe local defaults.
func FromEnv() Config {
	apiPort, err := strconv.Atoi(env("XRAY_API_PORT", "62789"))
	if err != nil || apiPort < 1 || apiPort > 65535 {
		apiPort = 62789
	}
	return Config{
		Listen:        env("LISTEN_ADDR", "127.0.0.1:28270"),
		MongoURI:      env("MONGO_URI", "mongodb://127.0.0.1:27017"),
		MongoDatabase: env("MONGO_DATABASE", "anypanel"),
		CookieSecure:  !strings.EqualFold(env("COOKIE_SECURE", "true"), "false"),
		SessionTTL:    12 * time.Hour,
		PublicURL:     env("PANEL_PUBLIC_URL", ""),

		StateDir:        env("STATE_DIR", "./data"),
		XrayBin:         env("XRAY_BIN", "/usr/local/bin/xray"),
		XrayAPIPort:     apiPort,
		BlockBittorrent: !strings.EqualFold(env("XRAY_BLOCK_BITTORRENT", "true"), "false"),

		CaddyRoutesFile: env("CADDY_ROUTES_FILE", ""),
		CaddyReloadCmd:  env("CADDY_RELOAD_CMD", "systemctl reload caddy"),

		NginxSNIDir:    env("NGINX_SNI_DIR", ""),
		CaddySitesDir:  env("CADDY_SITES_DIR", "/etc/anypanel/sites"),
		DecoyDir:       env("DECOY_DIR", "/var/www/anypanel-decoy"),
		NginxConf:      env("NGINX_CONF", "/etc/nginx/nginx.conf"),
		Caddyfile:      env("CADDYFILE", "/etc/caddy/Caddyfile"),
		NginxReloadCmd: env("NGINX_RELOAD_CMD", "systemctl reload nginx"),
	}
}

// ListenPort returns the TCP port of the panel's own listener (0 if it cannot be parsed).
func (c Config) ListenPort() int {
	_, p, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(p)
	return n
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
