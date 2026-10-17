# Changelog

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
- Distributed packages remain unsigned and are not notarized.
