package tvbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// HTTPClient builds a client that dials hosts overrides. A nil or empty map
// uses the default transport. Timeout is per request, not per stream.
func HTTPClient(hosts map[string]string, timeout time.Duration) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if len(hosts) > 0 {
		dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, RewriteDial(addr, hosts))
		}
	}
	return &http.Client{Timeout: timeout, Transport: tr}
}

type cmsClient struct {
	http    *http.Client
	api     string
	xml     bool
	header  map[string]string
	playURL string
}

func newCMS(src Source, client *http.Client) *cmsClient {
	if client == nil {
		client = HTTPClient(nil, 25*time.Second)
	}
	return &cmsClient{http: client, api: src.API, xml: src.Kind == "cms-xml", header: src.Header, playURL: src.PlayURL}
}

func (c *cmsClient) home(ctx context.Context) (map[string]any, error) {
	doc, err := c.get(ctx, url.Values{"ac": {"list"}})
	if err != nil {
		return nil, err
	}
	if len(doc.classes) == 0 {
		if more, err := c.get(ctx, url.Values{"ac": {c.listAction()}, "pg": {"1"}}); err == nil && len(more.classes) > 0 {
			doc.classes = more.classes
			if len(doc.list) == 0 {
				doc.list = more.list
			}
		}
	}
	return map[string]any{"class": doc.classes, "list": doc.list}, nil
}

func (c *cmsClient) category(ctx context.Context, tid string, page int, filters map[string]string) (map[string]any, error) {
	q := url.Values{"ac": {c.listAction()}, "t": {tid}, "pg": {strconv.Itoa(page)}}
	for k, v := range filters {
		if k != "" && v != "" {
			q.Set(k, v)
		}
	}
	doc, err := c.get(ctx, q)
	if err != nil {
		return nil, err
	}
	if len(doc.list) == 0 && c.listAction() == "videolist" {
		q.Set("ac", "detail")
		if more, err := c.get(ctx, q); err == nil && len(more.list) > 0 {
			doc = more
		}
	}
	return pageMap(doc), nil
}

func (c *cmsClient) search(ctx context.Context, wd string, page int) (map[string]any, error) {
	doc, err := c.get(ctx, url.Values{"ac": {c.listAction()}, "wd": {wd}, "pg": {strconv.Itoa(page)}})
	if err != nil {
		return nil, err
	}
	return pageMap(doc), nil
}

func (c *cmsClient) detail(ctx context.Context, id string) (map[string]any, error) {
	doc, err := c.get(ctx, url.Values{"ac": {"detail"}, "ids": {id}})
	if err != nil {
		return nil, err
	}
	if len(doc.list) == 0 {
		if more, err := c.get(ctx, url.Values{"ac": {"videolist"}, "ids": {id}}); err == nil && len(more.list) > 0 {
			doc = more
		}
	}
	return map[string]any{"list": doc.list}, nil
}

func (c *cmsClient) play(id string) map[string]any {
	u := id
	parse := 0
	if c.playURL != "" {
		if strings.Contains(c.playURL, "url=") || strings.HasSuffix(c.playURL, "=") {
			u = c.playURL + url.QueryEscape(id)
		} else {
			u = strings.TrimRight(c.playURL, "/") + "/" + id
		}
		parse = 1
	}
	out := map[string]any{"parse": parse, "url": u}
	if len(c.header) > 0 {
		h := map[string]any{}
		for k, v := range c.header {
			h[k] = v
		}
		out["header"] = h
	}
	return out
}

func (c *cmsClient) listAction() string {
	if c.xml {
		return "videolist"
	}
	return "videolist"
}

type cmsDoc struct {
	classes []any
	list    []any
	page    int
	pages   int
	limit   int
	total   int
}

func pageMap(doc cmsDoc) map[string]any {
	if doc.page < 1 {
		doc.page = 1
	}
	if doc.pages < 1 {
		doc.pages = 1
	}
	return map[string]any{
		"page": doc.page, "pagecount": doc.pages, "limit": doc.limit, "total": doc.total, "list": doc.list,
	}
}

func (c *cmsClient) get(ctx context.Context, q url.Values) (cmsDoc, error) {
	u, err := url.Parse(c.api)
	if err != nil {
		return cmsDoc{}, err
	}
	exist := u.Query()
	for k, vs := range q {
		for _, v := range vs {
			exist.Set(k, v)
		}
	}
	u.RawQuery = exist.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return cmsDoc{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 JSTVPlayer")
	for k, v := range c.header {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return cmsDoc{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return cmsDoc{}, err
	}
	if resp.StatusCode != 200 {
		return cmsDoc{}, fmt.Errorf("采集接口 HTTP %d", resp.StatusCode)
	}
	body = maybeGBK(body)
	if c.xml || looksXML(body) {
		return parseCMSXML(body)
	}
	return parseCMSJSON(body)
}

func looksXML(b []byte) bool {
	s := strings.TrimSpace(string(b[:min(len(b), 64)]))
	return strings.HasPrefix(s, "<")
}

func maybeGBK(b []byte) []byte {
	head := strings.ToLower(string(b[:min(len(b), 200)]))
	if !strings.Contains(head, "gbk") && !strings.Contains(head, "gb2312") {
		return b
	}
	out, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), b)
	if err != nil {
		return b
	}
	return out
}

func parseCMSJSON(body []byte) (cmsDoc, error) {
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return cmsDoc{}, errBadJSON
	}
	doc := cmsDoc{
		page: num(raw["page"]), pages: num(raw["pagecount"]), limit: num(raw["limit"]), total: num(raw["total"]),
	}
	if classes, ok := raw["class"].([]any); ok {
		for _, item := range classes {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			id := firstString(m, "type_id", "id")
			name := firstString(m, "type_name", "name")
			if id == "" || name == "" {
				continue
			}
			doc.classes = append(doc.classes, map[string]any{"type_id": id, "type_name": name})
		}
	}
	if list, ok := raw["list"].([]any); ok {
		for _, item := range list {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if vod := normalizeVOD(m); vod != nil {
				doc.list = append(doc.list, vod)
			}
		}
	}
	return doc, nil
}

func normalizeVOD(m map[string]any) map[string]any {
	id := firstString(m, "vod_id", "id")
	name := firstString(m, "vod_name", "name")
	if id == "" || name == "" {
		return nil
	}
	vod := map[string]any{
		"vod_id": id, "vod_name": name,
		"vod_pic": firstString(m, "vod_pic", "pic"), "vod_remarks": firstString(m, "vod_remarks", "note", "vod_remarks"),
		"vod_year": firstString(m, "vod_year", "year"), "vod_area": firstString(m, "vod_area", "area"),
		"vod_actor": firstString(m, "vod_actor", "actor"), "vod_director": firstString(m, "vod_director", "director"),
		"vod_content": firstString(m, "vod_content", "des", "content"), "type_name": firstString(m, "type_name", "type"),
	}
	if from := firstString(m, "vod_play_from"); from != "" {
		vod["vod_play_from"] = from
	}
	if play := firstString(m, "vod_play_url"); play != "" {
		vod["vod_play_url"] = play
	}
	return vod
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s := scalar(m[k]); s != "" {
			return s
		}
	}
	return ""
}

func scalar(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case json.Number:
		return t.String()
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return ""
	}
}

func num(v any) int {
	n, _ := strconv.Atoi(scalar(v))
	return n
}
