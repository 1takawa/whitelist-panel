#!/usr/bin/env bash
#
# Whitelist Panel — установщик (Ubuntu 22.04/24.04, Debian 12, x86_64).
#
#   sudo bash install.sh [--reality-domain reality.example.com | --skip-reality] --panel-domain panel.example.com [опции]
#
# Без ключей в терминале установщик спросит два домена: REALITY (нода на этом же сервере, можно пропустить) и панели.
#
# Схема 443: nginx (stream) раздаёт по SNI: домены нод REALITY -> Xray (127.0.0.1:8444 и выше), остальное -> Caddy (127.0.0.1:7443).
# Caddy выпускает сертификаты (HTTP-01 на :80), отдаёт панель и маршруты ws/gRPC/XHTTP-подключений.
#
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PANEL_BIN_SRC="$SCRIPT_DIR/anypanel"

XRAY_VERSION="v26.10.31"
XRAY_URL="https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}/Xray-linux-64.zip"
XRAY_SHA256="e4bd9119f50ae7c41658b1abd0679ae06f98bd864a2907ff33f2f2cf7081b214"

ENV_FILE=/etc/anypanel.env
CONF_DIR=/etc/anypanel
ROUTES_DIR="$CONF_DIR/routes"
STATE_DIR=/var/lib/anypanel
CADDYFILE=/etc/caddy/Caddyfile
DECOY_DIR=/var/www/anypanel-decoy
PANEL_LISTEN=127.0.0.1:28270
CADDY_TLS_PORT=7443
XRAY_REALITY_PORT=8443
MONGO_URI=mongodb://127.0.0.1:27017
MONGO_DB=anypanel

PANEL_DOMAIN=""
REALITY_DOMAIN=""
ACME_EMAIL=""
ADMIN_USER=admin
ADMIN_USER_SET=0
ADMIN_PASS="${ANYPANEL_ADMIN_PASS:-}"
SKIP_DNS=0
SKIP_REALITY=0
CHECK_ONLY=0
ASSUME_YES=0

if [[ -t 1 ]]; then C_G=$'\e[32m'; C_Y=$'\e[33m'; C_R=$'\e[31m'; C_B=$'\e[1m'; C_C=$'\e[1;36m'; C_V=$'\e[1;33m'; C_P=$'\e[1;32m'; C_0=$'\e[0m'; else C_G=""; C_Y=""; C_R=""; C_B=""; C_C=""; C_V=""; C_P=""; C_0=""; fi
log()  { printf '%s==>%s %s\n' "$C_G" "$C_0" "$*"; }
warn() { printf '%s!!%s  %s\n' "$C_Y" "$C_0" "$*" >&2; }
die()  { printf '%sОшибка:%s %s\n' "$C_R" "$C_0" "$*" >&2; exit 1; }
trap 'printf "%sСбой на строке %s: %s%s\n" "$C_R" "$LINENO" "$BASH_COMMAND" "$C_0" >&2' ERR

usage() {
  cat <<'EOF'
Whitelist Panel — установщик

  sudo bash install.sh [--reality-domain D | --skip-reality] --panel-domain D [опции]
  sudo bash install.sh upgrade        # обновить установленную панель до версии из этого каталога (с откатом при сбое)

Домены (A-записи на IP этого сервера). Без ключей в терминале установщик спросит их сам:
  --reality-domain D    домен REALITY: на нём сразу поднимется нода VLESS+REALITY на этом сервере
  --skip-reality        не ставить ноду сейчас (её можно поставить позже: «Серверы» -> «Поставить ноду»)
  --panel-domain D      домен панели и подписок (HTTPS через Caddy), обязателен

Опции:
  --email EMAIL         e-mail для Let's Encrypt (необязательно)
  --admin-user NAME     логин администратора (по умолчанию admin; без ключа в терминале установщик спросит)
  --admin-pass PASS     пароль (от 10 символов); лучше через переменную ANYPANEL_ADMIN_PASS; без ключа в терминале установщик спросит, Enter — сгенерировать
  --skip-dns-check      не проверять DNS (например, за Cloudflare)
  --check               только проверить требования и выйти
  -y, --yes             не задавать вопросов
  -h, --help            справка
EOF
}

MODE=install
if [[ "${1:-}" == upgrade ]]; then MODE=upgrade; shift; fi

