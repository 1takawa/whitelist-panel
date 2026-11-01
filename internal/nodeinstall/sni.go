package nodeinstall

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// SNISite is one extra REALITY domain served by a node's nginx on port 443: Port is the loopback port of its Xray
// connection.
type SNISite struct {
	Domain string
	Port   int
}

// SNIScript is the shell script that makes a node's nginx (the nginx-SNI scheme: /etc/nginx/stream-enabled/stream.conf
// with the map $sni_name and the upstream xray) send each of the sites' domains to its own Xray port. It owns two small
// include files and adds the two include lines to stream.conf once (keeping a backup); a change is checked with
// `nginx -t` first and undone if nginx refuses it, so a bad domain can never break the node's own connections.
//
// The paths and the nginx command can be replaced through ANYPANEL_NGINX_STREAM, ANYPANEL_SNI_DIR and ANYPANEL_NGINX_BIN
// (the tests use that).
func SNIScript(sites []SNISite) (string, error) {
	sorted := append([]SNISite(nil), sites...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Domain < sorted[j].Domain })
	var m, u strings.Builder
	seen := map[int]bool{}
	for _, s := range sorted {
		if !ValidDomain(s.Domain) {
			return "", fmt.Errorf("неверный домен: %q", s.Domain)
		}
		if s.Port < 1 || s.Port > 65535 {
			return "", errors.New("неверный порт Xray")
		}
		fmt.Fprintf(&m, "    %s  c%d;\n", s.Domain, s.Port)
		if !seen[s.Port] {
			seen[s.Port] = true
			fmt.Fprintf(&u, "    upstream c%d { server 127.0.0.1:%d; }\n", s.Port, s.Port)
		}
	}
	return fmt.Sprintf(sniScript, m.String(), u.String()), nil
}

const sniScript = `set -e
CONF="${ANYPANEL_NGINX_STREAM:-/etc/nginx/stream-enabled/stream.conf}"
DIR="${ANYPANEL_SNI_DIR:-/etc/anypanel-sni}"
NGINX="${ANYPANEL_NGINX_BIN:-nginx}"
MAP="$DIR/sni-map.conf"
UP="$DIR/sni-upstreams.conf"
if [ ! -f "$CONF" ]; then
  echo "нет файла $CONF: на сервере не схема nginx + SNI (nginx stream); сначала нажмите «REALITY-сайт» у сервера"
  exit 3
fi
mkdir -p "$DIR"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
cp -a "$CONF" "$TMP/conf.old"
[ -f "$MAP" ] && cp -a "$MAP" "$TMP/map.old" || true
[ -f "$UP" ] && cp -a "$UP" "$TMP/up.old" || true
[ -f "$CONF.anypanel-sni-bak" ] || cp -a "$CONF" "$CONF.anypanel-sni-bak"

restore() {
  cat "$TMP/conf.old" > "$CONF"
  if [ -f "$TMP/map.old" ]; then cat "$TMP/map.old" > "$MAP"; else rm -f "$MAP"; fi
  if [ -f "$TMP/up.old" ]; then cat "$TMP/up.old" > "$UP"; else rm -f "$UP"; fi
}

{
  echo "# Managed by Whitelist Panel. Do not edit: this file is regenerated on every change."
  cat <<'ANYPANEL_MAP'
%s
ANYPANEL_MAP
} | sed '/^$/d' > "$MAP"
{
  echo "# Managed by Whitelist Panel. Do not edit: this file is regenerated on every change."
  cat <<'ANYPANEL_UP'
%s
ANYPANEL_UP
} | sed '/^$/d' > "$UP"
chmod 644 "$MAP" "$UP"

if ! grep -qF "include $MAP;" "$CONF"; then
  if ! awk -v m="        include $MAP;" -v u="    include $UP;" '
      /^[[:space:]]*map[[:space:]]+\$ssl_preread_server_name/ && !dm { print; print m; dm = 1; next }
      /^[[:space:]]*upstream[[:space:]]+xray/ && !du { print u; print; du = 1; next }
      { print }
      END { if (!dm || !du) exit 7 }' "$CONF" > "$TMP/conf.new"; then
    restore
    echo "stream.conf не в формате установщика (нет блока map или upstream xray): файл не изменён"
    exit 5
  fi
  cat "$TMP/conf.new" > "$CONF"
fi

if ! out=$("$NGINX" -t 2>&1); then
  restore
  echo "nginx отклонил настройки, ничего не изменено: $out"
  exit 4
fi
if [ -z "$ANYPANEL_NGINX_BIN" ]; then
  "$NGINX" -s reload 2>/dev/null || systemctl reload nginx
fi
echo "nginx: домены каскадов обновлены"
`

// SyncSNI logs in with the key the panel keeps for the node and makes its nginx serve exactly these domains.
func SyncSNI(ctx context.Context, t Target, sites []SNISite, log func(string)) error {
	script, err := SNIScript(sites)
	if err != nil {
		return err
	}
	log("Подключаюсь к " + t.Host + " по SSH…")
	c, err := Dial(ctx, t)
	if err != nil {
		return err
	}
	defer c.Close()
	lw := &lineWriter{fn: log}
	code, err := c.Run(ctx, "bash -s", strings.NewReader(script), lw)
	lw.flush()
	if err != nil {
		return fmt.Errorf("не удалось настроить nginx на сервере: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("настройка nginx на сервере не удалась (код %d), подробности выше", code)
	}
	return nil
}
