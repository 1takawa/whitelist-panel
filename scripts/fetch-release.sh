#!/usr/bin/env bash
#
# Скачивает релиз Whitelist Panel из приватного репозитория GitHub и распаковывает его в ./whitelist-panel.
#
#   GH_TOKEN=github_pat_... bash fetch-release.sh            # последний релиз
#   GH_TOKEN=github_pat_... bash fetch-release.sh v0.29.0    # конкретная версия
#
# Токену нужен доступ к репозиторию (fine-grained, право Contents: Read-only).
# Другой репозиторий: WHITELIST_REPO=владелец/имя
#
set -Eeuo pipefail

REPO="${WHITELIST_REPO:-1takawa/whitelist-panel}"
ASSET="whitelist-panel-linux-amd64.tar.gz"
TAG="${1:-}"

die() { printf 'Ошибка: %s\n' "$*" >&2; exit 1; }
[[ -n "${GH_TOKEN:-}" ]] || die "задайте токен: GH_TOKEN=github_pat_... bash fetch-release.sh"

if ! command -v curl >/dev/null || ! command -v jq >/dev/null; then
  [[ $EUID -eq 0 ]] || die "нужны curl и jq: установите их или запустите от root"
  DEBIAN_FRONTEND=noninteractive apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl jq ca-certificates >/dev/null
fi

api="https://api.github.com/repos/$REPO/releases/latest"
[[ -z "$TAG" ]] || api="https://api.github.com/repos/$REPO/releases/tags/$TAG"

meta="$(curl -fsSL -H "Authorization: Bearer $GH_TOKEN" -H "Accept: application/vnd.github+json" "$api")" \
  || die "не удалось получить релиз ($api): проверьте токен, имя репозитория и тег"
tag="$(jq -r '.tag_name' <<<"$meta")"
id="$(jq -r --arg n "$ASSET" '.assets[] | select(.name == $n) | .id' <<<"$meta")"
[[ -n "$id" ]] || die "в релизе $tag нет файла $ASSET"

printf '==> Релиз %s: загружаю %s\n' "$tag" "$ASSET"
curl -fSL -H "Authorization: Bearer $GH_TOKEN" -H "Accept: application/octet-stream" \
  -o "$ASSET" "https://api.github.com/repos/$REPO/releases/assets/$id"

rm -rf whitelist-panel
mkdir whitelist-panel
tar -xzf "$ASSET" -C whitelist-panel
chmod +x whitelist-panel/anypanel whitelist-panel/install.sh whitelist-panel/install-node.sh
rm -f "$ASSET"

printf '==> Готово. Дальше:\n    cd whitelist-panel\n    sudo bash install.sh --panel-domain panel.example.com --reality-domain reality.example.com\n'