while [[ $# -gt 0 ]]; do
  case "$1" in
    --panel-domain)   PANEL_DOMAIN="${2:?}"; shift 2 ;;
    --reality-domain) REALITY_DOMAIN="${2:?}"; shift 2 ;;
    --email)          ACME_EMAIL="${2:?}"; shift 2 ;;
    --admin-user)     ADMIN_USER="${2:?}"; ADMIN_USER_SET=1; shift 2 ;;
    --admin-pass)     ADMIN_PASS="${2:?}"; shift 2 ;;
    --skip-reality)   SKIP_REALITY=1; REALITY_DOMAIN=""; shift ;;
    --skip-dns-check) SKIP_DNS=1; shift ;;
    --check)          CHECK_ONLY=1; shift ;;
    -y|--yes)         ASSUME_YES=1; shift ;;
    -h|--help)        usage; exit 0 ;;
    *) die "неизвестный аргумент: $1 (см. --help)" ;;
  esac
done

# ---------- проверки ----------
valid_domain() {
  local d="$1"
  (( ${#d} <= 253 )) || return 1
  [[ "$d" =~ ^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+([a-z]{2,63}|xn--[a-z0-9-]{1,59})$ ]]
}

need_root() { [[ $EUID -eq 0 ]] || die "запустите от root (sudo bash install.sh ...)"; }

check_os() {
  [[ "$(uname -m)" == x86_64 ]] || die "бинарники собраны под x86_64, у вас $(uname -m)"
  # shellcheck disable=SC1091
  . /etc/os-release
  case "${ID:-}:${VERSION_CODENAME:-}" in
    ubuntu:jammy|ubuntu:noble) MONGO_APT="https://repo.mongodb.org/apt/ubuntu ${VERSION_CODENAME}/mongodb-org/8.0 multiverse" ;;
    debian:bookworm)           MONGO_APT="https://repo.mongodb.org/apt/debian bookworm/mongodb-org/8.0 main" ;;
    *) die "поддерживаются Ubuntu 22.04/24.04 и Debian 12, у вас ${PRETTY_NAME:-неизвестная ОС}" ;;
  esac
  command -v systemctl >/dev/null || die "нужен systemd"
}

