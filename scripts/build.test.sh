#!/usr/bin/env bash
set -euo pipefail

SOURCE_ROOT="$(cd "$(dirname "$0")/.." && pwd -P)"
FIXTURE="$(mktemp -d "${TMPDIR:-/tmp}/jstv-build-test.XXXXXX")"
trap 'rm -rf "$FIXTURE"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
assert_file() { [[ -f "$1" ]] || fail "expected file: $1"; }
assert_absent() { [[ ! -e "$1" ]] || fail "expected absent: $1"; }
assert_contains() { grep -Fq -- "$2" "$1" || { cat "$1" >&2; fail "expected '$2' in $1"; }; }

grep -Fq 'GOPROXY: https://proxy.golang.org,direct' "$SOURCE_ROOT/.github/workflows/build.yml" || fail "macOS CI build must use a network-enabled Go proxy"
WORKFLOW="$SOURCE_ROOT/.github/workflows/build.yml"
for action in 'actions/checkout@v7' 'actions/setup-go@v7' 'actions/setup-node@v7' 'actions/upload-artifact@v7' 'actions/download-artifact@v8'; do
  grep -Fq -- "uses: $action" "$WORKFLOW" || fail "workflow must use verified Node 24 action $action"
done
grep -Fq 'node-version: 24' "$WORKFLOW" || fail "frontend tests must use Node 24"
grep -Fq 'release_tag:' "$WORKFLOW" || fail "workflow dispatch must expose optional release_tag"
grep -Fq 'RELEASE_TAG: ${{ inputs.release_tag' "$WORKFLOW" || fail "release tag must enter shell through an environment variable"
grep -Fq '[[ "$RELEASE_TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]' "$WORKFLOW" || fail "release tag must be restricted to numeric v-prefixed versions"
grep -Fq 'git ls-remote --exit-code --tags origin' "$WORKFLOW" || fail "release tag must already exist on origin"
grep -Fq 'needs.resolve-release.outputs.release_tag' "$WORKFLOW" || fail "release jobs must use the validated tag"
grep -Fq 'gh release create "$TAG" release-assets/* --generate-notes --verify-tag --repo "$GITHUB_REPOSITORY"' "$WORKFLOW" || fail "release creation must verify the existing tag and specify repository explicitly"

new_fixture() {
  local name="$1" root="$FIXTURE/$1"
  mkdir -p "$root/scripts" "$root/mock-bin" "$root/dist/darwin-universal/JSTV Player.app" "$root/out"
  cp "$SOURCE_ROOT/scripts/build.sh" "$root/scripts/build.sh"
  cp "$SOURCE_ROOT/mygo.json" "$root/mygo.json"
  printf '%s\n' prior-dist > "$root/dist/darwin-universal/JSTV Player.app/KEEP"
  cat > "$root/mock-bin/go" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == env ]]; then
  case "${2:-}" in
    GOCACHE) printf '%s\n' "$TEST_ROOT/setup-go-cache" ;;
    GOMODCACHE) printf '%s\n' "$TEST_ROOT/setup-go-modcache" ;;
    GOPATH) printf '%s\n' "$TEST_ROOT/setup-go-path" ;;
    *) exit 14 ;;
  esac
  exit 0
fi
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
  if [[ -d "$TEST_ROOT/resources/darwin" ]]; then
    mkdir -p "$out/darwin-universal/JSTV Player.app/Contents/Resources"
    cp -R "$TEST_ROOT/resources/darwin/." "$out/darwin-universal/JSTV Player.app/Contents/Resources/"
  fi
  : > "$out/darwin-universal/JSTV Player.app/Contents/Info.plist"
  printf 'fixture dmg\n' > "$out/darwin-universal/JSTV Player.dmg"
  [[ "${FAIL_GO_DARWIN:-0}" != 1 ]] || exit 41
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
if [[ -d "$5/Contents/Resources/node" ]]; then
  printf ' node=present' >> "$last"
else
  printf ' node=absent' >> "$last"
fi
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
case "${LSREGISTER_MODE:-success}" in
  not-registered) echo "failed to scan $2: -10814" >&2; exit 1 ;;
  other-error) echo "fixture Launch Services failure" >&2; exit 1 ;;
esac
[[ "${FAIL_LSREGISTER:-0}" != 1 ]]
MOCK
cat > "$root/mock-bin/lipo" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
[[ $# -eq 3 && -f "$1" && "$2" == -verify_arch ]] || exit 1
case "$1:$3" in
  */node/arm64/bin/node:arm64|*/node/amd64/bin/node:x86_64) exit 0 ;;
  *) exit 1 ;;
