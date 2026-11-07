# Generated playback fixtures

These files contain a project-generated blue frame, without external media.
They exercise actual H.264 decoding, HLS rewriting, headers, and seek/resume.

Generate the HLS fixture with FFmpeg:

```sh
ffmpeg -f lavfi -i 'color=c=blue:s=96x64:r=12:d=12' \
  -c:v libx264 -profile:v baseline -pix_fmt yuv420p -g 24 -sc_threshold 0 \
  -an -f hls -hls_time 2 -hls_list_size 0 -hls_playlist_type vod \
  -hls_segment_filename testdata/playback/segment-%02d.ts testdata/playback/stream.m3u8
ffmpeg -i testdata/playback/stream.m3u8 -c copy -movflags +faststart testdata/playback/vod.mp4
```

Run `scripts/e2e-web.cjs` with `CHROME` pointing to a Chromium executable and
`JSTV_PLAYWRIGHT_MODULE` pointing to an installed Playwright module if it is not
available in the normal Node module search path. The script accepts a web
binary path and an optional artifact directory, and uses temporary fixture data.