# Без ключей в терминале спрашивает домены: сначала REALITY (его можно пропустить), потом панели.
ask_domains() {
  if [[ -t 0 && -t 1 ]]; then
    if [[ -z "$REALITY_DOMAIN" && "$SKIP_REALITY" -eq 0 ]]; then
      printf '\n1) Домен REALITY для ноды на этом сервере (A-запись на IP сервера).\n   Enter — пропустить: ноду можно поставить позже в панели («Серверы» -> «Поставить ноду»).\n   Домен REALITY: '
      read -r REALITY_DOMAIN || true
      [[ -n "${REALITY_DOMAIN// }" ]] || SKIP_REALITY=1
    fi
    if [[ -z "$PANEL_DOMAIN" ]]; then
      printf '\n2) Домен панели и подписок (A-запись на IP сервера).\n   Домен панели: '
      read -r PANEL_DOMAIN || true
    fi
  fi
  PANEL_DOMAIN="${PANEL_DOMAIN,,}"; REALITY_DOMAIN="${REALITY_DOMAIN,,}"
  PANEL_DOMAIN="${PANEL_DOMAIN// }"; REALITY_DOMAIN="${REALITY_DOMAIN// }"
  [[ -n "$PANEL_DOMAIN" ]] || die "укажите домен панели: --panel-domain (и, если нужна нода на этом сервере, --reality-domain)"
  valid_domain "$PANEL_DOMAIN" || die "некорректный домен панели: $PANEL_DOMAIN"
  if [[ -n "$REALITY_DOMAIN" ]]; then
    valid_domain "$REALITY_DOMAIN" || die "некорректный домен REALITY: $REALITY_DOMAIN"
    [[ "$PANEL_DOMAIN" != "$REALITY_DOMAIN" ]] || die "домены панели и REALITY должны быть разными"
  fi
  [[ "$ADMIN_USER" =~ ^[A-Za-z0-9_]+$ ]] || die "логин администратора: только буквы, цифры и _"
  [[ -z "$ADMIN_PASS" || ${#ADMIN_PASS} -ge 10 ]] || die "пароль администратора должен быть не короче 10 символов"
}

# Логин и пароль панели: в терминале без ключей можно выбрать свои, а Enter оставляет admin и пароль, который сгенерирует установщик.
ask_admin() {
  [[ -t 0 && -t 1 ]] || return 0
  local u p p2
  if [[ "$ADMIN_USER_SET" -eq 0 ]]; then
    while true; do
      printf '\n3) Логин панели. Enter — admin.\n   Логин: '
      read -r u || true
      u="${u// }"
      if [[ -z "$u" ]]; then break; fi
      if [[ "$u" =~ ^[A-Za-z0-9_]+$ ]]; then ADMIN_USER="$u"; break; fi
      warn "логин: только буквы, цифры и _"
    done
  fi
  if [[ -z "$ADMIN_PASS" ]]; then
    while true; do
      printf '\n4) Пароль панели (от 10 символов). Enter — сгенерировать.\n   Пароль: '
      read -rs p || true
      printf '\n'
      if [[ -z "$p" ]]; then break; fi
      if [[ ${#p} -lt 10 ]]; then warn "пароль должен быть не короче 10 символов"; continue; fi
      printf '   Повторите пароль: '
      read -rs p2 || true
      printf '\n'
      if [[ "$p" == "$p2" ]]; then ADMIN_PASS="$p"; break; fi
      warn "пароли не совпали"
    done
  fi
}

ensure_tools() {
  command -v curl >/dev/null && return 0
  [[ "$CHECK_ONLY" -eq 1 ]] && return 0
  DEBIAN_FRONTEND=noninteractive apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl ca-certificates >/dev/null
}

public_ip() {
  local svc ip
  for svc in https://api.ipify.org https://ifconfig.me/ip https://icanhazip.com; do
    ip="$(curl -4 -fsS --max-time 5 "$svc" 2>/dev/null | tr -d '[:space:]')" || true
    if [[ "$ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then printf '%s' "$ip"; return 0; fi
  done
  return 1
}

check_dns() {
  [[ "$SKIP_DNS" -eq 1 ]] && { warn "проверка DNS пропущена (--skip-dns-check)"; return 0; }
  command -v curl >/dev/null || { warn "curl не установлен — проверка DNS пропущена"; return 0; }
  local ip d resolved
  ip="$(public_ip)" || { warn "не удалось определить публичный IP — проверка DNS пропущена"; return 0; }
  for d in "$PANEL_DOMAIN" ${REALITY_DOMAIN:+"$REALITY_DOMAIN"}; do
    resolved="$(getent ahostsv4 "$d" | awk '{print $1}' | sort -u || true)"
    [[ -n "$resolved" ]] || die "домен $d не резолвится. Создайте A-запись на $ip (или --skip-dns-check)"
    grep -qx "$ip" <<<"$resolved" || die "$d указывает на $(tr '\n' ' ' <<<"$resolved")— а IP сервера $ip. Исправьте A-запись (или --skip-dns-check)"
    log "DNS: $d -> $ip"
  done
}

port_busy_by_other() {
  local line
  line="$(ss -H -ltnp "sport = :$1" 2>/dev/null || true)"
  [[ -n "$line" ]] && ! grep -qE '"(caddy|nginx)"' <<<"$line"
}

check_ports() {
  local p
  for p in 80 443; do
    port_busy_by_other "$p" && die "TCP-порт $p занят другим процессом. Нужны свободные 80 и 443"
  done
  return 0
}

check_not_installed() {
  if [[ -e "$ENV_FILE" || -e /usr/local/bin/anypanel || -d "$STATE_DIR" ]]; then
    die "Whitelist Panel уже установлена (найден $ENV_FILE, /usr/local/bin/anypanel или $STATE_DIR). Установщик ничего не перезаписывает"
  fi
  if systemctl is-active --quiet any-panel.service 2>/dev/null; then
    die "на сервере работает прежняя панель (any-panel.service). Остановите и отключите её: systemctl disable --now any-panel hysteria-server hysteria-unrestricted sing-box-torrent"
  fi
  return 0
}

check_bundle() {
  [[ -x "$PANEL_BIN_SRC" ]] || die "рядом со скриптом нет бинарника anypanel ($PANEL_BIN_SRC)"
  return 0
}

# ---------- установка пакетов ----------
apt_prepare() {
  export DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a NEEDRESTART_SUSPEND=1
  log "Пакеты: базовые зависимости"
  apt-get update -qq
  apt-get install -y -qq ca-certificates curl gnupg openssl unzip iproute2 >/dev/null
}

install_mongo() {
  if command -v mongod >/dev/null && command -v mongosh >/dev/null && command -v mongodump >/dev/null; then
    log "MongoDB уже установлена"
  else
    log "MongoDB 8.0: репозиторий и установка"
    curl -fsSL https://www.mongodb.org/static/pgp/server-8.0.asc | gpg --dearmor --yes -o /usr/share/keyrings/mongodb-server-8.0.gpg
    chmod 0644 /usr/share/keyrings/mongodb-server-8.0.gpg
    printf 'deb [arch=amd64 signed-by=/usr/share/keyrings/mongodb-server-8.0.gpg] %s\n' "$MONGO_APT" > /etc/apt/sources.list.d/mongodb-org-8.0.list
    apt-get update -qq
    apt-get install -y -qq mongodb-org >/dev/null
  fi
  systemctl enable --now mongod.service >/dev/null
  local i
  for i in $(seq 1 30); do
    mongosh --quiet --eval 'quit(db.adminCommand({ping:1}).ok === 1 ? 0 : 1)' >/dev/null 2>&1 && return 0
    sleep 1
  done
  die "MongoDB не отвечает (journalctl -u mongod)"
}

install_nginx() {
  log "nginx (stream): SNI-маршрутизация на 443"
  apt-get install -y -qq nginx libnginx-mod-stream >/dev/null
  write_nginx_conf
  # Порт 443 должен достаться nginx: если на сервере уже работает Caddy (старая установка), он держит 443.
  # Caddy запустится позже уже с новой конфигурацией (только 80 и 7443).
  if systemctl is-active --quiet caddy.service; then
    log "Останавливаю действующий Caddy: он займёт порты заново с новой конфигурацией"
    systemctl stop caddy.service
  fi
  systemctl enable nginx.service >/dev/null
  nginx -t
  systemctl restart nginx.service
}

write_nginx_conf() {
  [[ -f /etc/nginx/nginx.conf && ! -f /etc/nginx/nginx.conf.anypanel-orig ]] && cp -a /etc/nginx/nginx.conf /etc/nginx/nginx.conf.anypanel-orig
  # Только stream: http-сайты nginx не нужны, 80-й порт принадлежит Caddy.
  cat > /etc/nginx/nginx.conf <<EOF
# Managed by Whitelist Panel installer. Original: nginx.conf.anypanel-orig
user www-data;
worker_processes auto;
worker_rlimit_nofile 65535;
pid /run/nginx.pid;
error_log /var/log/nginx/error.log warn;
include /etc/nginx/modules-enabled/*.conf;

events { worker_connections 4096; }

stream {
    # Домены нод REALITY панель дописывает сама (Серверы -> Поставить ноду): включения лежат в /etc/anypanel/nginx.
    map \$ssl_preread_server_name \$anypanel_upstream {
        default            web;
    }
    upstream web     { server 127.0.0.1:${CADDY_TLS_PORT}; }

    server {
        listen 443 reuseport;
        listen [::]:443 reuseport;
        ssl_preread on;
        proxy_pass \$anypanel_upstream;
        proxy_protocol on;
        proxy_connect_timeout 10s;
    }
}
EOF
}

# Нужен Caddy >= 2.7: в нём есть listener wrapper proxy_protocol (в пакете Ubuntu 2.6.2 его нет).
caddy_version_ok() {
  command -v caddy >/dev/null || return 1
  local v major minor
  v="$(caddy version 2>/dev/null | awk '{print $1}')" || return 1
  v="${v#v}"; major="${v%%.*}"; minor="${v#*.}"; minor="${minor%%.*}"
  [[ "$major" =~ ^[0-9]+$ && "$minor" =~ ^[0-9]+$ ]] || return 1
  (( major > 2 || (major == 2 && minor >= 7) ))
}

install_caddy() {
  if caddy_version_ok; then
    log "Caddy $(caddy version | awk '{print $1}') уже установлен"
    return 0
  fi
  log "Caddy: установка официального пакета (нужна версия 2.7+)"
  curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/gpg.key | gpg --dearmor --yes -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
  curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt > /etc/apt/sources.list.d/caddy-stable.list
  apt-get update -qq
  apt-get install -y -qq caddy >/dev/null
  caddy_version_ok || die "не удалось установить Caddy 2.7+ (сейчас: $(caddy version 2>/dev/null | awk '{print $1}'))"
}

install_xray() {
  # sed (не head): при pipefail ранний выход головы даёт SIGPIPE и ложную ошибку конвейера
  if [[ -x /usr/local/bin/xray ]] && /usr/local/bin/xray version 2>/dev/null | sed -n 1p | grep -q "${XRAY_VERSION#v}"; then
    log "Xray ${XRAY_VERSION} уже установлен"
    return 0
  fi
  log "Xray ${XRAY_VERSION}: загрузка и проверка контрольной суммы"
  local tmp; tmp="$(mktemp -d)"
  curl -fsSL --retry 3 -o "$tmp/xray.zip" "$XRAY_URL"
  echo "${XRAY_SHA256}  $tmp/xray.zip" | sha256sum -c --quiet - || die "контрольная сумма Xray не совпала"
  unzip -q -o "$tmp/xray.zip" -d "$tmp/x"
  install -m 0755 "$tmp/x/xray" /usr/local/bin/xray
  install -d -m 0755 /usr/local/share/xray
  for f in geoip.dat geosite.dat; do [[ -f "$tmp/x/$f" ]] && install -m 0644 "$tmp/x/$f" "/usr/local/share/xray/$f"; done
  rm -rf "$tmp"
  /usr/local/bin/xray version | sed -n 1p
}

# ---------- панель ----------
install_panel() {
  log "Панель: бинарник, каталоги, окружение"
  install -m 0755 "$PANEL_BIN_SRC" /usr/local/bin/anypanel
  install -d -m 0700 "$STATE_DIR" "$STATE_DIR/xray"
  install -d -m 0755 "$CONF_DIR" "$ROUTES_DIR"
  printf '# Managed by Whitelist Panel. Do not edit: this file is regenerated on every change.\n' > "$ROUTES_DIR/inbounds.caddy"

  ( umask 077; cat > "$ENV_FILE" <<EOF
LISTEN_ADDR=$PANEL_LISTEN
MONGO_URI=$MONGO_URI
MONGO_DATABASE=$MONGO_DB
STATE_DIR=$STATE_DIR
XRAY_BIN=/usr/local/bin/xray
XRAY_API_PORT=62789
COOKIE_SECURE=true
PANEL_PUBLIC_URL=https://$PANEL_DOMAIN
CADDY_ROUTES_FILE=$ROUTES_DIR/inbounds.caddy
CADDY_RELOAD_CMD="systemctl reload caddy"
# Отдельные домены REALITY у каскадов: панель сама дописывает include-строки в nginx.conf и Caddyfile
NGINX_SNI_DIR=$CONF_DIR/nginx
CADDY_SITES_DIR=$CONF_DIR/sites
EOF
  )
  chmod 600 "$ENV_FILE"

  cat > /etc/systemd/system/anypanel.service <<'EOF'
[Unit]
Description=Whitelist Panel (Xray management panel)
After=network-online.target mongod.service
Wants=network-online.target mongod.service

[Service]
Type=simple
User=root
EnvironmentFile=/etc/anypanel.env
ExecStart=/usr/local/bin/anypanel serve
Restart=on-failure
RestartSec=3
LimitNOFILE=1048576
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
LockPersonality=true
ReadWritePaths=/etc/anypanel /var/lib/anypanel -/etc/nginx -/etc/caddy

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
}

write_caddyfile() {
  log "Caddy: $CADDYFILE (панель $PANEL_DOMAIN)"
  install -d -m 0755 "$DECOY_DIR"
  cat > "$DECOY_DIR/index.html" <<'EOF'
<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Welcome</title>
<meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="font-family:system-ui,sans-serif;max-width:40rem;margin:15vh auto;padding:0 1rem;color:#333">
<h1>It works</h1><p>This site is under construction.</p></body></html>
EOF
  [[ -f "$CADDYFILE" && ! -f "$CADDYFILE.anypanel-orig" ]] && cp -a "$CADDYFILE" "$CADDYFILE.anypanel-orig"
  local tmp; tmp="$(mktemp)"
  {
    printf '{\n'
    [[ -n "$ACME_EMAIL" ]] && printf '    email %s\n' "$ACME_EMAIL"
    cat <<EOF
    # TLS обслуживает Caddy на loopback-порту; наружу 443 отдаёт nginx (stream).
    https_port ${CADDY_TLS_PORT}
    auto_https disable_redirects
    servers :${CADDY_TLS_PORT} {
        listener_wrappers {
            proxy_protocol {
                allow 127.0.0.1/32
            }
            tls
        }
        protocols h1 h2
    }
}

http://${PANEL_DOMAIN} {
    redir https://{host}{uri} permanent
}

${PANEL_DOMAIN} {
    encode zstd gzip
    header {
        Strict-Transport-Security "max-age=31536000; includeSubDomains"
        X-Content-Type-Options "nosniff"
        X-Frame-Options "DENY"
    }
    # Маршруты ws/gRPC/XHTTP-подключений генерирует панель.
    import ${ROUTES_DIR}/*.caddy
    handle {
        reverse_proxy ${PANEL_LISTEN}
    }
}

# Сайты-прикрытия для нод REALITY (сюда Xray направляет чужие соединения) панель добавляет сама: /etc/anypanel/sites/*.caddy
EOF
  } > "$tmp"
  caddy validate --config "$tmp" --adapter caddyfile >/dev/null 2>&1 || { caddy validate --config "$tmp" --adapter caddyfile || true; rm -f "$tmp"; die "Caddyfile не прошёл проверку"; }
  install -m 0644 "$tmp" "$CADDYFILE"
  rm -f "$tmp"
}

open_firewall() {
  if command -v ufw >/dev/null && [[ "$(ufw status 2>/dev/null)" == "Status: active"* ]]; then
    log "ufw: открываю 80/tcp и 443/tcp"
    ufw allow 80/tcp >/dev/null
    ufw allow 443/tcp >/dev/null
    ufw deny ${CADDY_TLS_PORT}/tcp >/dev/null || true   # внутренний порт Caddy, наружу не нужен
  else
    warn "ufw не активен — убедитесь, что в файрволе хостера открыты 80/tcp и 443/tcp"
  fi
}

load_env() { set -a; . "$ENV_FILE"; set +a; }

# Ставит ноду на этом сервере тем же способом, что кнопка «Поставить ноду»: входит в панель под администратором и
# вызывает её API. Сбой не останавливает установку: ноду можно поставить позже из панели.
json_escape() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'; }

create_main_node() {
  local base="http://$PANEL_LISTEN" hdr cookie code body
  hdr="$(curl -sS -D - -o /dev/null -H 'Content-Type: application/json' -H 'X-Requested-With: anypanel' \
    -d "{\"username\":\"$(json_escape "$ADMIN_USER")\",\"password\":\"$(json_escape "$ADMIN_PASS")\"}" "$base/api/auth/login" 2>/dev/null || true)"
  # Cookie сессии помечена Secure: curl не отправил бы её по http, поэтому передаём вручную.
  cookie="$(printf '%s' "$hdr" | sed -n 's/^[Ss]et-[Cc]ookie: \(ap_session=[^;]*\).*/\1/p' | tr -d '\r' | head -n1)"
  if [[ -z "$cookie" ]]; then
    warn "не удалось войти в панель для установки ноды: поставьте её позже («Серверы» -> «Поставить ноду»)"
    return 1
  fi
  body="$(mktemp)"
  code="$(curl -sS -o "$body" -w '%{http_code}' --max-time 180 -H 'Content-Type: application/json' -H 'X-Requested-With: anypanel' \
    -H "Cookie: $cookie" -d "{\"domain\":\"$(json_escape "$REALITY_DOMAIN")\"}" "$base/api/main-node" 2>/dev/null || true)"
  if [[ "$code" == 200 ]]; then
    rm -f "$body"
    return 0
  fi
  warn "нода на $REALITY_DOMAIN не поставилась (HTTP $code): $(tr -d '\n' < "$body" | cut -c1-300)"
  warn "проверьте A-запись домена и поставьте ноду позже: «Серверы» -> «Поставить ноду»"
  rm -f "$body"
  return 1
}

wait_health() {
  local i
  for i in $(seq 1 30); do
    curl -fsS --max-time 2 "http://$PANEL_LISTEN/api/health" >/dev/null 2>&1 && return 0
    sleep 1
  done
  die "панель не ответила на /api/health (journalctl -u anypanel -n 50)"
}

create_admin() {
  if [[ -z "$ADMIN_PASS" ]]; then ADMIN_PASS="$(openssl rand -base64 18 | tr '+/' 'Aa')"; fi
  log "Администратор: $ADMIN_USER"
  ( load_env; printf '%s' "$ADMIN_PASS" | /usr/local/bin/anypanel init-admin "$ADMIN_USER" )
}

# Обновление: заменяет только бинарник панели. Конфиги, база, домены и сертификаты не трогаются.
# Если после перезапуска панель не отвечает, возвращается предыдущая версия.
upgrade() {
  need_root
  check_bundle
  [[ -f "$ENV_FILE" && -x /usr/local/bin/anypanel ]] || die "панель не установлена: нечего обновлять"
  local before; before="$(/usr/local/bin/anypanel version 2>/dev/null || echo '?')"
  log "Обновление панели: $before -> $("$PANEL_BIN_SRC" version 2>/dev/null || echo '?')"
  # Установки до 0.14 не знают про отдельные домены REALITY у каскадов: дописываем каталоги в окружение.
  if ! grep -q '^NGINX_SNI_DIR=' "$ENV_FILE"; then
    printf 'NGINX_SNI_DIR=%s\nCADDY_SITES_DIR=%s\n' "$CONF_DIR/nginx" "$CONF_DIR/sites" >> "$ENV_FILE"
  fi
  # Панель дописывает nginx.conf и Caddyfile для доменов нод и каскадов; под ProtectSystem=full каталог /etc для записи закрыт.
  local unit=/etc/systemd/system/anypanel.service
  if [[ -f "$unit" ]] && ! grep -q '^ReadWritePaths=.*/etc/nginx' "$unit"; then
    log "Служба: разрешаю панели менять конфигурацию nginx и Caddy (домены нод)"
    sed -i 's|^ReadWritePaths=.*|ReadWritePaths=/etc/anypanel /var/lib/anypanel -/etc/nginx -/etc/caddy|' "$unit"
    systemctl daemon-reload
  fi
  cp -a /usr/local/bin/anypanel /usr/local/bin/anypanel.prev
  install -m 0755 "$PANEL_BIN_SRC" /usr/local/bin/anypanel.new
  mv -f /usr/local/bin/anypanel.new /usr/local/bin/anypanel
  systemctl restart anypanel.service
  if ! wait_health_quiet; then
    warn "панель не поднялась после обновления — возвращаю версию $before"
    mv -f /usr/local/bin/anypanel.prev /usr/local/bin/anypanel
    systemctl restart anypanel.service
    die "обновление отменено (журнал: journalctl -u anypanel -n 50)"
  fi
  log "Готово: работает версия $(/usr/local/bin/anypanel version). Предыдущая сохранена как /usr/local/bin/anypanel.prev"
}

wait_health_quiet() {
  local i
  for i in $(seq 1 30); do
    curl -fsS --max-time 2 "http://$PANEL_LISTEN/api/health" >/dev/null 2>&1 && return 0
    sleep 1
  done
  return 1
}

main() {
  if [[ "$MODE" == upgrade ]]; then upgrade; return 0; fi
  need_root
  check_os
  ask_domains
  ask_admin
  check_bundle
  ensure_tools
  check_dns
  check_ports
  check_not_installed
  if [[ "$CHECK_ONLY" -eq 1 ]]; then log "Проверка пройдена: можно устанавливать (панель $PANEL_DOMAIN, нода: ${REALITY_DOMAIN:-пропущена})"; return 0; fi

  apt_prepare
  install_mongo
  install_nginx
  install_caddy
  install_xray
  install_panel
  create_admin
  ( load_env; /usr/local/bin/anypanel set-domains "$PANEL_DOMAIN" "" )  # домены нод хранятся у самих нод
  write_caddyfile

  log "Запуск сервисов"
  # Caddy раньше панели: при старте панель перезагружает Caddy для маршрутов подключений.
  systemctl enable caddy.service >/dev/null
  systemctl restart caddy.service
  systemctl enable --now anypanel.service >/dev/null
  wait_health
  open_firewall
  if [[ -n "$REALITY_DOMAIN" ]]; then
    log "Нода на этом сервере: $REALITY_DOMAIN"
    create_main_node || true
  fi

  local bar="${C_B}==============================================${C_0}"
  printf '\n%sWhitelist Panel установлена.%s\n\n' "$C_G$C_B" "$C_0"
  printf '%s\n' "$bar"
  printf '  Панель:  %s%s%s\n' "$C_C" "https://$PANEL_DOMAIN" "$C_0"
  printf '  Логин:   %s%s%s\n' "$C_V" "$ADMIN_USER" "$C_0"
  printf '  Пароль:  %s%s%s\n' "$C_P" "$ADMIN_PASS" "$C_0"
  printf '%s\n\n' "$bar"
}

main
