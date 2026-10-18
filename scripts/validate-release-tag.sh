#!/usr/bin/env bash
set -euo pipefail

release_tag="${1:-}"
version="${SOURCE_VERSION:-}"
event_name="${GITHUB_EVENT_NAME:-}"
ref="${GITHUB_REF:-}"
commit_sha="${GITHUB_SHA:-}"

[[ "$release_tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'Release tag must look like vMAJOR.MINOR.PATCH.' >&2; exit 1; }
[[ "$release_tag" == "v$version" ]] || { echo "Release tag $release_tag does not match workflow source version $version." >&2; exit 1; }

if remote_refs="$(git ls-remote --exit-code --tags origin "refs/tags/$release_tag" "refs/tags/$release_tag^{}" 2>&1)"; then
  git fetch --no-tags origin "refs/tags/$release_tag:refs/tags/$release_tag"
  tag_sha="$(git rev-parse "$release_tag^{commit}")"
  if [[ "$event_name" == push && ( "$ref" == refs/heads/main || "$ref" == refs/tags/* ) && "$tag_sha" != "$commit_sha" ]]; then
    echo "Tag $release_tag points at $tag_sha, not pushed source commit $commit_sha. Verify the tag or bump mygo.json before releasing." >&2
    exit 1
  fi
  tag_version="$(git show "$release_tag:mygo.json" | sed -n 's/.*"version": *"\([^"]*\)".*/\1/p')"
  [[ "$release_tag" == "v$tag_version" ]] || { echo "Tag source version $tag_version does not match $release_tag." >&2; exit 1; }
else
  ls_status=$?
  if [[ "$ls_status" -ne 2 ]]; then
    echo "Could not query origin for tag $release_tag (git ls-remote exited $ls_status): $remote_refs" >&2
    exit "$ls_status"
  fi
  if [[ "$event_name" != push || "$ref" != refs/heads/main ]]; then
    echo "Tag $release_tag does not exist on origin; only a successful main push creates a new tag." >&2
    exit 1
  fi
fi
