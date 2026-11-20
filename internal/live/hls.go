package live

import "strings"

// IsHLSPlayback reports whether data is an HLS media or master playlist.
// A channel list that uses #EXTINF:-1, group-title, or a genre line stays a
// channel list even when it carries other #EXT-X- tags. Tags that define an
// HLS playlist are #EXT-X-STREAM-INF, #EXT-X-I-FRAME-STREAM-INF,
// #EXT-X-TARGETDURATION, #EXT-X-MEDIA-SEQUENCE, #EXT-X-PLAYLIST-TYPE,
// #EXT-X-MAP and #EXT-X-ENDLIST.
func IsHLSPlayback(data []byte) bool {
	text := strings.TrimPrefix(string(data), "\ufeff")
	hls, channel := false, false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" {
			continue
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "#EXT-X-STREAM-INF"),
			strings.HasPrefix(upper, "#EXT-X-I-FRAME-STREAM-INF"),
			strings.HasPrefix(upper, "#EXT-X-TARGETDURATION"),
			strings.HasPrefix(upper, "#EXT-X-MEDIA-SEQUENCE"),
			strings.HasPrefix(upper, "#EXT-X-PLAYLIST-TYPE"),
			strings.HasPrefix(upper, "#EXT-X-MAP:"),
			strings.HasPrefix(upper, "#EXT-X-ENDLIST"):
			hls = true
		case strings.HasPrefix(upper, "#EXTINF:"):
			if channelExtinf(line) {
				channel = true
			}
		case strings.Contains(strings.ToLower(line), "group-title="),
			strings.Contains(line, ",#genre#"):
			channel = true
		}
	}
	if channel {
		return false
	}
	return hls
}

func channelExtinf(line string) bool {
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		return false
	}
	rest := strings.TrimSpace(line[idx+1:])
	dur := rest
	if i := strings.IndexAny(rest, " \t,"); i >= 0 {
		dur = rest[:i]
	}
	if dur == "" || dur == "0" || strings.HasPrefix(dur, "-") {
		return true
	}
	low := strings.ToLower(line)
	return strings.Contains(low, "group-title=") || strings.Contains(low, "tvg-id=") ||
		strings.Contains(low, "tvg-name=") || strings.Contains(low, "tvg-logo=")
}
