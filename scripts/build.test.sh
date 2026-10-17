#!/usr/bin/env bash
set -euo pipefail

SOURCE_ROOT="$(cd "$(dirname "$0")/.." && pwd -P)"
FIXTURE="$(mktemp -d "${TMPDIR:-/tmp}/jstv-build-test.XXXXXX")"
trap 'rm -rf "$FIXTURE"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
assert_file() { [[ -f "$1" ]] || fail "expected file: $1"; }
assert_absent() { [[ ! -e "$1" ]] || fail "expected absent: $1"; }
assert_contains() { grep -Fq -- "$2" "$1" || fail "expected '$2' in $1"; }

new_fixture() {
  local name="$1" root="$FIXTURE/$1"
  mkdir -p "$root/scripts" "$root/mock-bin" "$root/dist/darwin-universal/JSTV Player.app" "$root/out"
  cp "$SOURCE_ROOT/scripts/build.sh" "$root/scripts/build.sh"
  cp "$SOURCE_ROOT/mygo.json" "$root/mygo.json"
  printf '%s\n' prior-dist > "$root/dist/darwin-universal/JSTV Player.app/KEEP"
  cat > "$root/mock-bin/go" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf 'GO %s | GO_BIN=%s | JSTV_CACHE_ROOT=%s | GOCACHE=%s | GOMODCACHE=%s | GOPATH=%s | GOTOOLCHAIN=%s | GOPROXY=%s\n' "$*" "${GO_BIN:-}" "${JSTV_CACHE_ROOT:-}" "${GOCACHE:-}" "${GOMODCACHE:-}" "${GOPATH:-}" "${GOTOOLCHAIN:-}" "${GOPROXY:-}" >> "$TEST_LOG"
platform=""
out=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -platform) platform="$2"; shift 2 ;;
    -o) out="$2"; shift 2 ;;
    *) shift ;;
  esac
done
if [[ "$platform" == darwin/universal ]]; then
  [[ -n "$out" ]] || exit 12
  mkdir -p "$out/darwin-universal/JSTV Player.app/Contents"
  : > "$out/darwin-universal/JSTV Player.app/Contents/Info.plist"
  printf 'fixture dmg\n' > "$out/darwin-universal/JSTV Player.dmg"
elif [[ "$platform" == windows/amd64 ]]; then
  mkdir -p "$TEST_ROOT/dist/windows-amd64"
  printf exe > "$TEST_ROOT/dist/windows-amd64/JSTV Player.exe"
  printf icon > "$TEST_ROOT/dist/windows-amd64/icon.png"
else
  exit 13
fi
MOCK
  cat > "$root/mock-bin/ditto" <<'MOCK'
#!/usr/bin/env bash
[[ "${FAIL_DITTO:-0}" != 1 ]] || exit 31
for last; do :; done
printf zip-fixture > "$last"
MOCK
  cat > "$root/mock-bin/unzip" <<'MOCK'
#!/usr/bin/env bash
[[ "${FAIL_UNZIP:-0}" != 1 && -s "${@: -1}" ]]
MOCK
  cat > "$root/mock-bin/hdiutil" <<'MOCK'
#!/usr/bin/env bash
[[ "${FAIL_HDIUTIL:-0}" != 1 && -s "${@: -1}" ]]
MOCK
  cat > "$root/mock-bin/zip" <<'MOCK'
#!/usr/bin/env bash
for arg; do
  case "$arg" in *.zip*) printf windows-zip-fixture > "$arg"; exit 0 ;; esac
done
exit 1
MOCK
  cat > "$root/mock-bin/lsregister" <<'MOCK'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$TEST_LOG"
