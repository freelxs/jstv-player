#!/usr/bin/env bash
set -euo pipefail

release_tag="${RELEASE_TAG_INPUT:-}"
event_name="${GITHUB_EVENT_NAME:-}"
ref="${GITHUB_REF:-}"

if [[ -z "$release_tag" && "$event_name" == push && "$ref" == refs/tags/v* ]]; then
  release_tag="${ref#refs/tags/}"
elif [[ -z "$release_tag" && "$event_name" == push && "$ref" == refs/heads/main ]]; then
  subject="${PUSH_COMMIT_MESSAGE%%$'\n'*}"
  if [[ "$subject" =~ ^release:\ publish\ (v[0-9]+\.[0-9]+\.[0-9]+)$ ]]; then
    release_tag="${BASH_REMATCH[1]}"
  fi
fi

if [[ -n "$release_tag" && ! "$release_tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo 'Release tag must look like vMAJOR.MINOR.PATCH.' >&2
  exit 1
fi

printf '%s\n' "$release_tag"
