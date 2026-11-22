package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/freelxs/jstv-player/internal/catvod"
)

const sniffTimeout = 15 * time.Second

// sniffed is a media request captured from a hidden page.
type sniffed struct {
	URL     string
	Headers map[string]string
}

// sniffPage is the desktop fallback after static extraction and parsers.
// Tests replace it. Web mode does not call it.
var sniffPage = sniffHidden

func (p *Player) sniffInto(st catvod.Stream) (catvod.Stream, error) {
	ctx := p.sourceContext
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = withContentRules(ctx, p.activeContentRules())
	found, err := sniffPage(ctx, st.URL, st.Headers)
	if err != nil {
		return st, err
	}
	applyResolved(&st, found.URL, found.Headers)
	if p.proxy != nil && catvod.IsHTTPMediaURL(st.URL) {
		if err := p.proxy.Grant(st.URL, st.Headers, p.GetSettings().AllowLANMedia); err != nil {
			return st, err
		}
	}
	return st, nil
}

func sniffHidden(ctx context.Context, page string, headers map[string]string) (sniffed, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, sniffTimeout)
	defer cancel()
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	if customUA := headerFold(headers, "User-Agent"); customUA != "" {
		ua = customUA
	}
	w := mygo.NewWindow(mygo.WindowOptions{
		Title:  "JSTV",
		Hidden: true,
		Width:  960,
		Height: 540,
		URL:    page,
		Page: mygo.PageOptions{
			Untrusted:            true,
			PreloadScript:        sniffScript,
			PreloadAllFrames:     true,
			Autoplay:             mygo.AutoplayAllow,
			BackgroundThrottling: mygo.ThrottlingOff,
			Session:              mygo.NewEphemeralSession(),
			ContentRules:         contentRulesFromContext(ctx),
			UserAgent:            ua,
		},
	})
	if w == nil {
		return sniffed{}, errors.New("无法打开隐藏页面来嗅探视频")
	}
	defer w.Close()
	w.Page().SetWindowOpenHandler(func(mygo.WindowOpenRequest) *mygo.WindowOptions {
		return nil
	})
	w.Page().OnWillDownload(func(e *mygo.DownloadEvent) {
		e.PreventDefault()
	})
	w.Page().OnWillNavigate(func(e *mygo.NavigateEvent) {
		u, err := url.Parse(e.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			e.PreventDefault()
		}
	})
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		value, err := w.Page().EvalContext(ctx, "window.__jstvMedia || null")
		if err == nil {
			if found, ok := decodeSniffWithHeaders(value, headers); ok {
				return found, nil
			}
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return sniffed{}, errors.New("播放页在 15 秒内没有出现可播放的视频")
			}
			return sniffed{}, errors.New("播放页嗅探已取消")
		case <-tick.C:
		}
	}
}

func decodeSniff(v any) (sniffed, bool) {
	return decodeSniffWithHeaders(v, nil)
}

func decodeSniffWithHeaders(v any, incoming map[string]string) (sniffed, bool) {
	m, ok := v.(map[string]any)
	if !ok || m == nil {
		return sniffed{}, false
	}
	raw, _ := m["url"].(string)
	raw = strings.TrimSpace(raw)
	verified, _ := m["verified"].(bool)
	if !catvodMedia(raw) && !(verified && isHTTPSniffURL(raw)) {
		return sniffed{}, false
	}
	headers := map[string]string{}
	switch h := m["headers"].(type) {
	case map[string]any:
		for k, val := range h {
			headers[k] = fmt.Sprint(val)
		}
	case map[string]string:
		for k, val := range h {
			headers[k] = val
		}
	}
	if explicitRef := headerFold(incoming, "Referer"); explicitRef != "" {
		headers["Referer"] = explicitRef
	} else if ref, _ := m["referer"].(string); ref != "" {
		if _, exists := headers["Referer"]; !exists {
			headers["Referer"] = ref
		}
	}
	if explicitUA := headerFold(incoming, "User-Agent"); explicitUA != "" {
		headers["User-Agent"] = explicitUA
	}
	return sniffed{URL: raw, Headers: headers}, true
}

func headerFold(headers map[string]string, key string) string {
	for k, v := range headers {
		if strings.EqualFold(strings.TrimSpace(k), key) && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func isHTTPSniffURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func catvodMedia(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	p := strings.ToLower(u.Path)
	for _, ext := range []string{".m3u8", ".mp4", ".m4v", ".flv", ".mkv", ".webm", ".mpd"} {
		if strings.HasSuffix(p, ext) {
			return true
		}
	}
	return false
}
