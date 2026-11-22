#!/usr/bin/env bash
set -euo pipefail

DIR="${1:-}"
if [[ -z "$DIR" || ! -d "$DIR" ]]; then
  echo "Usage: $0 <artifacts-dir>" >&2
  exit 1
fi

hash_cmd=()
verify_cmd=()
if command -v sha256sum >/dev/null 2>&1; then
  hash_cmd=(sha256sum)
  verify_cmd=(sha256sum -c)
elif command -v shasum >/dev/null 2>&1; then
  hash_cmd=(shasum -a 256)
  verify_cmd=(shasum -a 256 -c)
else
  echo "Missing sha256sum/shasum command" >&2
  exit 1
fi

(
  cd "$DIR"
  rm -f SHA256SUMS
  files=()
  while IFS= read -r file; do
    [[ -n "$file" ]] && files+=("$file")
  done < <(LC_ALL=C find . -maxdepth 1 -type f ! -name 'SHA256SUMS' ! -name '.*' -exec basename {} \; | LC_ALL=C sort)
  if [[ ${#files[@]} -eq 0 ]]; then
    echo "No artifact files found in $DIR" >&2
    exit 1
  fi
  "${hash_cmd[@]}" "${files[@]}" > SHA256SUMS
  "${verify_cmd[@]}" SHA256SUMS >/dev/null
)
