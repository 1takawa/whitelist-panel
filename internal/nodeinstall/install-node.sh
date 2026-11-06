#!/usr/bin/env bash
#
# Whitelist Panel — установщик дополнительного сервера (агента). Ubuntu 22.04/24.04, Debian 12, x86_64.
#
#   sudo bash install-node.sh --token ТОКЕН [--port 8765] [--host ДОМЕН_ИЛИ_IP]
#
# На сервере работают только Xray и агент; панель, база и сайты остаются на главном сервере. Токен выдаёт панель
# при добавлении сервера (раздел «Серверы»). Установщик печатает отпечаток сертификата агента: вставьте его в
# карточку сервера в панели, чтобы соединение было защищено от подмены.
#
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_SRC="$SCRIPT_DIR/anypanel"

XRAY_VERSION="v26.10.31"
XRAY_URL="https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}/Xray-linux-64.zip"
XRAY_SHA256="e4bd9119f50ae7c41658b1abd0679ae06f98bd864a2907ff33f2f2cf7081b214"

ENV_FILE=/etc/anypanel-agent.env
STATE_DIR=/var/lib/anypanel-agent
UNIT=/etc/systemd/system/anypanel-agent.service
DEFAULT_PORT=8765

TOKEN="${ANYPANEL_AGENT_TOKEN:-}"
PORT=$DEFAULT_PORT
HOSTS=""
ASSUME_YES=0
ANYPANEL_OPEN_PORTS="${ANYPANEL_OPEN_PORTS:-}"
REALITY_DOMAIN="${ANYPANEL_REALITY_DOMAIN:-}"
# Не проверять A-запись домена заранее (админ создал её сам и знает, что она скоро появится): вместо отказа скрипт ждёт её.
SKIP_DNS_CHECK="${ANYPANEL_SKIP_DNS_CHECK:-}"

if [[ -t 1 ]]; then C_G=$'\e[32m'; C_Y=$'\e[33m'; C_R=$'\e[31m'; C_B=$'\e[1m'; C_0=$'\e[0m'; else C_G=""; C_Y=""; C_R=""; C_B=""; C_0=""; fi
log()  { printf '%s==>%s %s\n' "$C_G" "$C_0" "$*"; }
warn() { printf '%s!!%s  %s\n' "$C_Y" "$C_0" "$*" >&2; }
die()  { printf '%sОшибка:%s %s\n' "$C_R" "$C_0" "$*" >&2; exit 1; }
trap 'printf "%sСбой на строке %s: %s%s\n" "$C_R" "$LINENO" "$BASH_COMMAND" "$C_0" >&2' ERR

usage() {
  cat <<'EOF'
Whitelist Panel — установщик дополнительного сервера

  sudo bash install-node.sh --token ТОКЕН [опции]
  sudo bash install-node.sh upgrade     # обновить агент до версии из этого каталога
  sudo bash install-node.sh reality --reality-domain D   # только REALITY-сайт (nginx + заглушка) на уже установленной ноде

Опции:
  --token T       токен из панели (минимум 32 символа); можно передать через ANYPANEL_AGENT_TOKEN
  --port N        порт агента (по умолчанию 8765); его нужно открыть для главного сервера
  --host H        домен или IP этого сервера (попадёт в сертификат агента; необязательно)
  --reality-domain D  домен ноды для REALITY (A-запись на этот сервер): ставит nginx, сертификат и сайт-заглушку по схеме с nginx
  --yes           не задавать вопросов
EOF
}

MODE=install
if [[ "${1:-}" == upgrade ]]; then MODE=upgrade; shift; fi
if [[ "${1:-}" == reality ]]; then MODE=reality; shift; fi
while [[ $# -gt 0 ]]; do
  case "$1" in
    --token) TOKEN="${2:?}"; shift 2 ;;
    --port)  PORT="${2:?}"; shift 2 ;;
    --host)  HOSTS="${2:?}"; shift 2 ;;
    --reality-domain) REALITY_DOMAIN="${2:?}"; shift 2 ;;
    --yes|-y) ASSUME_YES=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "неизвестный параметр: $1 (см. --help)" ;;
  esac
