# Установка вручную

Этот документ повторяет то, что делает `install.sh`, по шагам. Он нужен, если вы хотите поставить панель без скрипта, понять,
что именно появляется на сервере, или собрать нестандартную схему. Для обычной установки хватит `install.sh` из [README](../README.md).

Все команды выполняются от root на чистом сервере: **Ubuntu 22.04 / 24.04 или Debian 12, x86_64**. Свободны должны быть TCP-порты **80** и **443**.

Ниже `PANEL_DOMAIN` — домен панели и подписок, `REALITY_DOMAIN` — домен для VLESS + REALITY. Оба должны иметь A-запись на IP сервера.

```bash
export PANEL_DOMAIN=panel.example.com
export REALITY_DOMAIN=reality.example.com
```

У вас должен быть каталог релиза с файлами `anypanel`, `install.sh` и `install-node.sh` (скачанный релиз или результат [сборки](../README.md#-сборка-из-исходников)).

---

## 1. Пакеты

```bash
apt-get update
apt-get install -y ca-certificates curl gnupg openssl unzip iproute2
```

## 2. MongoDB 8.0

```bash
curl -fsSL https://www.mongodb.org/static/pgp/server-8.0.asc | gpg --dearmor --yes -o /usr/share/keyrings/mongodb-server-8.0.gpg
chmod 0644 /usr/share/keyrings/mongodb-server-8.0.gpg
```

Репозиторий для **Ubuntu** (замените `jammy` на `noble` для 24.04):

```bash
echo 'deb [arch=amd64 signed-by=/usr/share/keyrings/mongodb-server-8.0.gpg] https://repo.mongodb.org/apt/ubuntu jammy/mongodb-org/8.0 multiverse' \
  > /etc/apt/sources.list.d/mongodb-org-8.0.list
```

Для **Debian 12**:

```bash
echo 'deb [arch=amd64 signed-by=/usr/share/keyrings/mongodb-server-8.0.gpg] https://repo.mongodb.org/apt/debian bookworm/mongodb-org/8.0 main' \
  > /etc/apt/sources.list.d/mongodb-org-8.0.list
```

```bash
apt-get update && apt-get install -y mongodb-org
systemctl enable --now mongod
mongosh --quiet --eval 'db.adminCommand({ping:1}).ok'      # должно напечатать 1
```

> Серверам без AVX нужна MongoDB старой версии: MongoDB 5.0+ требует AVX. Проверка: `grep -c avx /proc/cpuinfo` (0 значит, что AVX нет).

## 3. nginx (stream) для маршрутизации по SNI

```bash
apt-get install -y nginx libnginx-mod-stream
cp -a /etc/nginx/nginx.conf /etc/nginx/nginx.conf.orig
```

Замените `/etc/nginx/nginx.conf` на следующее (подставьте домен REALITY):

```nginx
user www-data;
worker_processes auto;
worker_rlimit_nofile 65535;
pid /run/nginx.pid;
error_log /var/log/nginx/error.log warn;
include /etc/nginx/modules-enabled/*.conf;

events { worker_connections 4096; }

stream {
    map $ssl_preread_server_name $anypanel_upstream {
        reality.example.com  reality;     # домен REALITY -> Xray
        default              web;         # остальное -> Caddy
    }
    upstream reality { server 127.0.0.1:8443; }
    upstream web     { server 127.0.0.1:7443; }

    server {
        listen 443 reuseport;
        listen [::]:443 reuseport;
        ssl_preread on;
        proxy_pass $anypanel_upstream;
        proxy_protocol on;
        proxy_connect_timeout 10s;
    }
}
```

```bash
systemctl stop caddy 2>/dev/null || true       # 443 должен достаться nginx
nginx -t && systemctl enable nginx && systemctl restart nginx
```

## 4. Caddy 2.7 или новее

В пакете Ubuntu лежит Caddy 2.6, в нём нет нужного `proxy_protocol`. Ставьте официальный пакет:

```bash
curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/gpg.key | gpg --dearmor --yes -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt > /etc/apt/sources.list.d/caddy-stable.list
apt-get update && apt-get install -y caddy
caddy version                                   # v2.7 или выше
```

## 5. Xray 26.10.31

```bash
cd "$(mktemp -d)"
curl -fsSL -o xray.zip https://github.com/XTLS/Xray-core/releases/download/v26.10.31/Xray-linux-64.zip
echo 'e4bd9119f50ae7c41658b1abd0679ae06f98bd864a2907ff33f2f2cf7081b214  xray.zip' | sha256sum -c -
unzip -q -o xray.zip -d x
install -m 0755 x/xray /usr/local/bin/xray
install -d -m 0755 /usr/local/share/xray
install -m 0644 x/geoip.dat x/geosite.dat /usr/local/share/xray/
xray version
```

## 6. Панель

Из каталога релиза:

```bash
install -m 0755 anypanel /usr/local/bin/anypanel
install -d -m 0700 /var/lib/anypanel /var/lib/anypanel/xray
install -d -m 0755 /etc/anypanel /etc/anypanel/routes
echo '# Managed by Whitelist Panel.' > /etc/anypanel/routes/inbounds.caddy
```

Файл окружения `/etc/anypanel.env` (права 600):

```bash
umask 077
cat > /etc/anypanel.env <<EOF
LISTEN_ADDR=127.0.0.1:28270
MONGO_URI=mongodb://127.0.0.1:27017
MONGO_DATABASE=anypanel
STATE_DIR=/var/lib/anypanel
XRAY_BIN=/usr/local/bin/xray
XRAY_API_PORT=62789
COOKIE_SECURE=true
PANEL_PUBLIC_URL=https://$PANEL_DOMAIN
CADDY_ROUTES_FILE=/etc/anypanel/routes/inbounds.caddy
CADDY_RELOAD_CMD="systemctl reload caddy"
NGINX_SNI_DIR=/etc/anypanel/nginx
CADDY_SITES_DIR=/etc/anypanel/sites
EOF
chmod 600 /etc/anypanel.env
umask 022
```

Служба `/etc/systemd/system/anypanel.service`:

```ini
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
ReadWritePaths=/etc/anypanel /var/lib/anypanel

[Install]
WantedBy=multi-user.target
```

```bash
systemctl daemon-reload
```

## 7. Администратор и домены

Пароль от 10 символов, читается из stdin:

```bash
set -a; . /etc/anypanel.env; set +a
printf '%s' 'ваш-длинный-пароль' | anypanel init-admin admin
anypanel set-domains "$PANEL_DOMAIN" "$REALITY_DOMAIN"
```

## 8. Caddy: панель и сайт-прикрытие

Каталог и страница для REALITY (сюда Xray направляет чужие соединения):

```bash
install -d -m 0755 /var/www/anypanel-decoy
echo '<!doctype html><title>Welcome</title><h1>It works</h1>' > /var/www/anypanel-decoy/index.html
cp -a /etc/caddy/Caddyfile /etc/caddy/Caddyfile.orig
```

`/etc/caddy/Caddyfile` (подставьте домены и, если нужно, `email`):

```caddy
{
    # email admin@example.com
    https_port 7443
    auto_https disable_redirects
    servers :7443 {
        listener_wrappers {
            proxy_protocol {
                allow 127.0.0.1/32
            }
            tls
        }
        protocols h1 h2
    }
}

http://panel.example.com, http://reality.example.com {
    redir https://{host}{uri} permanent
}

panel.example.com {
    encode zstd gzip
    header {
        Strict-Transport-Security "max-age=31536000; includeSubDomains"
        X-Content-Type-Options "nosniff"
        X-Frame-Options "DENY"
    }
    import /etc/anypanel/routes/*.caddy
    handle {
        reverse_proxy 127.0.0.1:28270
    }
}

reality.example.com {
    root * /var/www/anypanel-decoy
    file_server
}
```

```bash
caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
```

## 9. Запуск

Caddy раньше панели: при старте панель перезагружает Caddy для маршрутов подключений.

```bash
systemctl enable caddy && systemctl restart caddy
systemctl enable --now anypanel
curl -fsS http://127.0.0.1:28270/api/health
```

Файрвол, если включён `ufw`:

```bash
ufw allow 80/tcp
ufw allow 443/tcp
ufw deny 7443/tcp          # внутренний порт Caddy, снаружи не нужен
```

Если `ufw` не используется, откройте 80 и 443 в файрволе хостера.

## 10. Проверка

```bash
systemctl status anypanel caddy nginx mongod
journalctl -u anypanel -f
```

Откройте `https://panel.example.com`: сертификаты Caddy выпустит при первом обращении к каждому домену (до минуты).
Войдите под администратором и создайте подключение **Подключения → Добавить → «VLESS + REALITY»**.

---

## Удаление

Установщик ничего не перезаписывает, поэтому удалять придётся вручную:

```bash
systemctl disable --now anypanel
rm -f /etc/systemd/system/anypanel.service /usr/local/bin/anypanel /etc/anypanel.env
rm -rf /etc/anypanel /var/lib/anypanel /var/www/anypanel-decoy
# nginx и Caddy: верните исходные конфигурации (nginx.conf.orig, Caddyfile.orig) или удалите пакеты
# MongoDB: база anypanel лежит в mongod; удалите её через mongosh (use anypanel; db.dropDatabase()) или удалите пакет
```
