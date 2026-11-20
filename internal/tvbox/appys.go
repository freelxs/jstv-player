package tvbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// appys speaks the public Apple CMS app API used by csp_AppYs and csp_AppYsV2.
// ext is the API root, for example https://cdn.example/api.php/app/.
func (s *Server) appys(ctx context.Context, src Source, action string, body map[string]any) (any, int, error) {
	if action == "init" {
		return map[string]any{}, http.StatusOK, nil
	}
	cfg, err := s.ruleConfig(ctx, src)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	base := strings.TrimSpace(firstNonEmpty(cfg["url"], cfg["api"]))
	if base == "" {
		return nil, http.StatusBadGateway, errors.New("AppYs 站点没有接口地址")
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	c := &appClient{srv: s, base: base, header: src.Header}
	switch action {
	case "home":
		class, list, err := c.home(ctx)
		if err != nil {
			return nil, http.StatusBadGateway, err
		}
		return map[string]any{"class": class, "list": list}, http.StatusOK, nil
	case "category":
		list, err := c.videos(ctx, scalar(body["id"]), positive(body["page"]), stringMap(body["filters"]))
		if err != nil {
			return nil, http.StatusBadGateway, err
		}
		page := positive(body["page"])
		pages := 1
		if len(list) > 0 {
			pages = page + 1
		}
		return map[string]any{"page": page, "pagecount": pages, "limit": len(list), "total": len(list), "list": list}, http.StatusOK, nil
	case "detail":
		vod, err := c.detail(ctx, scalar(body["id"]))
		if err != nil {
			return nil, http.StatusBadGateway, err
		}
		return map[string]any{"list": []any{vod}}, http.StatusOK, nil
	case "search":
		list, err := c.search(ctx, scalar(body["wd"]), positive(body["page"]))
		if err != nil {
			return nil, http.StatusBadGateway, err
		}
		page := positive(body["page"])
		pages := 1
		if len(list) > 0 {
			pages = page + 1
		}
		return map[string]any{"page": page, "pagecount": pages, "list": list}, http.StatusOK, nil
	case "play":
		return c.play(ctx, scalar(body["flag"]), scalar(body["id"]))
	default:
		return nil, http.StatusNotFound, errors.New("未知操作")
	}
}

type appClient struct {
	srv    *Server
	base   string
	header map[string]string
}

func (c *appClient) home(ctx context.Context) ([]any, []any, error) {
	doc, err := c.get(ctx, "nav")
	if err != nil {
		doc, err = c.get(ctx, "")
	}
	if err != nil {
		return nil, nil, err
	}
	class, list := splitApp(doc)
	return class, list, nil
}

func (c *appClient) videos(ctx context.Context, tid string, page int, filters map[string]string) ([]any, error) {
	q := url.Values{}
	q.Set("tid", tid)
	q.Set("pg", fmt.Sprintf("%d", page))
	for _, key := range []string{"class", "area", "lang", "year"} {
		if filters[key] != "" {
			q.Set(key, filters[key])
		}
	}
	doc, err := c.get(ctx, "video?"+q.Encode())
	if err != nil {
		return nil, err
	}
	_, list := splitApp(doc)
	return list, nil
}

func (c *appClient) search(ctx context.Context, wd string, page int) ([]any, error) {
	q := url.Values{}
	q.Set("text", wd)
	q.Set("pg", fmt.Sprintf("%d", page))
	doc, err := c.get(ctx, "search?"+q.Encode())
	if err != nil {
		doc, err = c.get(ctx, "search?wd="+url.QueryEscape(wd)+"&pg="+q.Get("pg"))
	}
	if err != nil {
		return nil, err
	}
	_, list := splitApp(doc)
	return list, nil
}

func (c *appClient) detail(ctx context.Context, id string) (map[string]any, error) {
	doc, err := c.get(ctx, "detail?id="+url.QueryEscape(id))
	if err != nil {
		doc, err = c.get(ctx, "video_detail?id="+url.QueryEscape(id))
	}
	if err != nil {
		return nil, err
	}
	_, list := splitApp(doc)
	if len(list) == 0 {
		return nil, errors.New("接口没有返回详情")
	}
	vod, _ := list[0].(map[string]any)
	if vod == nil {
		return nil, errors.New("接口没有返回详情")
	}
	return vod, nil
}

func (c *appClient) play(ctx context.Context, flag, id string) (any, int, error) {
	if strings.Contains(id, "://") {
		parse := 1
		if directMedia(id) {
			parse = 0
		}
		out := map[string]any{"parse": parse, "url": id}
		if len(c.header) > 0 {
			h := map[string]any{}
			for k, v := range c.header {
				h[k] = v
			}
			out["header"] = h
		}
		return out, http.StatusOK, nil
	}
	q := url.Values{}
	q.Set("id", id)
	if flag != "" {
		q.Set("from", flag)
	}
	doc, err := c.get(ctx, "play?"+q.Encode())
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	if m, ok := doc.(map[string]any); ok {
		if u := firstString(m, "url", "vod_url"); u != "" {
			parse := 1
			if directMedia(u) {
				parse = 0
			}
			return map[string]any{"parse": parse, "url": u}, http.StatusOK, nil
		}
	}
	_, list := splitApp(doc)
	if len(list) > 0 {
		if vod, ok := list[0].(map[string]any); ok {
			if u := firstString(vod, "url", "vod_url"); u != "" {
				return map[string]any{"parse": 0, "url": u}, http.StatusOK, nil
			}
		}
	}
	return nil, http.StatusBadGateway, errors.New("接口没有返回播放地址")
}

func (c *appClient) get(ctx context.Context, path string) (any, error) {
	text, _, err := c.srv.getText(ctx, c.base+strings.TrimPrefix(path, "/"), c.header)
	if err != nil {
		return nil, err
	}
	var v any
	if json.Unmarshal([]byte(strings.TrimSpace(text)), &v) != nil {
		return nil, errors.New("苹果接口没有返回 JSON")
	}
	return v, nil
}

func splitApp(v any) ([]any, []any) {
	var class, list []any
	for _, m := range collectObjects(v, 0) {
		if vod := appVod(m); vod != nil {
			list = append(list, vod)
			continue
		}
		id := firstString(m, "type_id", "id")
		name := firstString(m, "type_name", "name")
		if id != "" && name != "" && firstString(m, "vod_id", "vod_name") == "" {
			class = append(class, map[string]any{"type_id": id, "type_name": name})
		}
	}
	return class, list
}

func appVod(m map[string]any) map[string]any {
	if firstString(m, "vod_id", "vod_name") == "" && firstString(m, "type_id", "type_name") != "" {
		return nil
	}
	vod := normalizeVOD(m)
	if vod == nil {
		return nil
	}
	if _, ok := vod["vod_play_url"]; !ok {
		if u := firstString(m, "vod_url", "url"); strings.Contains(u, "://") {
			vod["vod_play_from"] = "播放"
			vod["vod_play_url"] = firstString(m, "vod_name", "name") + "$" + u
		}
	}
	return vod
}

func collectObjects(v any, depth int) []map[string]any {
	if depth > 4 || v == nil {
		return nil
	}
	switch t := v.(type) {
	case []any:
		var out []map[string]any
		for _, item := range t {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
				continue
			}
			out = append(out, collectObjects(item, depth+1)...)
		}
		return out
	case map[string]any:
		var out []map[string]any
		for _, key := range []string{"list", "data", "class", "video", "videos"} {
			if nested, ok := t[key]; ok {
				out = append(out, collectObjects(nested, depth+1)...)
			}
		}
		if len(out) == 0 && (firstString(t, "vod_id", "vod_name", "type_id", "type_name") != "") {
			out = append(out, t)
		}
		return out
	default:
		return nil
	}
}
