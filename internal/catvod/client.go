package catvod

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Site is one entry of GET /config → video.sites.
type Site struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Type        int    `json:"type"`
	API         string `json:"api"`
	Searchable  *int   `json:"searchable,omitempty"`
	QuickSearch *int   `json:"quickSearch,omitempty"`
	Filterable  *int   `json:"filterable,omitempty"`
}

// CanSearch is true unless the site sets searchable:0.
func (s Site) CanSearch() bool { return s.Searchable == nil || *s.Searchable != 0 }

// Config is GET /config.
type Config struct {
	Video struct {
		Sites []Site `json:"sites"`
	} `json:"video"`
}

// Client speaks the CatPawOpen HTTP protocol to a running bundle.
type Client struct {
	Base string
	HTTP *http.Client
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (c *Client) do(method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.Base+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s %s: HTTP %d %s", method, path, resp.StatusCode, truncate(string(b), 300))
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("%s: 返回不是 JSON: %s", path, truncate(string(b), 200))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func join(api, action string) string { return strings.TrimRight(api, "/") + "/" + action }

// Config lists the sites the bundle registered.
func (c *Client) Config() (*Config, error) {
	var cfg Config
	return &cfg, c.do("GET", "/config", nil, &cfg)
}

// M is a loosely typed JSON object as spiders return them.
type M = map[string]any

func (c *Client) Init(api string) error {
	var out any
	return c.do("POST", join(api, "init"), M{}, &out)
}

// Home returns {class:[{type_id,type_name}], filters:{type_id:[…]}, list?}.
func (c *Client) Home(api string) (M, error) {
	var out M
	return out, c.do("POST", join(api, "home"), M{"filter": true}, &out)
}

// Category returns {page,pagecount,total,list:[vod]}.
func (c *Client) Category(api, tid string, page int, filters map[string]string) (M, error) {
	if filters == nil {
		filters = map[string]string{}
	}
	var out M
	return out, c.do("POST", join(api, "category"), M{"id": tid, "page": page, "filter": true, "filters": filters}, &out)
}

// Detail returns {list:[vod]} with vod_play_from / vod_play_url.
func (c *Client) Detail(api, id string) (M, error) {
	var out M
	return out, c.do("POST", join(api, "detail"), M{"id": id}, &out)
}

// Play returns {parse, url, header?, jx?}.
func (c *Client) Play(api, flag, id string) (M, error) {
	var out M
	return out, c.do("POST", join(api, "play"), M{"flag": flag, "id": id}, &out)
}

// Search returns {page,pagecount,list:[vod]}.
func (c *Client) Search(api, wd string, page int) (M, error) {
	var out M
	return out, c.do("POST", join(api, "search"), M{"wd": wd, "page": page, "quick": false}, &out)
}

// Episode is one entry of a play line.
type Episode struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// Line is one play source (vod_play_from) with its episodes.
type Line struct {
	Flag     string    `json:"flag"`
	Episodes []Episode `json:"episodes"`
}

// ParseLines splits vod_play_from ("a$$$b") and vod_play_url
// ("第1集$id1#第2集$id2$$$…") into lines.
func ParseLines(from, urls string) []Line {
	flags := strings.Split(from, "$$$")
	groups := strings.Split(urls, "$$$")
	var lines []Line
	for i, g := range groups {
		flag := fmt.Sprintf("线路%d", i+1)
		if i < len(flags) && strings.TrimSpace(flags[i]) != "" {
			flag = flags[i]
		}
		var eps []Episode
		for j, it := range strings.Split(g, "#") {
			it = strings.TrimSpace(it)
			if it == "" {
				continue
			}
			name, id, ok := strings.Cut(it, "$")
			if !ok {
				name, id = fmt.Sprintf("第%d集", j+1), it
			}
			eps = append(eps, Episode{Name: name, ID: id})
		}
		if len(eps) > 0 {
			lines = append(lines, Line{Flag: flag, Episodes: eps})
		}
	}
	return lines
}

// Stream is one playable URL.
type Stream struct {
	Name    string            `json:"name"`
	URL     string            `json:"url"`
	Parse   bool              `json:"parse"` // a web page that still needs sniffing
	Headers map[string]string `json:"headers,omitempty"`
}

// NormalizePlay turns a /play answer into streams. url is a string or a
// list of alternating [name, url, name, url…].
func NormalizePlay(r M) []Stream {
	parse := false
	switch v := r["parse"].(type) {
	case float64:
		parse = v == 1
	case bool:
		parse = v
	case string:
		parse = v == "1"
	}
	headers := map[string]string{}
	if h, ok := r["header"].(map[string]any); ok {
		for k, v := range h {
			headers[k] = fmt.Sprint(v)
		}
	}
	var out []Stream
	switch u := r["url"].(type) {
	case string:
		if u != "" {
			out = append(out, Stream{Name: "默认", URL: u, Parse: parse, Headers: headers})
		}
	case []any:
		for i := 0; i+1 < len(u); i += 2 {
			n, _ := u[i].(string)
			v, _ := u[i+1].(string)
			if v != "" {
				out = append(out, Stream{Name: n, URL: v, Parse: parse, Headers: headers})
			}
		}
	}
	return out
}

// IsLoopback reports whether u points at this machine.
func IsLoopback(u string) bool {
	pu, err := url.Parse(u)
	if err != nil {
		return false
	}
	h := pu.Hostname()
	return h == "127.0.0.1" || h == "localhost" || h == "::1"
}

// SameHost reports whether u is served by base (http://127.0.0.1:port),
// treating localhost and 127.0.0.1 as equal.
func SameHost(u, base string) bool {
	a, err1 := url.Parse(u)
	b, err2 := url.Parse(base)
	if err1 != nil || err2 != nil || !IsLoopback(u) || !IsLoopback(base) {
		return false
	}
	return a.Port() == b.Port()
}

// CheckLocalStreams rejects loopback URLs that are not served by the
// running source (base) or by an allowed local service (the optional,
// user-configured play bridge).
func CheckLocalStreams(streams []Stream, base string, allowed ...string) error {
	for _, s := range streams {
		ok := SameHost(s.URL, base)
		for _, a := range allowed {
			ok = ok || SameHost(s.URL, "http://"+a)
		}
		if IsLoopback(s.URL) && !ok {
			pu, _ := url.Parse(s.URL)
			return fmt.Errorf("该站点解析失败，返回了本机地址 %s，但没有服务在该端口。请换一个站点或线路。", pu.Host)
		}
	}
	return nil
}

// IsWebPage reports URLs that look like HTML pages rather than media
// (some plugins return parse:0 with a page). Pages without such an
// extension are detected by Content-Type in Preflight.
func IsWebPage(u string) bool {
	pu, err := url.Parse(u)
	if err != nil {
		return false
	}
	p := strings.ToLower(pu.Path)
	if strings.HasSuffix(p, ".html") || strings.HasSuffix(p, ".htm") || strings.HasSuffix(p, ".php") {
		return true
	}
	return false
}
