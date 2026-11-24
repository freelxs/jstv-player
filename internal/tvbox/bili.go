package tvbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	biliDefaultAPIBase = "https://api.bilibili.com"
	biliDefaultUA      = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

var biliTagRe = regexp.MustCompile(`<[^>]+>`)

type biliCookieCache struct {
	mu     sync.Mutex
	cookie string
	until  time.Time
}

var sharedBiliCookie biliCookieCache

type biliClient struct {
	src     Source
	http    *http.Client
	apiBase string
}

func (s *Server) bili(ctx context.Context, src Source, action string, body map[string]any) (any, int, error) {
	bc := &biliClient{src: src, http: s.client, apiBase: biliDefaultAPIBase}
	switch action {
	case "init":
		return map[string]any{}, http.StatusOK, nil
	case "home":
		out, err := bc.home(ctx)
		return out, http.StatusBadGateway, err
	case "category":
		out, err := bc.category(ctx, scalar(body["id"]), positive(body["page"]), stringMap(body["filters"]))
		return out, http.StatusBadGateway, err
	case "detail":
		out, err := bc.detail(ctx, scalar(body["id"]))
		return out, http.StatusBadGateway, err
	case "search":
		out, err := bc.search(ctx, scalar(body["wd"]), positive(body["page"]))
		return out, http.StatusBadGateway, err
	case "play":
		out, err := bc.play(ctx, scalar(body["id"]))
		return out, http.StatusBadGateway, err
	default:
		return nil, http.StatusNotFound, errors.New("未知操作")
	}
}

func (b *biliClient) loadConfig(ctx context.Context) (map[string]any, error) {
	raw := bytesTrim(b.src.Ext)
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, nil
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		str = strings.TrimSpace(str)
		if idx := strings.Index(str, "$$$"); idx >= 0 {
			str = strings.TrimSpace(str[:idx])
		}
		if isHTTP(str) {
			return b.fetchConfigJSON(ctx, str)
		}
		var parsed map[string]any
		if json.Unmarshal([]byte(str), &parsed) == nil {
			return parsed, nil
		}
		return map[string]any{}, nil
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil {
		return map[string]any{}, nil
	}
	if jsonURL := strings.TrimSpace(scalar(obj["json"])); isHTTP(jsonURL) {
		return b.fetchConfigJSON(ctx, jsonURL)
	}
	return obj, nil
}

func (b *biliClient) fetchConfigJSON(ctx context.Context, rawURL string) (map[string]any, error) {
	body, err := b.getBytes(ctx, rawURL, false)
	if err != nil {
		return nil, err
	}
	decoded, err := Decode(body)
	if err == nil {
		body = decoded
	}
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, fmt.Errorf("B站分类配置解析失败: %w", err)
	}
	return obj, nil
}

func (b *biliClient) home(ctx context.Context) (map[string]any, error) {
	cfg, err := b.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	rawClasses := cfg["class"]
	if rawClasses == nil {
		rawClasses = cfg["classes"]
	}
	var classes []any
	if items, ok := rawClasses.([]any); ok {
		for _, item := range items {
			m := asMap(item)
			tid := strings.TrimSpace(scalar(m["type_id"]))
			name := strings.TrimSpace(scalar(m["type_name"]))
			if tid == "" && name == "" {
				continue
			}
			if strings.EqualFold(tid, "peizhi") || strings.Contains(name, "登陆配置") || strings.Contains(name, "登录配置") {
				continue
			}
			if tid == "" {
				tid = name
			}
			if name == "" {
				name = tid
			}
			classes = append(classes, map[string]any{
				"type_id":   tid,
				"type_name": name,
			})
		}
	}
	if len(classes) == 0 {
		classes = []any{
			map[string]any{"type_id": "动画", "type_name": "动画"},
			map[string]any{"type_id": "纪录片", "type_name": "纪录片"},
			map[string]any{"type_id": "音乐", "type_name": "音乐"},
			map[string]any{"type_id": "科技", "type_name": "科技"},
		}
	}
	out := map[string]any{"class": classes}
	if filters := cfg["filters"]; filters != nil {
		out["filters"] = filters
	} else if filter := cfg["filter"]; filter != nil {
		out["filters"] = filter
	}
	return out, nil
}

func (b *biliClient) category(ctx context.Context, tid string, page int, filters map[string]string) (map[string]any, error) {
	keyword := strings.TrimSpace(tid)
	if sub := strings.TrimSpace(filters["tid"]); sub != "" {
		keyword = sub
	}
	if keyword == "" {
		return map[string]any{"page": page, "pagecount": 1, "list": []any{}}, nil
	}
	return b.searchVideos(ctx, keyword, page, filters["order"], filters["duration"])
}

