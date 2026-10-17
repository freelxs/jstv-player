#!/usr/bin/env bash
# JSTV Player release helper. Source baseline: untracked Downloads script
# SHA-256 c28226eb8dbb11b93f70cdc55d999d135aa2615366377c65caf16dcfc2218d13
# Usage: scripts/build.sh [darwin|windows|all] [--with-node] [--keep-app]
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd -P)"
cd "$ROOT"

usage() {
  echo "Usage: $0 [darwin|mac|macos|windows|win|all] [--with-node] [--keep-app]" >&2
  exit 2
}

TARGET="${1:-all}"
KEEP_APP=0
WITH_NODE=0
for arg in "${@:2}"; do
  case "$arg" in
    --keep-app) [[ "$KEEP_APP" -eq 0 ]] || usage; KEEP_APP=1 ;;
    --with-node) [[ "$WITH_NODE" -eq 0 ]] || usage; WITH_NODE=1 ;;
    *) usage ;;
  esac
done

# ---- Locate Go (preserves the original GO_BIN/fallback behavior) ----
GO_BIN="${GO_BIN:-}"
if [[ -z "$GO_BIN" ]]; then
  if command -v go >/dev/null 2>&1; then
    GO_BIN="$(command -v go)"
  elif [[ -x "$HOME/.lorca/workspaces/bot-d06858b1/tools/go1.27.1/go/bin/go" ]]; then
    GO_BIN="$HOME/.lorca/workspaces/bot-d06858b1/tools/go1.27.1/go/bin/go"
  else
    echo "未找到 go。请安装 Go 1.27+ 或通过 GO_BIN 指定路径。" >&2
    exit 1
  fi
fi
export PATH="$(dirname "$GO_BIN"):$PATH"

# ---- Cache defaults and all original environment overrides ----
if [[ "${CI:-}" == "true" ]]; then
  # setup-go owns these standard Go cache paths on hosted runners. Keep only
  # this script's downloaded Node archives under the runner's temporary dir.
  CACHE_ROOT="${JSTV_CACHE_ROOT:-${RUNNER_TEMP:-${TMPDIR:-/tmp}}/jstv-player-cache}"
  export GOCACHE="${GOCACHE:-$(go env GOCACHE)}"
  export GOMODCACHE="${GOMODCACHE:-$(go env GOMODCACHE)}"
  export GOPATH="${GOPATH:-$(go env GOPATH)}"
  DEFAULT_GOPROXY="https://proxy.golang.org,direct"
else
  # Preserve the local offline-build defaults and overrides.
  CACHE_ROOT="${JSTV_CACHE_ROOT:-$HOME/.lorca/workspaces/bot-d06858b1/tools/go-cache}"
  export GOCACHE="${GOCACHE:-$CACHE_ROOT/gocache}"
  export GOMODCACHE="${GOMODCACHE:-$CACHE_ROOT/gomodcache}"
  export GOPATH="${GOPATH:-$CACHE_ROOT/gopath}"
  DEFAULT_GOPROXY="off"
fi
export GOTOOLCHAIN="${GOTOOLCHAIN:-local}"
export GOPROXY="${GOPROXY:-$DEFAULT_GOPROXY}"

VERSION="$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' mygo.json)"
[[ -n "$VERSION" ]] || { echo "无法从 mygo.json 读取版本。" >&2; exit 1; }
OUT="$ROOT/out"
mkdir -p "$OUT"
[[ ! -L "$OUT" ]] || { echo "拒绝使用符号链接 out 目录: $OUT" >&2; exit 1; }

LSREGISTER="${JSTV_LSREGISTER:-/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister}"
NODE_VERSION="24.21.0"
NODE_CACHE="$CACHE_ROOT/node-v$NODE_VERSION"
OWNED_RESOURCE_DIRS=()
OWNED_STAGE_DIRS=()

cleanup_owned_staging() {
  local path
  for path in "${OWNED_STAGE_DIRS[@]-}"; do
    [[ -n "$path" ]] || continue
    case "$path" in
      "$OUT"/.node-stage.*) [[ ! -L "$path" ]] && rm -rf -- "$path" || true ;;
      *) echo "Refusing to clean unexpected staging path: $path" >&2 ;;
    esac
  done
  for path in "${OWNED_RESOURCE_DIRS[@]-}"; do
    [[ -n "$path" ]] || continue
    case "$path" in
      "$ROOT/resources/darwin"|"$ROOT/resources/windows-amd64") [[ ! -L "$path" ]] && rm -rf -- "$path" || true ;;
      *) echo "Refusing to clean unexpected resource path: $path" >&2 ;;
    esac
  done
  return 0
}
trap cleanup_owned_staging EXIT

