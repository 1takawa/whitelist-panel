#!/usr/bin/env bash
#
# Whitelist Panel: установка одной командой. Скачивает последний релиз, проверяет контрольную сумму
# и запускает основной установщик (deploy/install.sh) с вашими параметрами.
#
#   bash <(curl -fsSL https://raw.githubusercontent.com/1takawa/whitelist-panel/main/install.sh) \
#       --panel-domain panel.example.com --reality-domain reality.example.com
#
#   ... upgrade                       обновить установленную панель до последнего релиза
#   WHITELIST_VERSION=v0.32.0 ...     поставить конкретную версию вместо последней
#   WHITELIST_REPO=владелец/имя       взять релизы из другого репозитория
#
# Все остальные параметры (--email, --admin-user, -y, --check и так далее) передаются основному установщику как есть.
#
set -Eeuo pipefail

REPO="${WHITELIST_REPO:-1takawa/whitelist-panel}"
VERSION="${WHITELIST_VERSION:-latest}"
ASSET="whitelist-panel-linux-amd64.tar.gz"

die() { printf 'Ошибка: %s\n' "$*" >&2; exit 1; }
log() { printf '==> %s\n' "$*"; }

[[ $EUID -eq 0 ]] || die "запустите от root: войдите под root (sudo -i) и повторите команду"
[[ "$(uname -m)" == x86_64 ]] || die "готовые сборки только для x86_64, у вас $(uname -m)"

if ! command -v curl >/dev/null || ! command -v tar >/dev/null; then
  command -v apt-get >/dev/null || die "нужны curl и tar"
  DEBIAN_FRONTEND=noninteractive apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl tar ca-certificates >/dev/null
fi

base="https://github.com/$REPO/releases/latest/download"
[[ "$VERSION" == latest ]] || base="https://github.com/$REPO/releases/download/$VERSION"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

log "Загружаю $ASSET ($VERSION)"
curl -fSL --retry 3 -o "$tmp/$ASSET" "$base/$ASSET" \
  || die "не удалось скачать $base/$ASSET. проверьте интернет и что у репозитория есть релиз"
if curl -fsSL --retry 3 -o "$tmp/$ASSET.sha256" "$base/$ASSET.sha256"; then
  ( cd "$tmp" && sha256sum -c --quiet "$ASSET.sha256" ) || die "контрольная сумма архива не совпала"
  log "Контрольная сумма совпала"
else
  printf '!!  файла контрольной суммы нет: продолжаю без проверки\n' >&2
fi

mkdir "$tmp/pkg"
tar -xzf "$tmp/$ASSET" -C "$tmp/pkg"
chmod +x "$tmp/pkg/anypanel" "$tmp/pkg/install.sh" "$tmp/pkg/install-node.sh"

# Основной установщик ищет бинарник панели рядом с собой. Без exec: после него нужно удалить временный каталог.
set +e
bash "$tmp/pkg/install.sh" "$@"
rc=$?
set -e
exit "$rc"