done

need_root() { [[ $EUID -eq 0 ]] || die "запустите от root: sudo bash install-node.sh ..."; }

check_os() {
  [[ "$(uname -m)" == x86_64 ]] || die "поддерживается только x86_64"
  [[ -r /etc/os-release ]] || die "не удалось определить систему"
  # shellcheck disable=SC1091
  . /etc/os-release
  case "${ID}:${VERSION_CODENAME:-}" in
    ubuntu:jammy|ubuntu:noble|debian:bookworm) ;;
    *) die "поддерживаются Ubuntu 22.04/24.04 и Debian 12 (у вас: ${PRETTY_NAME:-?})" ;;
  esac
  command -v systemctl >/dev/null || die "нужен systemd"
}

check_args() {
  [[ ${#TOKEN} -ge 32 ]] || die "нужен --token (минимум 32 символа): скопируйте его из панели при добавлении сервера"
  [[ "$TOKEN" =~ ^[A-Za-z0-9]+$ ]] || die "токен должен состоять из латинских букв и цифр"
  [[ "$PORT" =~ ^[0-9]+$ && "$PORT" -ge 1 && "$PORT" -le 65535 ]] || die "порт должен быть числом 1–65535"
  [[ -f "$BIN_SRC" ]] || die "рядом со скриптом нет файла anypanel (скопируйте весь каталог dist)"
  if ss -ltn 2>/dev/null | awk '{print $4}' | grep -qE "[:.]${PORT}\$"; then
    if [[ ! -e "$UNIT" ]]; then die "порт $PORT уже занят: выберите другой через --port"; fi
  fi
}

ensure_tools() {
  local need=() t
  for t in curl unzip; do command -v "$t" >/dev/null || need+=("$t"); done
  command -v sha256sum >/dev/null || die "нет sha256sum"
  if ((${#need[@]})); then
    log "Установка: ${need[*]}"
    export DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a
    apt-get update -qq >/dev/null
    apt-get install -y -qq "${need[@]}" >/dev/null
  fi
}

install_xray() {
  if [[ -x /usr/local/bin/xray ]] && /usr/local/bin/xray version 2>/dev/null | sed -n 1p | grep -q "${XRAY_VERSION#v}"; then
    log "Xray ${XRAY_VERSION} уже установлен"
    return
  fi
  log "Xray ${XRAY_VERSION}: загрузка и проверка контрольной суммы"
  local tmp; tmp="$(mktemp -d)"
  curl -fsSL --retry 3 -o "$tmp/xray.zip" "$XRAY_URL"
  echo "${XRAY_SHA256}  $tmp/xray.zip" | sha256sum -c --quiet - || die "контрольная сумма Xray не совпала"
  unzip -q -o "$tmp/xray.zip" -d "$tmp/x"
  install -m 0755 "$tmp/x/xray" /usr/local/bin/xray
  install -d -m 0755 /usr/local/share/xray
  local f
  for f in geoip.dat geosite.dat; do [[ -f "$tmp/x/$f" ]] && install -m 0644 "$tmp/x/$f" "/usr/local/share/xray/$f"; done
  rm -rf "$tmp"
  /usr/local/bin/xray version | sed -n 1p
}

write_files() {
  install -m 0755 "$BIN_SRC" /usr/local/bin/anypanel
  install -d -m 0700 "$STATE_DIR" "$STATE_DIR/xray"
  umask 077
  {
    echo "AGENT_TOKEN=$TOKEN"
    echo "AGENT_LISTEN=0.0.0.0:$PORT"
    echo "STATE_DIR=$STATE_DIR"
    echo "XRAY_BIN=/usr/local/bin/xray"
    [[ -n "$HOSTS" ]] && echo "AGENT_HOSTS=$HOSTS"
  } > "$ENV_FILE"
  chmod 0600 "$ENV_FILE"
  umask 022
  cat > "$UNIT" <<'EOF'
[Unit]
Description=Whitelist Panel agent (Xray controlled by a remote panel)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
EnvironmentFile=/etc/anypanel-agent.env
ExecStart=/usr/local/bin/anypanel agent
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
ReadWritePaths=/var/lib/anypanel-agent

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
}

fingerprint() {
  set -a
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  set +a
  /usr/local/bin/anypanel agent --print-fingerprint
}

wait_listening() {
  local i
  for i in $(seq 1 20); do
    ss -ltn 2>/dev/null | awk '{print $4}' | grep -qE "[:.]${PORT}\$" && return 0
    sleep 1
  done
  return 1
}

ufw_active() { command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; }

# С включённым ufw порт агента закрыт для всех. Агента открываем только для панели: её адрес виден по SSH-сессии, из
# которой запущен установщик ($SSH_CLIENT). Порты подключений (ANYPANEL_OPEN_PORTS, через запятую) нужны клиентам, их
# открываем для всех. Другие файрволы (iptables, облачные правила провайдера) установщик не трогает.
open_firewall() {
  ufw_active || return 0
  local src="${SSH_CLIENT:-}" p; src="${src%% *}"
  if [[ "$src" =~ ^[0-9a-fA-F:.]+$ ]]; then
    log "ufw: открываю порт агента ${PORT}/tcp для панели ($src)"
    ufw allow from "$src" to any port "$PORT" proto tcp >/dev/null
  else
    log "ufw: открываю порт агента ${PORT}/tcp"
    ufw allow "${PORT}/tcp" >/dev/null
  fi
  for p in ${ANYPANEL_OPEN_PORTS//,/ }; do
    [[ "$p" =~ ^[0-9]+$ && "$p" -ge 1 && "$p" -le 65535 ]] || continue
    log "ufw: открываю порт подключений ${p}/tcp"
    ufw allow "${p}/tcp" >/dev/null
  done
}

firewall_hint() {
  if ufw_active; then
    log "Порты в ufw открыты. Если у провайдера есть свой файрвол, порт агента ${PORT}/tcp нужно открыть и там"
  else
    log "Не забудьте открыть порт агента ${PORT}/tcp (и порты подключений) в файрволе провайдера"
  fi
}

# ---- REALITY-сайт: схема nginx + SNI ------------------------------------------------------------------------------
# nginx (stream) принимает 443 и по SNI отдаёт соединения в Xray (127.0.0.1:8443, PROXY protocol). Xray с REALITY
# пересылает чужие соединения на сайт-заглушку (nginx, 127.0.0.1:9443, настоящий сертификат Let's Encrypt для домена
# REALITY), поэтому снаружи по этому имени виден обычный сайт. Содержимое сайта — случайный шаблон из репозитория
# готового набора (assets/fake-sites), как делает их установщик.
XRAY_REALITY_PORT=8443
DECOY_PORT=9443
FAKE_SITES_URL="https://github.com/mozaroc/3x-ui-pro/archive/refs/heads/main.tar.gz"

valid_domain() { [[ "$1" =~ ^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$ ]]; }

# Домен должен указывать на этот сервер, иначе Let's Encrypt откажет в сертификате.
# С ANYPANEL_SKIP_DNS_CHECK запись ждут до 12 минут: сертификат всё равно нельзя выпустить, пока домен не указывает сюда.
wait_reality_dns() {
  local d="$1" ip_local="$2" ip_pub="$3" a="" i
  log "Проверка A-записи пропущена по просьбе администратора: жду, пока $d начнёт указывать на этот сервер (до 12 минут)"
  for i in $(seq 1 72); do
    a="$(getent ahostsv4 "$d" 2>/dev/null | awk 'NR==1{print $1}')"
    if [[ -n "$a" && ( "$a" == "$ip_local" || "$a" == "$ip_pub" ) ]]; then
      log "Домен $d указывает на этот сервер ($a)"
      return 0
    fi
    if (( i % 6 == 0 )); then log "Жду A-запись $d… (прошло $((i / 6)) мин)"; fi
    sleep 10
  done
  die "за 12 минут домен $d так и не стал указывать на этот сервер (сейчас: ${a:-записи нет}). Проверьте A-запись и повторите установку"
}

check_reality_dns() {
  local d="$1" a ip_local ip_pub
  ip_local="$(ip route get 8.8.8.8 2>/dev/null | grep -Po -- 'src \K\S*' || true)"
  ip_pub="$(curl -fsS -m 8 https://ipv4.icanhazip.com 2>/dev/null | tr -d '[:space:]' || true)"
  if [[ -n "$SKIP_DNS_CHECK" ]]; then
    wait_reality_dns "$d" "$ip_local" "$ip_pub"
    return 0
  fi
  a="$(getent ahostsv4 "$d" 2>/dev/null | awk 'NR==1{print $1}')"
  [[ -n "$a" ]] || die "у домена $d нет A-записи: создайте запись типа A на адрес этого сервера и повторите"
  if [[ "$a" != "$ip_local" && "$a" != "$ip_pub" ]]; then
    die "домен $d указывает на $a, а адрес этого сервера: ${ip_pub:-$ip_local}. Исправьте A-запись и повторите (после смены DNS может пройти несколько минут)"
  fi
  log "Домен $d указывает на этот сервер ($a)"
}

port_listening() { ss -ltn 2>/dev/null | awk '{print $4}' | grep -qE "[:.]$1\$"; }

install_fake_site() {
  local tmp site; tmp="$(mktemp -d)"
  install -d -m 0755 /var/www/html
  if curl -fsSL --retry 2 -m 60 -o "$tmp/repo.tar.gz" "$FAKE_SITES_URL" \
     && tar -xzf "$tmp/repo.tar.gz" -C "$tmp" --strip-components=3 "3x-ui-pro-main/assets/fake-sites" 2>/dev/null \
     && compgen -G "$tmp/site-*/index.html" >/dev/null; then
    site="$(ls -d "$tmp"/site-*/ | shuf -n1)"
    log "Сайт-заглушка: случайный шаблон $(basename "$site") из готового набора"
    find /var/www/html -mindepth 1 -maxdepth 1 -exec rm -rf {} +
    cp -a "$site". /var/www/html/
  else
    warn "не удалось скачать шаблоны сайтов nginx-SNI: ставлю простую страницу-заглушку"
    cat > /var/www/html/index.html <<'HTML'
<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Welcome</title>
<meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="font-family:system-ui,sans-serif;max-width:40rem;margin:15vh auto;padding:0 1rem;color:#333">
<h1>Welcome</h1><p>This site is under construction.</p></body></html>
HTML
  fi
  chmod -R a+rX /var/www/html
  rm -rf "$tmp"
}

write_reality_nginx() {
  local d="$1" re
  re="${d//./\\.}" # точки домена в регулярном выражении nginx
  install -d /etc/nginx/stream-enabled /etc/nginx/sites-available /etc/nginx/sites-enabled
  cat > /etc/nginx/stream-enabled/stream.conf <<EOT
map \$ssl_preread_server_name \$sni_name {
    hostnames;
    ${d}      xray;
    default   xray;
}

upstream xray {
    server 127.0.0.1:${XRAY_REALITY_PORT};
}

server {
    proxy_protocol on;
    set_real_ip_from unix:;
    listen          443;
    listen          [::]:443;
    proxy_pass      \$sni_name;
    ssl_preread     on;
}
EOT
  grep -qF "stream { include /etc/nginx/stream-enabled/*.conf; }" /etc/nginx/nginx.conf \
    || echo "stream { include /etc/nginx/stream-enabled/*.conf; }" >> /etc/nginx/nginx.conf

  cat > /etc/nginx/sites-available/80.conf <<EOT
server {
    listen 80;
    listen [::]:80;
    server_name ${d};
    return 301 https://\$host\$request_uri;
}
EOT
  cat > "/etc/nginx/sites-available/${d}" <<EOT
server {
	server_tokens off;
	server_name ${d};
	listen ${DECOY_PORT} ssl http2;
	listen [::]:${DECOY_PORT} ssl http2;
	index index.html index.htm;
	root /var/www/html/;
	ssl_protocols TLSv1.2 TLSv1.3;
	ssl_ciphers HIGH:!aNULL:!eNULL:!MD5:!DES:!RC4:!ADH:!SSLv3:!EXP:!PSK:!DSS;
	ssl_certificate /etc/letsencrypt/live/${d}/fullchain.pem;
	ssl_certificate_key /etc/letsencrypt/live/${d}/privkey.pem;
	if (\$host !~* ^(.+\.)?${re}\$ ){return 444;}
	if (\$scheme ~* https) {set \$safe 1;}
	if (\$ssl_server_name !~* ^(.+\.)?${re}\$ ) {set \$safe "\${safe}0"; }
	if (\$safe = 10){return 444;}
	error_page 400 401 402 403 500 501 502 503 504 =404 /404;
	proxy_intercept_errors on;
	location / { try_files \$uri \$uri/ =404; }
}
EOT
  rm -f /etc/nginx/sites-enabled/default /etc/nginx/sites-available/default
  ln -sf /etc/nginx/sites-available/80.conf /etc/nginx/sites-enabled/80.conf
  ln -sf "/etc/nginx/sites-available/${d}" "/etc/nginx/sites-enabled/${d}"
}

setup_reality() {
  local d="$REALITY_DOMAIN"
  valid_domain "$d" || die "неверный домен REALITY: «$d»"
  d="${d,,}"; REALITY_DOMAIN="$d"
  export DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a
  log "REALITY-сайт для $d: nginx, certbot, сертификат, сайт-заглушка"
  apt-get update -qq >/dev/null
  apt-get install -y -qq nginx libnginx-mod-stream certbot python3-certbot-nginx >/dev/null
  systemctl stop nginx >/dev/null 2>&1 || true
  local p
  for p in 80 443; do
    port_listening "$p" && die "порт $p занят другой программой: освободите его (в панели — галочка «Освободить порты принудительно») и повторите"
  done
  if [[ ! -d "/etc/letsencrypt/live/$d" ]]; then
    log "Let's Encrypt: выпускаю сертификат для $d"
    # Сразу после появления записи у Let's Encrypt она может быть ещё не видна: при пропущенной проверке пробуем несколько раз.
    local tries=1 n
    [[ -n "$SKIP_DNS_CHECK" ]] && tries=4
    for n in $(seq 1 "$tries"); do
      if certbot certonly --standalone --non-interactive --agree-tos --register-unsafely-without-email -d "$d" >/dev/null; then
        break
      fi
      if (( n == tries )); then
        die "сертификат для $d не выпущен: проверьте A-запись и что порт 80 доступен из интернета"
      fi
      log "Сертификат пока не выдан (DNS ещё обновляется), повторяю через 40 секунд ($n из $((tries - 1)))"
      sleep 40
    done
  fi
  # Продление через nginx (порт 80 принадлежит ему), а не standalone, иначе certbot.timer не сможет обновить сертификат.
  sed -i 's/^authenticator = .*/authenticator = nginx/' "/etc/letsencrypt/renewal/$d.conf" 2>/dev/null || true
  install -d /etc/letsencrypt/renewal-hooks/deploy
  printf '#!/bin/sh\nnginx -s reload\n' > /etc/letsencrypt/renewal-hooks/deploy/anypanel-nginx-reload
  chmod 0755 /etc/letsencrypt/renewal-hooks/deploy/anypanel-nginx-reload

  write_reality_nginx "$d"
  install_fake_site
  nginx -t >/dev/null 2>&1 || { nginx -t || true; die "конфигурация nginx не прошла проверку"; }
  systemctl enable --now nginx.service >/dev/null 2>&1
  systemctl restart nginx.service
  if ufw_active; then
    log "ufw: открываю 80/tcp и 443/tcp"
    ufw allow 80/tcp >/dev/null
    ufw allow 443/tcp >/dev/null
  fi
  port_listening 443 || die "nginx не слушает 443 (journalctl -u nginx -n 50)"
  log "REALITY-сайт готов: https://$d (REALITY-подключение Xray: 127.0.0.1:${XRAY_REALITY_PORT}, заглушка: 127.0.0.1:${DECOY_PORT})"
}

do_reality() {
  need_root; check_os
  [[ -n "$REALITY_DOMAIN" ]] || die "укажите домен: bash install-node.sh reality --reality-domain ДОМЕН"
  valid_domain "$REALITY_DOMAIN" || die "неверный домен: «$REALITY_DOMAIN»"
  check_reality_dns "$REALITY_DOMAIN"
  command -v curl >/dev/null || { apt-get update -qq >/dev/null; apt-get install -y -qq curl >/dev/null; }
  setup_reality
}

do_install() {
  need_root; check_os; check_args
  if [[ -e "$UNIT" ]]; then die "агент уже установлен ($UNIT). Для обновления: bash install-node.sh upgrade"; fi
  if [[ "$ASSUME_YES" -eq 0 && -t 0 ]]; then
    printf 'Установить агент Whitelist Panel на этот сервер (порт %s)? [y/N] ' "$PORT"
    local a; read -r a
    [[ "$a" =~ ^[YyДд] ]] || die "отменено"
  fi
  ensure_tools
  if [[ -n "$REALITY_DOMAIN" ]]; then valid_domain "$REALITY_DOMAIN" || die "неверный домен REALITY: «$REALITY_DOMAIN»"; check_reality_dns "$REALITY_DOMAIN"; fi
  install_xray
  # Сайт-заглушка ставится до агента: при сбое здесь агента ещё нет, и установку можно повторить с тем же запросом.
  if [[ -n "$REALITY_DOMAIN" ]]; then setup_reality; fi
  write_files
  systemctl enable --now anypanel-agent.service >/dev/null 2>&1
  wait_listening || die "агент не запустился (journalctl -u anypanel-agent -n 50)"
  local fp; fp="$(fingerprint)"
  open_firewall
  firewall_hint
  cat <<EOF

${C_B}Агент установлен.${C_0}
  Порт агента:            ${PORT}
  Отпечаток сертификата:  ${fp}

Вставьте отпечаток в карточку сервера в панели (поле «Отпечаток») и нажмите «Проверить связь».
Если оставить поле пустым, панель запомнит сертификат при первом соединении.
EOF
}

do_upgrade() {
  need_root
  [[ -f "$BIN_SRC" ]] || die "рядом со скриптом нет файла anypanel"
  [[ -e "$UNIT" ]] || die "агент не установлен: сначала выполните установку"
  local before; before="$(/usr/local/bin/anypanel version 2>/dev/null || echo '?')"
  log "Обновление агента: $before -> $("$BIN_SRC" version)"
  cp -f /usr/local/bin/anypanel /usr/local/bin/anypanel.prev
  install -m 0755 "$BIN_SRC" /usr/local/bin/anypanel
  systemctl restart anypanel-agent.service
  local i ok=0
  for i in $(seq 1 15); do
    if systemctl is-active --quiet anypanel-agent.service; then ok=1; sleep 1; systemctl is-active --quiet anypanel-agent.service && break; fi
    sleep 1
  done
  if [[ "$ok" -ne 1 ]] || ! systemctl is-active --quiet anypanel-agent.service; then
    warn "агент не запустился после обновления — возвращаю версию $before"
    mv -f /usr/local/bin/anypanel.prev /usr/local/bin/anypanel
    systemctl restart anypanel-agent.service
    die "обновление отменено (journalctl -u anypanel-agent -n 50)"
  fi
  log "Готово: работает версия $(/usr/local/bin/anypanel version)"
}

case "$MODE" in upgrade) do_upgrade ;; reality) do_reality ;; *) do_install ;; esac