ensure_resource_root() {
  [[ ! -L "$ROOT/resources" ]] || { echo "Refusing to stage Node through a resources symlink" >&2; return 1; }
  mkdir -p "$ROOT/resources"
  [[ ! -L "$ROOT/resources" ]] || { echo "Refusing to stage Node through a resources symlink" >&2; return 1; }
}

download_node_archive() {
  local name="$1" url="https://nodejs.org/dist/v$NODE_VERSION/$1" sums="$NODE_CACHE/SHASUMS256.txt" archive="$NODE_CACHE/$1" expected actual
  mkdir -p "$NODE_CACHE"
  if [[ ! -f "$sums" ]]; then
    curl --fail --location --retry 3 --connect-timeout 30 --silent --show-error "https://nodejs.org/dist/v$NODE_VERSION/SHASUMS256.txt" -o "$sums.tmp"
    mv "$sums.tmp" "$sums"
  fi
  if [[ ! -f "$archive" ]]; then
    curl --fail --location --continue-at - --retry 3 --connect-timeout 30 --silent --show-error "$url" -o "$archive.tmp"
    mv "$archive.tmp" "$archive"
  fi
  expected="$(awk -v n="$name" '$2 == n {print $1}' "$sums")"
  [[ "$expected" =~ ^[a-fA-F0-9]{64}$ ]] || { echo "Official SHASUMS has no SHA-256 for $name" >&2; return 1; }
  actual="$(shasum -a 256 "$archive" | awk '{print $1}')"
  [[ "$(printf '%s' "$actual" | tr '[:upper:]' '[:lower:]')" == "$(printf '%s' "$expected" | tr '[:upper:]' '[:lower:]')" ]] || { rm -f "$archive"; echo "Node archive SHA-256 mismatch: $name" >&2; return 1; }
  printf '%s\n' "$archive"
}

stage_node_macos() {
  local stage="$ROOT/resources/darwin" arm x64 tmp
  [[ ! -e "$stage" && ! -L "$stage" ]] || { echo "Refusing to overwrite existing resources/darwin" >&2; return 1; }
  tmp="$(mktemp -d "$OUT/.node-stage.XXXXXX")"
  OWNED_STAGE_DIRS+=("$tmp")
  arm="$(download_node_archive "node-v$NODE_VERSION-darwin-arm64.tar.gz")"
  x64="$(download_node_archive "node-v$NODE_VERSION-darwin-x64.tar.gz")"
  if ! { mkdir -p "$tmp/arm" "$tmp/x64" \
    && tar -xzf "$arm" -C "$tmp/arm" --strip-components=2 "node-v$NODE_VERSION-darwin-arm64/bin/node" \
    && tar -xzf "$arm" -C "$tmp/arm" --strip-components=1 "node-v$NODE_VERSION-darwin-arm64/LICENSE" \
    && tar -xzf "$x64" -C "$tmp/x64" --strip-components=2 "node-v$NODE_VERSION-darwin-x64/bin/node" \
    && tar -xzf "$x64" -C "$tmp/x64" --strip-components=1 "node-v$NODE_VERSION-darwin-x64/LICENSE"; }; then
    return 1
  fi
  ensure_resource_root
  if ! mkdir "$stage"; then
    echo "Refusing to create resources/darwin: path appeared during staging" >&2
    return 1
  fi
  OWNED_RESOURCE_DIRS+=("$stage")
  if ! { mkdir -p "$stage/node/arm64/bin" "$stage/node/amd64/bin" \
    && cp "$tmp/arm/node" "$stage/node/arm64/bin/node" \
    && cp "$tmp/x64/node" "$stage/node/amd64/bin/node" \
    && cp "$tmp/arm/LICENSE" "$stage/node/LICENSE" \
    && chmod 755 "$stage/node/arm64/bin/node" "$stage/node/amd64/bin/node"; }; then
    return 1
  fi
}

stage_node_windows() {
  local stage="$ROOT/resources/windows-amd64" archive
  [[ ! -e "$stage" && ! -L "$stage" ]] || { echo "Refusing to overwrite existing resources/windows-amd64" >&2; return 1; }
  archive="$(download_node_archive "node-v$NODE_VERSION-win-x64.zip")"
  ensure_resource_root
  if ! mkdir "$stage"; then
    echo "Refusing to create resources/windows-amd64: path appeared during staging" >&2
    return 1
  fi
  OWNED_RESOURCE_DIRS+=("$stage")
  if ! { mkdir -p "$stage/node/bin" \
    && unzip -p "$archive" "node-v$NODE_VERSION-win-x64/node.exe" > "$stage/node/bin/node.exe" \
    && unzip -p "$archive" "node-v$NODE_VERSION-win-x64/LICENSE" > "$stage/node/LICENSE"; }; then
    return 1
  fi
}

