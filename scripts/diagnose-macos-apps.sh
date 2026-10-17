#!/usr/bin/env bash
# Read-only diagnostics for the two known JSTV bundle locations.
set -u

if [[ "$(uname -s)" != Darwin ]]; then
  echo "This diagnostic is macOS-only; no files or Launch Services state were changed." >&2
  exit 2
fi

ROOT="$(cd "$(dirname "$0")/.." && pwd -P)"
LSREGISTER="/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
PLISTBUDDY="/usr/libexec/PlistBuddy"

echo "macOS: $(sw_vers -productVersion) ($(sw_vers -buildVersion))"
echo "Diagnostic mode: read-only; no registration, reset, cache deletion, or app launch."

APP_PATHS=("/Applications/JSTV Player.app" "$ROOT/dist/darwin-universal/JSTV Player.app")
for app in "${APP_PATHS[@]}"; do
  echo
  echo "Bundle path: $app"
  if [[ ! -d "$app" ]]; then
    echo "Status: absent"
    continue
  fi
  plist="$app/Contents/Info.plist"
  if [[ ! -f "$plist" ]]; then
    echo "Status: present, Info.plist missing"
    continue
  fi
  for key in CFBundleDisplayName CFBundleName CFBundleIdentifier CFBundleShortVersionString CFBundlePackageType LSApplicationCategoryType; do
    value="$("$PLISTBUDDY" -c "Print :$key" "$plist" 2>/dev/null || true)"
    printf '  %s: %s\n' "$key" "${value:-<missing>}"
  done
  echo "  Spotlight metadata (mdls):"
  if ! mdls -name kMDItemDisplayName -name kMDItemKind -name kMDItemContentType -name kMDItemPath "$app" 2>&1; then
    echo "  mdls returned no metadata for this path."
  fi
done

echo
if [[ -x "$LSREGISTER" ]]; then
  echo "Launch Services text matches for known paths (absence is inconclusive):"
  dump="$("$LSREGISTER" -dump 2>&1 || true)"
  found=0
  for app in "${APP_PATHS[@]}"; do
    if [[ "$dump" == *"$app"* ]]; then
      printf '%s\n' "$dump" | grep -F -B 3 -A 6 -- "$app" || true
      found=1
    fi
  done
  [[ "$found" -eq 1 ]] || echo "No exact-path text match was returned. This does not prove the app is unregistered."
else
  echo "lsregister is unavailable; no registration state queried."
fi

cat <<'NOTE'

Interpretation:
- This report lists bundle metadata and available Spotlight/Launch Services evidence only.
- LSApplicationCategoryType describes an application category, but this diagnostic cannot prove that macOS 27's App list uses it or when that list refreshes.
- Spotlight searchability does not establish App-list inclusion. Do not infer that absent text matches mean a cache problem.
- For duplicate entries, compare the exact bundle paths, identifiers, and versions above. No automatic unregister/register or global index operation is performed.
NOTE
