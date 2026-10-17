#!/usr/bin/env bash
# JSTV Player release helper. Source baseline: untracked Downloads script
# SHA-256 c28226eb8dbb11b93f70cdc55d999d135aa2615366377c65caf16dcfc2218d13
# Usage: scripts/build.sh [darwin|windows|all] [--keep-app]
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd -P)"
cd "$ROOT"

usage() {
  echo "Usage: $0 [darwin|mac|macos|windows|win|all] [--keep-app]" >&2
  exit 2
}

TARGET="${1:-all}"
KEEP_APP=0
if [[ "${2:-}" == "--keep-app" && $# -eq 2 ]]; then
  KEEP_APP=1
elif [[ $# -gt 1 ]]; then
  usage
fi

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
CACHE_ROOT="${JSTV_CACHE_ROOT:-$HOME/.lorca/workspaces/bot-d06858b1/tools/go-cache}"
export GOCACHE="${GOCACHE:-$CACHE_ROOT/gocache}"
export GOMODCACHE="${GOMODCACHE:-$CACHE_ROOT/gomodcache}"
export GOPATH="${GOPATH:-$CACHE_ROOT/gopath}"
export GOTOOLCHAIN="${GOTOOLCHAIN:-local}"
export GOPROXY="${GOPROXY:-off}"

VERSION="$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' mygo.json)"
[[ -n "$VERSION" ]] || { echo "无法从 mygo.json 读取版本。" >&2; exit 1; }
OUT="$ROOT/out"
mkdir -p "$OUT"
[[ ! -L "$OUT" ]] || { echo "拒绝使用符号链接 out 目录: $OUT" >&2; exit 1; }

LSREGISTER="${JSTV_LSREGISTER:-/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister}"

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
  platform_dir="$build_root/dist/darwin-universal"
  app_dir="$platform_dir/JSTV Player.app"
  zip_final="$OUT/JSTV-Player-$VERSION-macos-universal.zip"
  dmg_final="$OUT/JSTV-Player-$VERSION-macos-universal.dmg"
  zip_tmp="$OUT/.JSTV-Player-$VERSION-macos-universal.zip.$$"
  dmg_tmp="$OUT/.JSTV-Player-$VERSION-macos-universal.dmg.$$"

  if ! "$GO_BIN" tool mygo build -platform darwin/universal -o "$build_root/dist"; then
    echo "macOS 构建失败；隔离输出保留在: $build_root" >&2
    return 1
  fi
  if [[ ! -d "$app_dir" ]]; then
    echo "构建没有生成预期 app；隔离输出保留在: $build_root" >&2
    return 1
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
  if [[ -x "$LSREGISTER" ]]; then
    if ! "$LSREGISTER" -u "$app_dir"; then
      echo "归档已发布，但精确注销失败；app 保留在: $app_dir" >&2
      return 1
    fi
  else
    echo "归档已发布，但 lsregister 不可用；app 保留在: $app_dir" >&2
    return 1
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
  "$GO_BIN" tool mygo build -platform windows/amd64
  platform_dir="$ROOT/dist/windows-amd64"
  zip_final="$OUT/JSTV-Player-$VERSION-windows-amd64.zip"
  zip_tmp="$OUT/.JSTV-Player-$VERSION-windows-amd64.zip.$$"
  [[ -f "$platform_dir/JSTV Player.exe" && -f "$platform_dir/icon.png" ]] || {
    echo "Windows 构建缺少 exe/icon: $platform_dir" >&2
    return 1
  }
  if ! (cd "$platform_dir" && zip -X -r "$zip_tmp" "JSTV Player.exe" icon.png); then
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