restore_artifacts() {
  local backups="$1" final="$2" name
  name="$(basename "$final")"
  if [[ -e "$backups/$name" ]]; then
    [[ ! -e "$final" ]] || rm -f "$final"
    mv "$backups/$name" "$final"
  fi
}

publish_pair() {
  local zip_tmp="$1" dmg_tmp="$2" zip_final="$3" dmg_final="$4" backup_dir="$5"
  mkdir -p "$backup_dir"
  [[ ! -L "$zip_final" && ! -L "$dmg_final" ]] || { echo "拒绝替换符号链接归档。" >&2; return 1; }
  if [[ -e "$zip_final" ]] && ! mv "$zip_final" "$backup_dir/$(basename "$zip_final")"; then
    return 1
  fi
  if [[ -e "$dmg_final" ]] && ! mv "$dmg_final" "$backup_dir/$(basename "$dmg_final")"; then
    restore_artifacts "$backup_dir" "$zip_final"
    return 1
  fi
  if ! mv "$zip_tmp" "$zip_final"; then
    restore_artifacts "$backup_dir" "$zip_final"
    restore_artifacts "$backup_dir" "$dmg_final"
    return 1
  fi
  if ! mv "$dmg_tmp" "$dmg_final"; then
    rm -f "$zip_final"
    restore_artifacts "$backup_dir" "$zip_final"
    restore_artifacts "$backup_dir" "$dmg_final"
    return 1
  fi
  rm -f "$backup_dir/$(basename "$zip_final")" "$backup_dir/$(basename "$dmg_final")"
}

