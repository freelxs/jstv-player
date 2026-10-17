#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd -P)"
TARGET="/Applications/JSTV Player.app"
LSREGISTER="/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
PLISTBUDDY="/usr/libexec/PlistBuddy"
EXPECTED_ID="$(sed -n 's/^[[:space:]]*"identifier":[[:space:]]*"\([^"]*\)".*/\1/p' "$ROOT/mygo.json")"
EXPECTED_VERSION="$(sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' "$ROOT/mygo.json")"

usage() {
  echo "Usage: $0 [--dry-run] \"/path/to/JSTV Player.app\"" >&2
  exit 2
}

DRY_RUN=0
if [[ "${1:-}" == "--dry-run" ]]; then
  DRY_RUN=1
  shift
fi
[[ $# -eq 1 ]] || usage
SOURCE="$1"
[[ -d "$SOURCE" && ! -L "$SOURCE" ]] || { echo "Source must be an existing, non-symlink .app directory: $SOURCE" >&2; exit 1; }
[[ "$(basename "$SOURCE")" == "JSTV Player.app" ]] || { echo "Source bundle must be named JSTV Player.app" >&2; exit 1; }
SOURCE="$(cd "$(dirname "$SOURCE")" && pwd -P)/JSTV Player.app"

bundle_value() {
  "$PLISTBUDDY" -c "Print :$2" "$1/Contents/Info.plist" 2>/dev/null
}

validate_identity() {
  local bundle="$1" id package_type
  id="$(bundle_value "$bundle" CFBundleIdentifier)" || return 1
  package_type="$(bundle_value "$bundle" CFBundlePackageType)" || return 1
  [[ "$id" == "$EXPECTED_ID" && "$package_type" == "APPL" ]]
}

validate_release_bundle() {
  local bundle="$1" version category
  validate_identity "$bundle" || return 1
  version="$(bundle_value "$bundle" CFBundleShortVersionString)" || return 1
  category="$(bundle_value "$bundle" LSApplicationCategoryType)" || return 1
  [[ "$version" == "$EXPECTED_VERSION" && "$category" == "public.app-category.entertainment" ]]
}

[[ -n "$EXPECTED_ID" && -n "$EXPECTED_VERSION" ]] || { echo "Could not read expected bundle ID/version from mygo.json" >&2; exit 1; }
if ! validate_release_bundle "$SOURCE"; then
  echo "Source app must be an APPL bundle with ID $EXPECTED_ID and version $EXPECTED_VERSION" >&2
  exit 1
fi
if [[ -L "$TARGET" ]]; then
  echo "Refusing to replace a symlink at the exact install target: $TARGET" >&2
  exit 1
fi
if [[ -e "$TARGET" ]] && ! validate_identity "$TARGET"; then
  echo "Refusing to replace an app with an unexpected bundle ID or malformed package metadata: $TARGET" >&2
  exit 1
fi

echo "Source:  $SOURCE ($EXPECTED_ID $EXPECTED_VERSION)"
echo "Target:  $TARGET"
if [[ -e "$TARGET" ]]; then
  BACKUP_ROOT="$HOME/Library/Application Support/JSTV Player/install-backups"
  BACKUP_DIR="$BACKUP_ROOT/$(/bin/date -u '+%Y%m%dT%H%M%SZ')-$$"
  BACKUP="$BACKUP_DIR/JSTV Player.app.zip"
  echo "Existing target matches the expected bundle ID. Rollback archive path: $BACKUP"
else
  echo "Target does not exist; this will install a new app bundle."
fi
echo "Metadata checks do not authenticate the publisher; the repo has no Developer ID signing or notarization configured."
echo "After a successful copy, only this exact target will be passed to lsregister -f."

if [[ "$DRY_RUN" -eq 1 ]]; then
  echo "Dry run: no files will be copied and LaunchServices will not be changed."
  exit 0
fi
[[ -w /Applications ]] || { echo "No write access to /Applications. Run this from an account allowed to install apps there." >&2; exit 1; }
[[ "$EUID" -ne 0 ]] || { echo "Do not run as root/sudo; use the logged-in account so rollback is kept in its own home directory." >&2; exit 1; }
if /usr/bin/pgrep -x "JSTV Player" >/dev/null 2>&1; then
  echo "JSTV Player appears to be running. Quit it before installing." >&2
  exit 1
fi

printf 'Type INSTALL to continue: '
IFS= read -r answer
[[ "$answer" == "INSTALL" ]] || { echo "Cancelled."; exit 0; }

STAGE=""
PREVIOUS_STAGE=""
PREVIOUS_APP=""
BACKUP=""
MOVED_OLD=0
cleanup() {
  if [[ -n "$STAGE" && -d "$STAGE" ]]; then /bin/rm -rf "$STAGE"; fi
  if [[ "$MOVED_OLD" -eq 1 && ! -e "$TARGET" && -n "$PREVIOUS_APP" && -e "$PREVIOUS_APP" ]]; then
    /bin/mv "$PREVIOUS_APP" "$TARGET" || echo "Rollback failed; previous app remains at $PREVIOUS_APP (archive: $BACKUP)" >&2
    MOVED_OLD=0
  fi
  if [[ "$MOVED_OLD" -eq 0 && -n "$PREVIOUS_STAGE" && -d "$PREVIOUS_STAGE" ]]; then
    /bin/rm -rf "$PREVIOUS_STAGE"
  fi
}
STAGE="$(/usr/bin/mktemp -d /Applications/.jstv-player-stage.XXXXXX)"
trap cleanup EXIT

/usr/bin/ditto --rsrc --extattr "$SOURCE" "$STAGE"
validate_release_bundle "$STAGE" || { echo "Staged copy failed bundle validation." >&2; exit 1; }

if [[ -e "$TARGET" ]]; then
  /bin/mkdir -p "$BACKUP_DIR"
  /bin/chmod 700 "$BACKUP_ROOT" "$BACKUP_DIR"
  /usr/bin/ditto -c -k --sequesterRsrc --keepParent "$TARGET" "$BACKUP"
  /usr/bin/unzip -tq "$BACKUP"
  PREVIOUS_STAGE="$(/usr/bin/mktemp -d /Applications/.jstv-player-previous.XXXXXX)"
  PREVIOUS_APP="$PREVIOUS_STAGE/JSTV Player.app"
  /bin/mv "$TARGET" "$PREVIOUS_APP"
  MOVED_OLD=1
fi

if ! /bin/mv -n "$STAGE" "$TARGET" || [[ -e "$STAGE" ]]; then
  echo "Could not publish staged app." >&2
  if [[ "$MOVED_OLD" -eq 1 && ! -e "$TARGET" && -e "$PREVIOUS_APP" ]]; then
    /bin/mv "$PREVIOUS_APP" "$TARGET"
    MOVED_OLD=0
    echo "Restored the previous app; rollback archive remains at $BACKUP" >&2
  elif [[ "$MOVED_OLD" -eq 1 ]]; then
    echo "Previous app remains at $PREVIOUS_APP; rollback archive at $BACKUP" >&2
  fi
  exit 1
fi
if ! validate_release_bundle "$TARGET"; then
  echo "Installed bundle validation failed: $TARGET" >&2
  /bin/mv "$TARGET" "$STAGE"
  exit 1
fi
MOVED_OLD=0
if [[ -n "$PREVIOUS_STAGE" && -d "$PREVIOUS_STAGE" ]]; then
  /bin/rm -rf "$PREVIOUS_STAGE"
fi

if [[ ! -x "$LSREGISTER" ]]; then
  echo "App installed, but lsregister is unavailable; no registration refresh was attempted." >&2
  exit 1
fi
"$LSREGISTER" -f "$TARGET"
echo "Installed and registered exact target: $TARGET"
echo "Launchpad/Spotlight visibility can still update asynchronously; no global index rebuild was run."
