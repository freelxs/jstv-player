package catvod

import (
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

var (
	hlsExplicitAdPathRe = regexp.MustCompile(`(?i)(^|[/_-])(adjump|guanggao|advert|ad_seg|ad-seg|ads|adv)([/_.-]|$)`)
	hlsExplicitAdFileRe = regexp.MustCompile(`(?i)(^|/)ad[0-9]+\.(ts|m4s|mp4|png|jpg|jpeg)$`)
)

type hlsSegment struct {
	uri           string
	duration      float64
	tags          []string
	keyLine       string
	mapLine       string
	discontinuity bool
	blockIndex    int
	explicitAd    bool
}

type hlsBlock struct {
	index       int
	count       int
	duration    float64
	dirCounts   map[string]int
	hasExplicit bool
	hasKeyNone  bool
	hasEncKey   bool
}

// FilterHLSAds removes explicit ad segments and isolated short VOD ad inserts
// delimited by #EXT-X-DISCONTINUITY while preserving encryption keys and
// initialization maps for the remaining segments.
func FilterHLSAds(body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	text := string(body)
	if !strings.Contains(text, "#EXTM3U") || strings.Contains(text, "#EXT-X-STREAM-INF") {
		return body
	}
	rawLines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")

	var headerLines []string
	var segments []hlsSegment
	var pendingTags []string
	var currentKey string
	var currentMap string
	hasEndList := false
	inSegments := false
	pendingDiscontinuity := false
	blockIndex := 0

	for _, raw := range rawLines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if line == "#EXT-X-ENDLIST" {
			hasEndList = true
			continue
		}
		if line == "#EXT-X-DISCONTINUITY" {
			if inSegments {
				blockIndex++
				pendingDiscontinuity = true
			} else {
				headerLines = append(headerLines, line)
			}
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-KEY:") {
			currentKey = line
			if !inSegments {
				headerLines = append(headerLines, line)
			}
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-MAP:") {
			currentMap = line
			if !inSegments {
				headerLines = append(headerLines, line)
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			if !inSegments && !isPerSegmentTag(line) {
				headerLines = append(headerLines, line)
				continue
			}
			inSegments = true
			pendingTags = append(pendingTags, line)
			continue
		}
		inSegments = true
		dur := parseExtInfDuration(pendingTags)
		seg := hlsSegment{
			uri:           line,
			duration:      dur,
			tags:          pendingTags,
			keyLine:       currentKey,
			mapLine:       currentMap,
			discontinuity: pendingDiscontinuity,
			blockIndex:    blockIndex,
			explicitAd:    isExplicitAdSegmentURI(line),
		}
		segments = append(segments, seg)
		pendingTags = nil
		pendingDiscontinuity = false
	}

	if len(segments) < 3 {
		return body
	}

	drop := make([]bool, len(segments))
	totalDuration := 0.0
	for idx, seg := range segments {
		totalDuration += seg.duration
		if seg.explicitAd {
			drop[idx] = true
		}
	}

	if hasEndList && blockIndex > 0 {
		blocks := make([]*hlsBlock, blockIndex+1)
		for idx := range blocks {
			blocks[idx] = &hlsBlock{index: idx, dirCounts: map[string]int{}}
		}
		for _, seg := range segments {
			blk := blocks[seg.blockIndex]
			blk.count++
			blk.duration += seg.duration
			if seg.explicitAd {
				blk.hasExplicit = true
			}
			if dir := hlsSegmentDir(seg.uri); dir != "" {
				blk.dirCounts[dir]++
			}
			upperKey := strings.ToUpper(seg.keyLine)
			if strings.Contains(upperKey, "METHOD=NONE") {
				blk.hasKeyNone = true
			} else if strings.Contains(upperKey, "METHOD=AES-128") || strings.Contains(upperKey, "METHOD=SAMPLE-AES") {
				blk.hasEncKey = true
			}
		}

		var dominant *hlsBlock
		for _, blk := range blocks {
			if blk.count == 0 {
				continue
			}
			if dominant == nil || blk.duration > dominant.duration || (blk.duration == dominant.duration && blk.count > dominant.count) {
				dominant = blk
			}
		}
		if dominant != nil && dominant.count >= 3 && (dominant.duration >= 60 || (totalDuration > 0 && dominant.duration >= totalDuration*0.5)) {
			dominantDir := topBlockDir(dominant)
			adBlocks := map[int]bool{}
			for _, blk := range blocks {
				if blk == dominant || blk.count == 0 {
					continue
				}
				if blk.duration <= 0 || blk.duration > 90 {
					continue
				}
				if dominant.duration > 0 && blk.duration >= dominant.duration*0.25 {
					continue
				}
				blkDir := topBlockDir(blk)
				foreignDir := dominantDir != "" && blkDir != "" && blkDir != dominantDir
				unencryptedSplice := dominant.hasEncKey && blk.hasKeyNone && !blk.hasEncKey
				if blk.hasExplicit || foreignDir || unencryptedSplice {
					adBlocks[blk.index] = true
				}
			}
			if len(adBlocks) > 0 {
				for idx, seg := range segments {
					if adBlocks[seg.blockIndex] {
						drop[idx] = true
					}
				}
			}
		}
	}

	keptCount := 0
	removedDuration := 0.0
	for idx, seg := range segments {
		if drop[idx] {
			removedDuration += seg.duration
		} else {
			keptCount++
		}
	}
	if keptCount == len(segments) {
		return body
	}
	if keptCount < 2 || (totalDuration > 0 && removedDuration > totalDuration*0.4) {
		return body
	}

	var out []string
	lastKey := ""
	lastMap := ""
	for _, line := range headerLines {
		out = append(out, line)
		if strings.HasPrefix(line, "#EXT-X-KEY:") {
			lastKey = line
		} else if strings.HasPrefix(line, "#EXT-X-MAP:") {
			lastMap = line
		}
	}

	emittedAny := false
	prevDropped := false
	for idx, seg := range segments {
		if drop[idx] {
			prevDropped = true
			continue
		}
		if emittedAny && (seg.discontinuity || prevDropped) {
			out = append(out, "#EXT-X-DISCONTINUITY")
		}
		if seg.keyLine != lastKey {
			if seg.keyLine != "" {
				out = append(out, seg.keyLine)
			} else if lastKey != "" {
				out = append(out, "#EXT-X-KEY:METHOD=NONE")
			}
			lastKey = seg.keyLine
		}
		if seg.mapLine != lastMap && seg.mapLine != "" {
			out = append(out, seg.mapLine)
			lastMap = seg.mapLine
		}
		out = append(out, seg.tags...)
		out = append(out, seg.uri)
		emittedAny = true
		prevDropped = false
	}
	if hasEndList {
		out = append(out, "#EXT-X-ENDLIST")
	}
	return []byte(strings.Join(out, "\n") + "\n")
}

func isPerSegmentTag(line string) bool {
	return strings.HasPrefix(line, "#EXTINF:") ||
		strings.HasPrefix(line, "#EXT-X-BYTERANGE:") ||
		strings.HasPrefix(line, "#EXT-X-PROGRAM-DATE-TIME:") ||
		strings.HasPrefix(line, "#EXT-X-DATERANGE:")
}

func parseExtInfDuration(tags []string) float64 {
	for _, tag := range tags {
		if !strings.HasPrefix(tag, "#EXTINF:") {
			continue
		}
		val := strings.TrimPrefix(tag, "#EXTINF:")
		if comma := strings.IndexByte(val, ','); comma >= 0 {
			val = val[:comma]
		}
		if dur, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil && dur > 0 {
			return dur
		}
	}
	return 0
}

func isExplicitAdSegmentURI(raw string) bool {
	cleaned := raw
	if parsed, err := url.Parse(raw); err == nil && parsed.Path != "" {
		cleaned = parsed.Path
	} else if idx := strings.IndexAny(cleaned, "?#"); idx >= 0 {
		cleaned = cleaned[:idx]
	}
	return hlsExplicitAdPathRe.MatchString(cleaned) || hlsExplicitAdFileRe.MatchString(cleaned)
}

func hlsSegmentDir(raw string) string {
	if parsed, err := url.Parse(raw); err == nil && parsed.Scheme != "" {
		return strings.ToLower(parsed.Host) + "|" + path.Dir(parsed.Path)
	}
	cleaned := raw
	if idx := strings.IndexAny(cleaned, "?#"); idx >= 0 {
		cleaned = cleaned[:idx]
	}
	dir := path.Dir(cleaned)
	if dir == "." {
		return "__rel__"
	}
	return dir
}

func topBlockDir(blk *hlsBlock) string {
	bestDir := ""
	bestCount := 0
	for dir, count := range blk.dirCounts {
		if count > bestCount || (count == bestCount && dir < bestDir) {
			bestDir = dir
			bestCount = count
		}
	}
	return bestDir
}
