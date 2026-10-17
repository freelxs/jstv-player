# Changelog

## 0.3.3

- Treat nil and empty stream headers as the same playback grant identity after
  the frontend JSON round trip, while retaining exact checks for nonempty
  headers.
- Restrict automatic episode selection to the HTTP(S) URL forms accepted by
  proxy grant registration, and report when an episode has no supported
  continuous-play address.
- These fixes address confirmed code paths; they do not establish the cause
  or resolution of any individual playback incident.

## 0.3.2

- The macOS release helper builds into a unique, isolated output directory and
  validates the ZIP and DMG before publishing either artifact.
- After successful validation and publication, it unregisters and removes only
  the app bundle created by that invocation. Existing `dist/` apps and prior
  artifacts are left untouched. `--keep-app` preserves the isolated build
  directory and reports its exact path.
- Failed builds or archive validation retain the isolated app for recovery and
  do not replace prior release archives. No installation, global Launch
  Services refresh, or Spotlight reset is performed.
- The script source was the original untracked `scripts/build.sh` (SHA-256
  `c28226eb8dbb11b93f70cdc55d999d135aa2615366377c65caf16dcfc2218d13`); its
  Windows target, `all` selector, Go/cache overrides, and fallback behavior
  are retained. The original file in the Downloads worktree was not modified.
- `LSApplicationCategoryType` remains entertainment metadata; this change does
  not claim it controls App-list categorization or guarantees refresh timing.
- Added a read-only macOS diagnostic for the installed and project `dist/`
  bundle paths; it does not alter Launch Services or system indexes.

## 0.3.1

- Set the macOS Launch Services application category to Entertainment in the
  generated bundle's `Info.plist`.
- Keep the stable bundle identifier `com.freelxs.jstvplayer`; this metadata
  change does not create a new application identity.
- Added an opt-in macOS installation helper that verifies the source bundle,
  asks before replacing `/Applications/JSTV Player.app`, preserves the prior
  app as a verified ZIP rollback archive, and registers only the installed
  target path.
- Does not force a Spotlight/Launchpad database rebuild. DMG drag-and-drop has
  no post-install hook, and visibility may refresh asynchronously.
- The duplicate icon source is confirmed as the installed
  `/Applications/JSTV Player.app` plus the extracted development bundle at
  `dist/darwin-universal/JSTV Player.app`.
  This release does not remove or unregister either copy.
- No GUI/app-list refresh or real playback verification is claimed. The repo
  does not configure Developer ID signing or notarization; the isolated app
  built for this check carries an ad-hoc signature, which does not authenticate
  its publisher.

## 0.3.0

This release adds security and playback behavior changes. Application/package
version is defined by `mygo.json`; CI derives artifact filenames from that
field. The Go module/toolchain requirements in `go.mod` are unchanged.

### P0 — lifecycle, downloads, and network exposure

- Improved app/plugin shutdown and cleanup, including initialization retry and
  host-start failure handling.
- Script/config downloads are bounded and streamed, validated before atomic
  replacement, with strict MD5 mode available. MD5 remains the publisher/source
  protocol check; it does not authenticate the publisher.
- Added secret redaction to application-managed logs. Arbitrary third-party
  plugin output may contain formats the app cannot recognize, so complete
  redaction is not guaranteed.
- LAN plugin serving and LAN media access require explicit settings and carry
  an exposure warning; the default remains local-only.

### P1 — proxy and plugin trust

- Replaced client-selected arbitrary proxy targets with opaque, server-managed
  stream IDs and scoped proxy mappings. HLS variants/keys, byte ranges,
  redirects, legitimate plugin loopback targets, and explicitly authorized LAN
  media are supported by the proxy flow.
- Added first-use and content-change SHA-256 trust prompts for downloaded
  plugin files. Existing source MD5 behavior is retained. This TOFU pin is a
  local change-detection/approval aid, not publisher authentication; changing
  the source URL requires a new trust decision.
- Node permission support remains a help/flag capability check only. A
  permission-restricted execution mode is not implemented, and Node's
  permission model is not treated as a security sandbox.
- The existing cross-platform CI test workflow is retained and includes Go,
  Node, and Linux race test jobs.

### P2 — filters and optional logs

- Added category-aware Home filters for the documented metadata format, with
  safe fallback for missing or malformed metadata and request-token handling
  for stale responses.
- Added optional bounded persistent, redacted logs. Persistence is off by
  default; the UI exposes clearing and retention information. Logs are limited
  by age and total size, so capacity pressure may evict entries earlier.
- Added per-row age filtering and cleanup so expired log lines do not remain
  visible merely because an active file was recently modified.

### Migration and verification notes

- Existing settings continue to migrate through the current settings schema;
  existing SHA-256 pins are retained. A first trust prompt is required where a
  source has no matching pin or its content/source URL has changed. Declining
  the prompt prevents the changed script from running.
- Persistent logs remain disabled after upgrade unless explicitly enabled.
  Clearing logs does not clear trust pins, playback history, or plugin config.
- GUI/manual playback was not verified for this release. Mock requests and
  automated tests do not establish successful playback against real media.
- The repo does not configure Developer ID signing or notarization. Locally
  built app bundles may carry an ad-hoc signature, which does not authenticate
  their publisher.