build_darwin() {
  local build_root app_dir platform_dir dmg_source zip_tmp dmg_tmp zip_final dmg_final
  local keep_path=""
  echo "==> 构建 darwin/universal"
  build_root="$(mktemp -d "$OUT/.jstv-build.XXXXXX")"
  if [[ "$WITH_NODE" -eq 1 ]]; then
    stage_node_macos
  fi
  platform_dir="$build_root/dist/darwin-universal"
  app_dir="$platform_dir/JSTV Player.app"
  local suffix=""
  [[ "$WITH_NODE" -eq 1 ]] && suffix="-with-node"
  zip_final="$OUT/JSTV-Player-$VERSION-macos-universal$suffix.zip"
  dmg_final="$OUT/JSTV-Player-$VERSION-macos-universal$suffix.dmg"
  zip_tmp="$OUT/.JSTV-Player-$VERSION-macos-universal$suffix.zip.$$"
  dmg_tmp="$OUT/.JSTV-Player-$VERSION-macos-universal$suffix.dmg.$$"

  if ! "$GO_BIN" tool mygo build -platform darwin/universal -o "$build_root/dist"; then
    echo "macOS 构建失败；隔离输出保留在: $build_root" >&2
    return 1
  fi
  if [[ ! -d "$app_dir" ]]; then
    echo "构建没有生成预期 app；隔离输出保留在: $build_root" >&2
    return 1
  fi
  if [[ "$WITH_NODE" -eq 1 ]]; then
    local arm_node="$app_dir/Contents/Resources/node/arm64/bin/node"
    local x64_node="$app_dir/Contents/Resources/node/amd64/bin/node"
    [[ -x "$arm_node" && -x "$x64_node" ]] || { echo "macOS build lacks an architecture-specific Node runtime" >&2; return 1; }
    lipo -verify_arch arm64 "$arm_node"
    lipo -verify_arch x86_64 "$x64_node"
    otool -L "$arm_node"
    otool -L "$x64_node"
    codesign --verify --deep --strict "$app_dir"
  fi
  shopt -s nullglob
  local dmg_files=("$platform_dir"/*.dmg)
  shopt -u nullglob
  if [[ ${#dmg_files[@]} -ne 1 ]]; then
    echo "预期恰好一个 DMG，实际 ${#dmg_files[@]} 个；隔离输出保留在: $build_root" >&2
    return 1
  fi
  dmg_source="${dmg_files[0]}"

  if ! ditto -c -k --sequesterRsrc --keepParent "$app_dir" "$zip_tmp"; then
    rm -f "$zip_tmp" "$dmg_tmp"
    echo "ZIP 打包失败；隔离输出保留在: $build_root" >&2
    return 1
  fi
  if ! unzip -tq "$zip_tmp"; then
    rm -f "$zip_tmp" "$dmg_tmp"
    echo "ZIP 验证失败；隔离输出保留在: $build_root" >&2
    return 1
  fi
  if ! cp "$dmg_source" "$dmg_tmp" || ! hdiutil verify "$dmg_tmp"; then
    rm -f "$zip_tmp" "$dmg_tmp"
    echo "DMG 复制或验证失败；隔离输出保留在: $build_root" >&2
    return 1
  fi
  if ! publish_pair "$zip_tmp" "$dmg_tmp" "$zip_final" "$dmg_final" "$build_root/artifact-backups"; then
    echo "归档发布失败；尝试保留旧归档，隔离输出保留在: $build_root" >&2
    return 1
  fi

  echo "==> macOS 归档已验证并发布:"
  ls -lh "$zip_final" "$dmg_final"
  if [[ "$KEEP_APP" -eq 1 ]]; then
    keep_path="$build_root"
    echo "--keep-app: 本轮 app 保留在: $app_dir"
    echo "隔离构建目录: $keep_path"
    return 0
  fi
  local unregister_output=""
  if [[ -x "$LSREGISTER" ]]; then
    if unregister_output="$("$LSREGISTER" -u "$app_dir" 2>&1)"; then
      :
    elif [[ "$unregister_output" == *"-10814"* ]]; then
      echo "本轮 app 未注册到 Launch Services（-10814）；继续清理隔离目录。"
    else
      echo "警告：归档已发布，但 Launch Services 注销失败；app 保留在: $app_dir${unregister_output:+ ($unregister_output)}" >&2
      return 0
    fi
  else
    echo "警告：归档已发布，但 lsregister 不可用；app 保留在: $app_dir" >&2
    return 0
  fi
  # Guard cleanup to this invocation's freshly-created path under out/.
  [[ "$build_root" == "$OUT"/.jstv-build.* && -d "$app_dir" && ! -L "$app_dir" ]] || {
    echo "拒绝清理非预期构建路径: $build_root" >&2
    return 1
  }
  rm -rf "$build_root"
  echo "本轮 app 已精确注销并清理；既有 dist/ 副本未更改。"
}

build_windows() {
  local platform_dir zip_final zip_tmp
  echo "==> 构建 windows/amd64"
  if [[ "$WITH_NODE" -eq 1 ]]; then
    stage_node_windows
  fi
  "$GO_BIN" tool mygo build -platform windows/amd64
  platform_dir="$ROOT/dist/windows-amd64"
  local suffix=""
  [[ "$WITH_NODE" -eq 1 ]] && suffix="-with-node"
  zip_final="$OUT/JSTV-Player-$VERSION-windows-amd64$suffix.zip"
  zip_tmp="$OUT/.JSTV-Player-$VERSION-windows-amd64$suffix.zip.$$"
  [[ -f "$platform_dir/JSTV Player.exe" && -f "$platform_dir/icon.png" ]] || {
    echo "Windows 构建缺少 exe/icon: $platform_dir" >&2
    return 1
  }
  if [[ "$WITH_NODE" -eq 1 ]]; then
    [[ -f "$platform_dir/node/bin/node.exe" && -f "$platform_dir/node/LICENSE" ]] || { echo "Windows build lacks bundled Node files" >&2; return 1; }
  fi
  if [[ "$WITH_NODE" -eq 1 ]]; then
    if ! (cd "$platform_dir" && zip -X -r "$zip_tmp" "JSTV Player.exe" icon.png node); then rm -f "$zip_tmp"; return 1; fi
  elif ! (cd "$platform_dir" && zip -X -r "$zip_tmp" "JSTV Player.exe" icon.png); then
    rm -f "$zip_tmp"
    return 1
  fi
  if ! unzip -tq "$zip_tmp"; then
    rm -f "$zip_tmp"
    echo "Windows ZIP 验证失败；旧归档未替换。" >&2
    return 1
  fi
  mv -f "$zip_tmp" "$zip_final"
  echo "==> Windows 产物:"
  ls -lh "$zip_final"
}

case "$TARGET" in
  darwin|mac|macos) build_darwin ;;
  windows|win)      [[ "$KEEP_APP" -eq 0 ]] || usage; build_windows ;;
  all)              build_darwin; build_windows ;;
  *) echo "未知目标: $TARGET (可用 darwin|windows|all)" >&2; usage ;;
esac

echo "完成。产物在 $OUT/"