esac
MOCK
cat > "$root/mock-bin/otool" <<'MOCK'
#!/usr/bin/env bash
exit 0
MOCK
cat > "$root/mock-bin/codesign" <<'MOCK'
#!/usr/bin/env bash
exit 0
MOCK
  chmod +x "$root/mock-bin"/* "$root/scripts/build.sh"
  printf '%s\n' "$root"
}

prepare_node_cache() {
  local root="$1" version=24.21.0 tree
  local cache_root="${2:-$root/cache-root}"
  local cache="$cache_root/node-v$version"
  tree="$root/node-fixture"
  mkdir -p "$cache" \
    "$tree/node-v$version-darwin-arm64/bin" \
    "$tree/node-v$version-darwin-x64/bin"
  printf fixture-arm64 > "$tree/node-v$version-darwin-arm64/bin/node"
  printf fixture-x64 > "$tree/node-v$version-darwin-x64/bin/node"
  printf fixture-license > "$tree/node-v$version-darwin-arm64/LICENSE"
  printf fixture-license > "$tree/node-v$version-darwin-x64/LICENSE"
  tar -czf "$cache/node-v$version-darwin-arm64.tar.gz" -C "$tree" "node-v$version-darwin-arm64"
  tar -czf "$cache/node-v$version-darwin-x64.tar.gz" -C "$tree" "node-v$version-darwin-x64"
  (cd "$cache" && shasum -a 256 node-v$version-darwin-arm64.tar.gz node-v$version-darwin-x64.tar.gz > SHASUMS256.txt)
}

run_script() {
  local root="$1"; shift
  TEST_ROOT="$root" TEST_LOG="$root/tool.log" GO_BIN="$root/mock-bin/go" \
    JSTV_CACHE_ROOT="$root/cache-root" GOCACHE="$root/custom-gocache" \
    GOMODCACHE="$root/custom-gomodcache" GOPATH="$root/custom-gopath" \
    GOTOOLCHAIN=local GOPROXY=off JSTV_LSREGISTER="$root/mock-bin/lsregister" \
    PATH="$root/mock-bin:$PATH" bash "$root/scripts/build.sh" "$@"
}

run_ci_script() {
  local root="$1"; shift
  env -u JSTV_CACHE_ROOT -u GOCACHE -u GOMODCACHE -u GOPATH -u GOTOOLCHAIN -u GOPROXY \
    TEST_ROOT="$root" TEST_LOG="$root/tool.log" GO_BIN="$root/mock-bin/go" \
    RUNNER_TEMP="$root/runner-temp" CI=true JSTV_LSREGISTER="$root/mock-bin/lsregister" \
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

# CI defaults use setup-go's go env cache paths and a reachable module proxy,
# while placing helper downloads in the runner temp directory instead of a
# developer-specific Lorca path.
ROOT="$(new_fixture ci-defaults)"
prepare_node_cache "$ROOT" "$ROOT/runner-temp/jstv-player-cache"
run_ci_script "$ROOT" darwin --with-node
assert_file "$ROOT/out/JSTV-Player-0.3.2-macos-universal-with-node.zip"
grep -Fq 'node=present' "$ROOT/out/JSTV-Player-0.3.2-macos-universal-with-node.zip" || fail "CI cache defaults did not find the bundled Node fixtures"
assert_contains "$ROOT/tool.log" "GOCACHE=$ROOT/setup-go-cache"
assert_contains "$ROOT/tool.log" "GOMODCACHE=$ROOT/setup-go-modcache | GOPATH=$ROOT/setup-go-path | GOTOOLCHAIN=local | GOPROXY=https://proxy.golang.org,direct"
if grep -Fq '.lorca/workspaces' "$ROOT/tool.log"; then fail "CI build used a developer-specific Lorca cache path"; fi

# A failed fat build must clean only the Node staging directory, so a later
# slim build cannot pick it up through MyGo's normal resources directory.
ROOT="$(new_fixture node-cleanup)"
prepare_node_cache "$ROOT"
if FAIL_GO_DARWIN=1 run_script "$ROOT" darwin --with-node; then fail "injected fat build failure unexpectedly succeeded"; fi
assert_absent "$ROOT/resources/darwin"
[[ -z "$(find "$ROOT/out" -maxdepth 1 -type d -name '.node-stage.*' -print -quit)" ]] || fail "Node extraction scratch survived failed build"
run_script "$ROOT" darwin
grep -Fq 'node=absent' "$ROOT/out/JSTV-Player-0.3.2-macos-universal.zip" || fail "slim ZIP fixture contains bundled Node"

# Existing resources are never claimed or removed by cleanup.
ROOT="$(new_fixture existing-resource)"
prepare_node_cache "$ROOT"
mkdir -p "$ROOT/resources/darwin"
printf user-resource > "$ROOT/resources/darwin/keep.txt"
if run_script "$ROOT" darwin --with-node; then fail "existing platform resources were accepted"; fi
assert_contains "$ROOT/resources/darwin/keep.txt" user-resource

# Launch Services can report -10814 for an app that was never registered.
# It must not prevent the all target from building Windows.
ROOT="$(new_fixture all-not-registered)"
LSREGISTER_MODE=not-registered run_script "$ROOT" all > "$ROOT/build.log" 2>&1 || fail "expected not-registered result stopped all build"
assert_file "$ROOT/out/JSTV-Player-0.3.2-macos-universal.zip"
assert_file "$ROOT/out/JSTV-Player-0.3.2-windows-amd64.zip"
[[ "$(grep -c '^GO ' "$ROOT/tool.log")" -eq 2 ]] || fail "all must invoke both platform builds"
assert_contains "$ROOT/build.log" "-10814"

# Unexpected unregister failures warn and retain the app, while still allowing
# the Windows half of the all target to finish.
ROOT="$(new_fixture all-unregister-error)"
LSREGISTER_MODE=other-error run_script "$ROOT" all > "$ROOT/build.log" 2>&1 || fail "other unregister error stopped all build"
assert_file "$ROOT/out/JSTV-Player-0.3.2-windows-amd64.zip"
assert_contains "$ROOT/build.log" "app 保留在"
[[ -n "$(find "$ROOT/out" -path '*/JSTV Player.app/Contents/Info.plist' -print -quit)" ]] || fail "unexpected unregister error did not retain app"

echo "PASS: build script fixture tests"