[[ "${FAIL_LSREGISTER:-0}" != 1 ]]
MOCK
  chmod +x "$root/mock-bin"/* "$root/scripts/build.sh"
  printf '%s\n' "$root"
}

run_script() {
  local root="$1"; shift
  TEST_ROOT="$root" TEST_LOG="$root/tool.log" GO_BIN="$root/mock-bin/go" \
    JSTV_CACHE_ROOT="$root/cache-root" GOCACHE="$root/custom-gocache" \
    GOMODCACHE="$root/custom-gomodcache" GOPATH="$root/custom-gopath" \
    GOTOOLCHAIN=local GOPROXY=off JSTV_LSREGISTER="$root/mock-bin/lsregister" \
    PATH="$root/mock-bin:$PATH" bash "$root/scripts/build.sh" "$@"
}

# Default success: validates/publishes archives, unregisters only fresh app,
# removes only this invocation's isolated directory, and leaves old dist app.
ROOT="$(new_fixture success)"
run_script "$ROOT" darwin
assert_file "$ROOT/out/JSTV-Player-0.3.2-macos-universal.zip"
assert_file "$ROOT/out/JSTV-Player-0.3.2-macos-universal.dmg"
assert_contains "$ROOT/tool.log" "-u "
assert_contains "$ROOT/tool.log" ".jstv-build."
assert_contains "$ROOT/dist/darwin-universal/JSTV Player.app/KEEP" prior-dist
[[ -z "$(find "$ROOT/out" -maxdepth 1 -type d -name '.jstv-build.*' -print -quit)" ]] || fail "successful build scratch was not removed"

# --keep-app preserves the isolated output and does not unregister it.
ROOT="$(new_fixture keep)"
run_script "$ROOT" macos --keep-app
KEPT="$(find "$ROOT/out" -maxdepth 1 -type d -name '.jstv-build.*' -print -quit)"
[[ -n "$KEPT" ]] || fail "--keep-app did not retain scratch"
assert_file "$KEPT/dist/darwin-universal/JSTV Player.app/Contents/Info.plist"
if grep -Fq -- '-u ' "$ROOT/tool.log"; then fail "--keep-app unexpectedly unregistered app"; fi

# Archive failure keeps the new app and prior published archives byte-for-byte.
ROOT="$(new_fixture archive-failure)"
printf prior-zip > "$ROOT/out/JSTV-Player-0.3.2-macos-universal.zip"
printf prior-dmg > "$ROOT/out/JSTV-Player-0.3.2-macos-universal.dmg"
if FAIL_UNZIP=1 run_script "$ROOT" darwin; then fail "injected ZIP validation failure unexpectedly succeeded"; fi
BUILD_ROOT="$(find "$ROOT/out" -maxdepth 1 -type d -name '.jstv-build.*' -print -quit)"
[[ -n "$BUILD_ROOT" ]] || fail "failed build scratch path was not retained"
assert_file "$BUILD_ROOT/dist/darwin-universal/JSTV Player.app/Contents/Info.plist"
[[ "$(cat "$ROOT/out/JSTV-Player-0.3.2-macos-universal.zip")" == prior-zip ]] || fail "prior ZIP was replaced on failure"
[[ "$(cat "$ROOT/out/JSTV-Player-0.3.2-macos-universal.dmg")" == prior-dmg ]] || fail "prior DMG was replaced on failure"

# Windows selector, all selector, aliases, and Go/cache overrides are retained.
ROOT="$(new_fixture windows)"
run_script "$ROOT" win
assert_file "$ROOT/out/JSTV-Player-0.3.2-windows-amd64.zip"
assert_contains "$ROOT/tool.log" "GO tool mygo build -platform windows/amd64 | GO_BIN=$ROOT/mock-bin/go | JSTV_CACHE_ROOT=$ROOT/cache-root | GOCACHE=$ROOT/custom-gocache"
assert_contains "$ROOT/tool.log" "GOMODCACHE=$ROOT/custom-gomodcache | GOPATH=$ROOT/custom-gopath | GOTOOLCHAIN=local | GOPROXY=off"

ROOT="$(new_fixture all)"
run_script "$ROOT" all --keep-app
assert_file "$ROOT/out/JSTV-Player-0.3.2-macos-universal.zip"
assert_file "$ROOT/out/JSTV-Player-0.3.2-windows-amd64.zip"
[[ "$(grep -c '^GO ' "$ROOT/tool.log")" -eq 2 ]] || fail "all must invoke both platform builds"

echo "PASS: build script fixture tests"