func (b *biliClient) search(ctx context.Context, wd string, page int) (map[string]any, error) {
	wd = strings.TrimSpace(wd)
	if wd == "" {
		return map[string]any{"page": page, "pagecount": 1, "list": []any{}}, nil
	}
	return b.searchVideos(ctx, wd, page, "", "")
}

func (b *biliClient) searchVideos(ctx context.Context, keyword string, page int, order, duration string) (map[string]any, error) {
	vals := url.Values{}
	vals.Set("search_type", "video")
	vals.Set("keyword", keyword)
	vals.Set("page", strconv.Itoa(page))
	if strings.TrimSpace(order) != "" {
		vals.Set("order", strings.TrimSpace(order))
	}
	if strings.TrimSpace(duration) != "" {
		vals.Set("duration", strings.TrimSpace(duration))
	}
	endpoint := strings.TrimRight(b.apiBase, "/") + "/x/web-interface/search/type?" + vals.Encode()
	raw, err := b.getBytes(ctx, endpoint, true)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			NumPages int `json:"numPages"`
			Result   []struct {
				BVID     string `json:"bvid"`
				AID      int64  `json:"aid"`
				Title    string `json:"title"`
				Pic      string `json:"pic"`
				Duration string `json:"duration"`
				Author   string `json:"author"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		if resp.Message == "" {
			resp.Message = fmt.Sprintf("B站接口返回错误 %d", resp.Code)
		}
		return nil, errors.New(resp.Message)
	}
	list := make([]any, 0, len(resp.Data.Result))
	for _, item := range resp.Data.Result {
		vid := strings.TrimSpace(item.BVID)
		if vid == "" && item.AID > 0 {
			vid = strconv.FormatInt(item.AID, 10)
		}
		if vid == "" {
			continue
		}
		title := cleanBiliText(item.Title)
		remarks := strings.TrimSpace(item.Duration)
		if item.Author != "" {
			if remarks != "" {
				remarks += " · " + item.Author
			} else {
				remarks = item.Author
			}
		}
		list = append(list, map[string]any{
			"vod_id":      vid,
			"vod_name":    title,
			"vod_pic":     normalizeBiliPic(item.Pic),
			"vod_remarks": remarks,
		})
	}
	pageCount := resp.Data.NumPages
	if pageCount <= 0 {
		pageCount = 1
	}
	return map[string]any{
		"page":      page,
		"pagecount": pageCount,
		"list":      list,
	}, nil
}

func (b *biliClient) detail(ctx context.Context, id string) (map[string]any, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("缺少视频 ID")
	}
	query := "aid=" + url.QueryEscape(id)
	if strings.HasPrefix(strings.ToUpper(id), "BV") {
		query = "bvid=" + url.QueryEscape(id)
	}
	endpoint := strings.TrimRight(b.apiBase, "/") + "/x/web-interface/view?" + query
	raw, err := b.getBytes(ctx, endpoint, true)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			BVID  string `json:"bvid"`
			AID   int64  `json:"aid"`
			CID   int64  `json:"cid"`
			Title string `json:"title"`
			Pic   string `json:"pic"`
			Desc  string `json:"desc"`
			TName string `json:"tname"`
			Owner struct {
				Name string `json:"name"`
			} `json:"owner"`
			Pages []struct {
				CID  int64  `json:"cid"`
				Page int    `json:"page"`
				Part string `json:"part"`
			} `json:"pages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		if resp.Message == "" {
			resp.Message = fmt.Sprintf("B站详情返回错误 %d", resp.Code)
		}
		return nil, errors.New(resp.Message)
	}
	d := resp.Data
	vid := d.BVID
	if vid == "" {
		vid = id
	}
	var episodes []string
	for _, p := range d.Pages {
		label := strings.TrimSpace(p.Part)
		if label == "" {
			label = fmt.Sprintf("P%d", p.Page)
		}
		label = strings.NewReplacer("$", " ", "#", " ").Replace(label)
		episodes = append(episodes, fmt.Sprintf("%s$%s_%d", label, vid, p.CID))
	}
	if len(episodes) == 0 && d.CID > 0 {
		episodes = append(episodes, fmt.Sprintf("正片$%s_%d", vid, d.CID))
	}
	vod := map[string]any{
		"vod_id":        vid,
		"vod_name":      cleanBiliText(d.Title),
		"vod_pic":       normalizeBiliPic(d.Pic),
		"type_name":     d.TName,
		"vod_director":  d.Owner.Name,
		"vod_content":   strings.TrimSpace(d.Desc),
		"vod_play_from": "B站",
		"vod_play_url":  strings.Join(episodes, "#"),
	}
	return map[string]any{"list": []any{vod}}, nil
}

func (b *biliClient) play(ctx context.Context, id string) (map[string]any, error) {
	id = strings.TrimSpace(id)
	bvid, cid, hasSep := strings.Cut(id, "_")
	if !hasSep || cid == "" {
		det, err := b.detail(ctx, id)
		if err != nil {
			return nil, err
		}
		list, _ := det["list"].([]any)
		if len(list) == 0 {
			return nil, errors.New("未找到视频分P")
		}
		playURL := scalar(asMap(list[0])["vod_play_url"])
		firstEp := strings.Split(playURL, "#")[0]
		if _, epID, ok := strings.Cut(firstEp, "$"); ok {
			bvid, cid, _ = strings.Cut(epID, "_")
		}
	}
	if bvid == "" || cid == "" {
		return nil, errors.New("无效的 B站播放参数")
	}
	vals := url.Values{}
	if strings.HasPrefix(strings.ToUpper(bvid), "BV") {
		vals.Set("bvid", bvid)
	} else {
		vals.Set("avid", bvid)
	}
	vals.Set("cid", cid)
	vals.Set("qn", "64")
	vals.Set("fnval", "1")
	vals.Set("platform", "html5")
	vals.Set("high_quality", "1")
	endpoint := strings.TrimRight(b.apiBase, "/") + "/x/player/playurl?" + vals.Encode()
	raw, err := b.getBytes(ctx, endpoint, true)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			DURL []struct {
				URL string `json:"url"`
			} `json:"durl"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 0 || len(resp.Data.DURL) == 0 || resp.Data.DURL[0].URL == "" {
		if resp.Message == "" {
			resp.Message = "未获取到可播放的 B站地址"
		}
		return nil, errors.New(resp.Message)
	}
	return map[string]any{
		"parse": 0,
		"url":   resp.Data.DURL[0].URL,
		"header": map[string]string{
			"User-Agent": biliDefaultUA,
			"Referer":    "https://www.bilibili.com",
		},
	}, nil
}

func (b *biliClient) biliCookie(ctx context.Context) string {
	if b.apiBase != biliDefaultAPIBase {
		return "buvid3=infoc"
	}
	sharedBiliCookie.mu.Lock()
	if sharedBiliCookie.cookie != "" && time.Now().Before(sharedBiliCookie.until) {
		c := sharedBiliCookie.cookie
		sharedBiliCookie.mu.Unlock()
		return c
	}
	sharedBiliCookie.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, biliDefaultAPIBase+"/x/frontend/finger/spi", nil)
	if err != nil {
		return "buvid3=infoc"
	}
	req.Header.Set("User-Agent", biliDefaultUA)
	client := b.http
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "buvid3=infoc"
	}
	defer resp.Body.Close()
	var spi struct {
		Code int `json:"code"`
		Data struct {
			B3 string `json:"b_3"`
			B4 string `json:"b_4"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&spi) == nil && spi.Data.B3 != "" {
		c := "buvid3=" + spi.Data.B3
		if spi.Data.B4 != "" {
			c += "; buvid4=" + spi.Data.B4
		}
		sharedBiliCookie.mu.Lock()
		sharedBiliCookie.cookie = c
		sharedBiliCookie.until = time.Now().Add(30 * time.Minute)
		sharedBiliCookie.mu.Unlock()
		return c
	}
	return "buvid3=infoc"
}

func (b *biliClient) getBytes(ctx context.Context, rawURL string, withBiliHeaders bool) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", biliDefaultUA)
	if withBiliHeaders {
		cookie := b.biliCookie(ctx)
		req.Header.Set("Cookie", cookie)
		if cookie != "buvid3=infoc" {
			req.Header.Set("Referer", "https://www.bilibili.com/")
		}
	}
	client := b.http
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

func cleanBiliText(s string) string {
	s = biliTagRe.ReplaceAllString(s, "")
	return strings.TrimSpace(html.UnescapeString(s))
}

func normalizeBiliPic(pic string) string {
	pic = strings.TrimSpace(pic)
	if strings.HasPrefix(pic, "//") {
		return "https:" + pic
	}
	return pic
}
