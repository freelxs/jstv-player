package kazumi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultBangumiNextBase = "https://next.bgm.tv"
	defaultBangumiAPIBase  = "https://api.bgm.tv"
	bangumiUserAgent       = "jstv-player/0.6 (https://github.com/jstv-player)"
	bangumiPageSize        = 24
)

var yearPattern = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)

type bangumiCacheEntry struct {
	data      any
	expiresAt time.Time
}

// BangumiItem is a normalized anime catalog card fetched from Bangumi.tv.
type BangumiItem struct {
	ID       int
	Name     string
	NameRaw  string
	Pic      string
	Summary  string
	Score    float64
	Rank     int
	Year     string
	Area     string
	Platform string
	Tags     []string
	Director string
	Actor    string
	Watchers int
}

// BangumiClient fetches anime recommendations, weekly schedules, and filtered
// categories from Bangumi.tv public endpoints with short in-memory caching.
type BangumiClient struct {
	HTTP        *http.Client
	NextBaseURL string
	APIBaseURL  string
	Now         func() time.Time
	// Token optionally returns a user-supplied Bangumi personal Access Token.
	// When non-empty, it is sent as Bearer auth only to Bangumi endpoints.
	Token func() string

	mu    sync.Mutex
	cache map[string]bangumiCacheEntry
}

func (b *BangumiClient) httpClient() *http.Client {
	if b != nil && b.HTTP != nil {
		return b.HTTP
	}
	return http.DefaultClient
}

func (b *BangumiClient) nextBase() string {
	if b != nil && strings.TrimSpace(b.NextBaseURL) != "" {
		return strings.TrimRight(strings.TrimSpace(b.NextBaseURL), "/")
	}
	return defaultBangumiNextBase
}

func (b *BangumiClient) apiBase() string {
	if b != nil && strings.TrimSpace(b.APIBaseURL) != "" {
		return strings.TrimRight(strings.TrimSpace(b.APIBaseURL), "/")
	}
	return defaultBangumiAPIBase
}

func (b *BangumiClient) now() time.Time {
	if b != nil && b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *BangumiClient) token() string {
	if b == nil || b.Token == nil {
		return ""
	}
	tok := strings.TrimSpace(b.Token())
	if tok == "" || strings.ContainsAny(tok, "\r\n") {
		return ""
	}
	return tok
}

func (b *BangumiClient) shouldSendToken(target string) bool {
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return false
	}
	targetHost := strings.ToLower(u.Host)
	for _, base := range []string{b.apiBase(), b.nextBase()} {
		if bu, err := url.Parse(base); err == nil && bu.Host != "" && strings.ToLower(bu.Host) == targetHost {
			return true
		}
	}
	return false
}

func (b *BangumiClient) getCached(key string) (any, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cache == nil {
		return nil, false
	}
	entry, ok := b.cache[key]
	if !ok || b.now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.data, true
}

func (b *BangumiClient) setCached(key string, val any, ttl time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cache == nil {
		b.cache = map[string]bangumiCacheEntry{}
	}
	if len(b.cache) > 128 {
		now := b.now()
		for k, v := range b.cache {
			if now.After(v.expiresAt) {
				delete(b.cache, k)
			}
		}
	}
	b.cache[key] = bangumiCacheEntry{data: val, expiresAt: b.now().Add(ttl)}
}

// HomePayload builds the CatVod-compatible categories and filters for the
// Bangumi anime catalog (热门推荐 / 新番时间表 / 番剧分类 / 高分榜单 / 国创动画 / 剧场版).
func (b *BangumiClient) HomePayload() map[string]any {
	today := bangumiWeekday(b.now())
	years := bangumiYearOptions(b.now().Year())
	sortOpts := []map[string]string{
		{"value": "heat", "label": "按热度"},
		{"value": "rank", "label": "按排名"},
		{"value": "score", "label": "按评分"},
	}
	tagOpts := []map[string]string{
		{"value": "日本", "label": "日本"},
		{"value": "中国", "label": "国创"},
		{"value": "原创", "label": "原创"},
		{"value": "漫画改", "label": "漫画改"},
		{"value": "轻小说改", "label": "轻小说改"},
		{"value": "游戏改", "label": "游戏改"},
		{"value": "奇幻", "label": "奇幻"},
		{"value": "搞笑", "label": "搞笑"},
		{"value": "日常", "label": "日常"},
		{"value": "恋爱", "label": "恋爱"},
		{"value": "校园", "label": "校园"},
		{"value": "战斗", "label": "战斗"},
		{"value": "热血", "label": "热血"},
		{"value": "悬疑", "label": "悬疑"},
		{"value": "科幻", "label": "科幻"},
		{"value": "治愈", "label": "治愈"},
		{"value": "异世界", "label": "异世界"},
		{"value": "百合", "label": "百合"},
		{"value": "机战", "label": "机战"},
		{"value": "剧场版", "label": "剧场版"},
	}
	dayOpts := []map[string]string{
		{"value": "1", "label": "周一"},
		{"value": "2", "label": "周二"},
		{"value": "3", "label": "周三"},
		{"value": "4", "label": "周四"},
		{"value": "5", "label": "周五"},
		{"value": "6", "label": "周六"},
		{"value": "7", "label": "周日"},
	}
	return map[string]any{
		"class": []map[string]any{
			{"type_id": "trending", "type_name": "热门推荐"},
			{"type_id": "calendar", "type_name": "新番时间表"},
			{"type_id": "browse", "type_name": "番剧分类"},
			{"type_id": "top", "type_name": "高分榜单"},
			{"type_id": "cn", "type_name": "国创动画"},
			{"type_id": "movie", "type_name": "剧场版"},
		},
		"filters": map[string]any{
			"calendar": []map[string]any{
				{"key": "day", "label": "放送日", "init": strconv.Itoa(today), "options": dayOpts},
			},
			"browse": []map[string]any{
				{"key": "tag", "label": "题材", "options": tagOpts},
				{"key": "year", "label": "年份", "options": years},
				{"key": "sort", "label": "排序", "init": "heat", "options": sortOpts},
			},
			"top": []map[string]any{
				{"key": "year", "label": "年份", "options": years},
			},
			"cn": []map[string]any{
				{"key": "year", "label": "年份", "options": years},
				{"key": "sort", "label": "排序", "init": "heat", "options": sortOpts},
			},
			"movie": []map[string]any{
				{"key": "year", "label": "年份", "options": years},
				{"key": "sort", "label": "排序", "init": "heat", "options": sortOpts},
			},
		},
	}
}

func bangumiWeekday(t time.Time) int {
	wd := int(t.Weekday())
	if wd == 0 {
		return 7
	}
	return wd
}

func bangumiYearOptions(curYear int) []map[string]string {
	if curYear < 2024 {
		curYear = 2026
	}
	var out []map[string]string
	for y := curYear; y >= curYear-6; y-- {
		ys := strconv.Itoa(y)
		out = append(out, map[string]string{"value": ys, "label": ys})
	}
	out = append(out,
		map[string]string{"value": "2010s", "label": "2010年代"},
		map[string]string{"value": "2000s", "label": "2000年代"},
		map[string]string{"value": "classic", "label": "2000以前"},
	)
	return out
}

// CategoryPage fetches one page of a Bangumi catalog category and formats it as
// a CatVod category response. prefix should be "msearch:bgm:" (all rules) or
// "ssearch:bgm:" (current rule only).
func (b *BangumiClient) CategoryPage(ctx context.Context, tid string, page int, filters map[string]string, prefix string) (map[string]any, error) {
	if page < 1 {
		page = 1
	}
	var items []BangumiItem
	var total int
	var err error
	switch tid {
	case "", "trending":
		items, total, err = b.Trending(ctx, page, bangumiPageSize)
	case "calendar":
		items, total, err = b.Calendar(ctx, filters["day"])
	case "top":
		items, total, err = b.SearchSubjects(ctx, page, bangumiPageSize, "rank", []string{"日本"}, filters["year"], true)
	case "cn":
		sortBy := nonEmpty(filters["sort"], "heat")
		items, total, err = b.SearchSubjects(ctx, page, bangumiPageSize, sortBy, []string{"中国"}, filters["year"], false)
	case "movie":
		sortBy := nonEmpty(filters["sort"], "heat")
		items, total, err = b.SearchSubjects(ctx, page, bangumiPageSize, sortBy, []string{"剧场版"}, filters["year"], false)
	case "browse":
		sortBy := nonEmpty(filters["sort"], "heat")
		var tags []string
		if t := strings.TrimSpace(filters["tag"]); t != "" {
			tags = append(tags, t)
		}
		items, total, err = b.SearchSubjects(ctx, page, bangumiPageSize, sortBy, tags, filters["year"], sortBy == "rank")
	default:
		items, total, err = b.Trending(ctx, page, bangumiPageSize)
	}
	if err != nil {
		return nil, err
	}
	pageCount := 1
	if tid != "calendar" {
		if total > 0 {
			pageCount = max(1, (total+bangumiPageSize-1)/bangumiPageSize)
		} else if len(items) >= bangumiPageSize {
			pageCount = page + 1
		} else {
			pageCount = page
		}
	}
	list := make([]any, 0, len(items))
	for _, item := range items {
		remarks := ""
		if item.Score > 0 {
			remarks = fmt.Sprintf("★ %.1f", item.Score)
		} else if item.Watchers > 0 {
			remarks = fmt.Sprintf("%d 人在看", item.Watchers)
		}
		subtitle := item.Platform
		if len(item.Tags) > 0 {
			limit := min(2, len(item.Tags))
			joined := strings.Join(item.Tags[:limit], " / ")
			if subtitle != "" {
				subtitle += " · " + joined
			} else {
				subtitle = joined
			}
		}
		list = append(list, map[string]any{
			"vod_id":      fmt.Sprintf("%s%d", prefix, item.ID),
			"vod_name":    item.Name,
			"vod_pic":     item.Pic,
			"vod_remarks": remarks,
			"vod_year":    item.Year,
			"type_name":   subtitle,
		})
	}
	return map[string]any{
		"page":      page,
		"pagecount": pageCount,
		"limit":     bangumiPageSize,
		"total":     max(total, len(list)),
		"list":      list,
	}, nil
}

// Trending returns currently popular anime from Bangumi Next trending API.
func (b *BangumiClient) Trending(ctx context.Context, page, limit int) ([]BangumiItem, int, error) {
	offset := (page - 1) * limit
	key := fmt.Sprintf("trending:%d:%d", limit, offset)
	type cachedResult struct {
		items []BangumiItem
		total int
	}
	if v, ok := b.getCached(key); ok {
		c := v.(cachedResult)
		return c.items, c.total, nil
	}
	u := fmt.Sprintf("%s/p1/trending/subjects?type=2&limit=%d&offset=%d", b.nextBase(), limit, offset)
	raw, err := b.doJSON(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	var resp struct {
		Total int `json:"total"`
		Data  []struct {
			Subject bangumiNextSubject `json:"subject"`
			Count   int                `json:"count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, 0, fmt.Errorf("解析 Bangumi 热门数据失败: %w", err)
	}
	items := make([]BangumiItem, 0, len(resp.Data))
	for _, row := range resp.Data {
		it := row.Subject.toItem(row.Count)
		if it.Name != "" {
			b.rememberItem(it)
			items = append(items, it)
		}
	}
	b.setCached(key, cachedResult{items: items, total: resp.Total}, 5*time.Minute)
	return items, resp.Total, nil
}

// Calendar returns the weekly anime broadcast schedule from Bangumi Next calendar API.
func (b *BangumiClient) Calendar(ctx context.Context, day string) ([]BangumiItem, int, error) {
	const key = "calendar"
	var byDay map[int][]BangumiItem
	if v, ok := b.getCached(key); ok {
		byDay = v.(map[int][]BangumiItem)
	} else {
		u := b.nextBase() + "/p1/calendar"
		raw, err := b.doJSON(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, 0, err
		}
		var resp map[string][]struct {
			Subject  bangumiNextSubject `json:"subject"`
			Watchers int                `json:"watchers"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, 0, fmt.Errorf("解析 Bangumi 时间表失败: %w", err)
		}
		byDay = make(map[int][]BangumiItem, 7)
		dayLabels := []string{"", "周一", "周二", "周三", "周四", "周五", "周六", "周日"}
		for d := 1; d <= 7; d++ {
			rows := resp[strconv.Itoa(d)]
			list := make([]BangumiItem, 0, len(rows))
			for _, row := range rows {
				it := row.Subject.toItem(row.Watchers)
				if it.Name == "" {
					continue
				}
				if it.Platform == "" {
					it.Platform = dayLabels[d]
				} else {
					it.Platform = dayLabels[d] + " · " + it.Platform
				}
				b.rememberItem(it)
				list = append(list, it)
			}
			sort.SliceStable(list, func(i, j int) bool {
				if list[i].Watchers != list[j].Watchers {
					return list[i].Watchers > list[j].Watchers
				}
				return list[i].Score > list[j].Score
			})
			byDay[d] = list
		}
		b.setCached(key, byDay, 10*time.Minute)
	}
	if d, err := strconv.Atoi(strings.TrimSpace(day)); err == nil && d >= 1 && d <= 7 {
		list := byDay[d]
		return list, len(list), nil
	}
	var all []BangumiItem
	for d := 1; d <= 7; d++ {
		all = append(all, byDay[d]...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Watchers != all[j].Watchers {
			return all[i].Watchers > all[j].Watchers
		}
		return all[i].Score > all[j].Score
	})
	return all, len(all), nil
}

// SearchSubjects queries Bangumi v0 search subjects endpoint for category/tag/year browsing.
func (b *BangumiClient) SearchSubjects(ctx context.Context, page, limit int, sortBy string, tags []string, year string, rankedOnly bool) ([]BangumiItem, int, error) {
	offset := (page - 1) * limit
	key := fmt.Sprintf("search:%d:%d:%s:%s:%s:%t", limit, offset, sortBy, strings.Join(tags, ","), year, rankedOnly)
	type cachedResult struct {
		items []BangumiItem
		total int
	}
	if v, ok := b.getCached(key); ok {
		c := v.(cachedResult)
		return c.items, c.total, nil
	}
	filter := map[string]any{
		"type": []int{2},
		"nsfw": false,
	}
	if len(tags) > 0 {
		filter["tag"] = tags
	}
	if dateRange := yearToAirDateRange(year); len(dateRange) > 0 {
		filter["air_date"] = dateRange
	}
	if rankedOnly {
		filter["rank"] = []string{">0", "<=99999"}
	}
	payload := map[string]any{
		"keyword": "",
		"sort":    sortBy,
		"filter":  filter,
	}
	u := fmt.Sprintf("%s/v0/search/subjects?limit=%d&offset=%d", b.apiBase(), limit, offset)
	raw, err := b.doJSON(ctx, http.MethodPost, u, payload)
	if err != nil {
		return nil, 0, err
	}
	var resp struct {
		Total int                `json:"total"`
		Data  []bangumiV0Subject `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, 0, fmt.Errorf("解析 Bangumi 分类数据失败: %w", err)
	}
	items := make([]BangumiItem, 0, len(resp.Data))
	for _, row := range resp.Data {
		it := row.toItem()
		if it.Name == "" {
			continue
		}
		b.rememberItem(it)
		items = append(items, it)
	}
	b.setCached(key, cachedResult{items: items, total: resp.Total}, 5*time.Minute)
	return items, resp.Total, nil
}

// LookupSubject queries Bangumi v0 API by title to enrich anime detail pages
// with official Chinese synopsis (summary), score, release year, tags, and staff.
func (b *BangumiClient) LookupSubject(ctx context.Context, rawTitle string) (*BangumiItem, error) {
	if b == nil {
		return nil, nil
	}
	query := cleanBangumiLookupTitle(rawTitle)
	norm := normalizeBangumiTitle(query)
	if query == "" || norm == "" {
		return nil, nil
	}
	hasToken := b.token() != ""
	authTag := "anon"
	if hasToken {
		authTag = "auth"
	}
	lookupKey := "lookup:" + authTag + ":" + norm
	if v, ok := b.getCached(lookupKey); ok {
		if item, ok := v.(*BangumiItem); ok {
			return item, nil
		}
		return nil, nil
	}
	if v, ok := b.getCached("title:" + norm); ok {
		if known, ok := v.(BangumiItem); ok {
			if known.Summary != "" {
				cp := known
				b.setCached(lookupKey, &cp, 15*time.Minute)
				return &cp, nil
			}
			if known.ID > 0 {
				if full, err := b.SubjectByID(ctx, known.ID); err == nil && full != nil {
					merged := mergeBangumiItem(*full, known)
					b.rememberItem(merged)
					b.setCached(lookupKey, &merged, 15*time.Minute)
					return &merged, nil
				}
			}
		}
	}
	filter := map[string]any{
		"type": []int{2},
	}
	if !hasToken {
		filter["nsfw"] = false
	}
	payload := map[string]any{
		"keyword": query,
		"filter":  filter,
	}
	u := fmt.Sprintf("%s/v0/search/subjects?limit=5&offset=0", b.apiBase())
	raw, err := b.doJSON(ctx, http.MethodPost, u, payload)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []bangumiV0Subject `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	best, ok := pickBestBangumiSubject(query, resp.Data)
	if !ok {
		b.setCached(lookupKey, (*BangumiItem)(nil), 5*time.Minute)
		return nil, nil
	}
	item := best.toItem()
	if item.ID > 0 && (item.Summary == "" || (item.Director == "" && item.Actor == "")) {
		if full, err := b.SubjectByID(ctx, item.ID); err == nil && full != nil {
			item = mergeBangumiItem(item, *full)
		}
	}
	b.rememberItem(item)
	b.setCached(lookupKey, &item, 15*time.Minute)
	return &item, nil
}

// SubjectByID fetches a single subject from GET /v0/subjects/{id}.
func (b *BangumiClient) SubjectByID(ctx context.Context, id int) (*BangumiItem, error) {
	if b == nil || id <= 0 {
		return nil, nil
	}
	key := fmt.Sprintf("subject:%d", id)
	if v, ok := b.getCached(key); ok {
		if item, ok := v.(*BangumiItem); ok {
			return item, nil
		}
	}
	u := fmt.Sprintf("%s/v0/subjects/%d", b.apiBase(), id)
	raw, err := b.doJSON(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	var subj bangumiV0Subject
	if err := json.Unmarshal(raw, &subj); err != nil {
		return nil, err
	}
	item := subj.toItem()
	if item.Name == "" {
		return nil, nil
	}
	b.rememberItem(item)
	b.setCached(key, &item, 15*time.Minute)
	return &item, nil
}

func (b *BangumiClient) rememberItem(it BangumiItem) {
	if b == nil || it.Name == "" {
		return
	}
	for _, raw := range []string{it.Name, it.NameRaw} {
		norm := normalizeBangumiTitle(raw)
		if norm == "" {
			continue
		}
		if prev, ok := b.getCached("title:" + norm); ok {
			if old, ok := prev.(BangumiItem); ok {
				it = mergeBangumiItem(it, old)
			}
		}
		b.setCached("title:"+norm, it, 15*time.Minute)
	}
}

func mergeBangumiItem(primary, fallback BangumiItem) BangumiItem {
	if primary.ID == 0 {
		primary.ID = fallback.ID
	}
	primary.Name = firstNonEmpty(primary.Name, fallback.Name)
	primary.NameRaw = firstNonEmpty(primary.NameRaw, fallback.NameRaw)
	primary.Pic = firstNonEmpty(primary.Pic, fallback.Pic)
	primary.Summary = firstNonEmpty(primary.Summary, fallback.Summary)
	if primary.Score == 0 {
		primary.Score = fallback.Score
	}
	if primary.Rank == 0 {
		primary.Rank = fallback.Rank
	}
	primary.Year = firstNonEmpty(primary.Year, fallback.Year)
	primary.Area = firstNonEmpty(primary.Area, fallback.Area)
	primary.Platform = firstNonEmpty(primary.Platform, fallback.Platform)
	if len(primary.Tags) == 0 {
		primary.Tags = fallback.Tags
	}
	primary.Director = firstNonEmpty(primary.Director, fallback.Director)
	primary.Actor = firstNonEmpty(primary.Actor, fallback.Actor)
	if primary.Watchers == 0 {
		primary.Watchers = fallback.Watchers
	}
	return primary
}

func yearToAirDateRange(year string) []string {
	year = strings.TrimSpace(year)
	if year == "" {
		return nil
	}
	switch year {
	case "2010s":
		return []string{">=2010-01-01", "<2020-01-01"}
	case "2000s":
		return []string{">=2000-01-01", "<2010-01-01"}
	case "classic":
		return []string{"<2000-01-01"}
	}
	if y, err := strconv.Atoi(year); err == nil && y >= 1950 && y <= 2100 {
		return []string{fmt.Sprintf(">=%d-01-01", y), fmt.Sprintf("<%d-01-01", y+1)}
	}
	return nil
}

func (b *BangumiClient) doJSON(ctx context.Context, method, target string, body any) ([]byte, error) {
	if _, err := url.Parse(target); err != nil {
		return nil, err
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", bangumiUserAgent)
	req.Header.Set("Accept", "application/json")
	if tok := b.token(); tok != "" && b.shouldSendToken(target) {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := b.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("访问 Bangumi 目录失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Bangumi 目录返回 HTTP %d", resp.StatusCode)
	}
	return data, nil
}

type bangumiNextSubject struct {
	ID       int      `json:"id"`
	Name     string   `json:"name"`
	NameCN   string   `json:"nameCN"`
	Info     string   `json:"info"`
	MetaTags []string `json:"metaTags"`
	Rating   struct {
		Rank  int     `json:"rank"`
		Score float64 `json:"score"`
	} `json:"rating"`
	Platform struct {
		Type   string `json:"type"`
		TypeCN string `json:"typeCN"`
	} `json:"platform"`
	Images struct {
		Common string `json:"common"`
		Large  string `json:"large"`
		Medium string `json:"medium"`
	} `json:"images"`
}

func (s bangumiNextSubject) toItem(watchers int) BangumiItem {
	title := strings.TrimSpace(s.NameCN)
	if title == "" {
		title = strings.TrimSpace(s.Name)
	}
	pic := firstNonEmpty(s.Images.Common, s.Images.Large, s.Images.Medium)
	yr := ""
	if m := yearPattern.FindString(s.Info); m != "" {
		yr = m
	}
	plat := firstNonEmpty(s.Platform.TypeCN, s.Platform.Type)
	return BangumiItem{
		ID:       s.ID,
		Name:     title,
		NameRaw:  strings.TrimSpace(s.Name),
		Pic:      pic,
		Score:    s.Rating.Score,
		Rank:     s.Rating.Rank,
		Year:     yr,
		Area:     extractBangumiArea(s.MetaTags),
		Platform: plat,
		Tags:     cleanBangumiTags(s.MetaTags),
		Watchers: watchers,
	}
}

type bangumiV0Subject struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	NameCN   string `json:"name_cn"`
	Summary  string `json:"summary"`
	Date     string `json:"date"`
	Platform string `json:"platform"`
	Image    string `json:"image"`
	Images   struct {
		Common string `json:"common"`
		Large  string `json:"large"`
		Medium string `json:"medium"`
	} `json:"images"`
	Rating struct {
		Rank  int     `json:"rank"`
		Score float64 `json:"score"`
	} `json:"rating"`
	MetaTags []string `json:"meta_tags"`
	Infobox  []struct {
		Key   string `json:"key"`
		Value any    `json:"value"`
	} `json:"infobox"`
}

func (s bangumiV0Subject) toItem() BangumiItem {
	title := strings.TrimSpace(s.NameCN)
	if title == "" {
		title = strings.TrimSpace(s.Name)
	}
	pic := firstNonEmpty(s.Images.Common, s.Images.Large, s.Images.Medium, s.Image)
	yr := ""
	if len(s.Date) >= 4 {
		yr = s.Date[:4]
	}
	area := extractBangumiArea(s.MetaTags)
	var directors, actors []string
	for _, box := range s.Infobox {
		key := strings.TrimSpace(box.Key)
		val := extractInfoboxValue(box.Value)
		if val == "" {
			continue
		}
		switch key {
		case "导演", "总导演", "监督", "总监督", "原作", "动画制作":
			if len(directors) < 2 {
				directors = append(directors, val)
			}
		case "主演", "声优", "CV", "配音":
			if len(actors) < 2 {
				actors = append(actors, val)
			}
		case "地区", "国家", "制片国家/地区":
			if area == "" {
				area = val
			}
		case "放送开始", "上映年度", "发售日":
			if yr == "" {
				if m := yearPattern.FindString(val); m != "" {
					yr = m
				}
			}
		}
	}
	return BangumiItem{
		ID:       s.ID,
		Name:     title,
		NameRaw:  strings.TrimSpace(s.Name),
		Pic:      pic,
		Summary:  cleanSynopsisText(s.Summary),
		Score:    s.Rating.Score,
		Rank:     s.Rating.Rank,
		Year:     yr,
		Area:     area,
		Platform: strings.TrimSpace(s.Platform),
		Tags:     cleanBangumiTags(s.MetaTags),
		Director: strings.Join(directors, " / "),
		Actor:    strings.Join(actors, " / "),
	}
}

func extractInfoboxValue(raw any) string {
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		var parts []string
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if s, ok := m["v"].(string); ok && strings.TrimSpace(s) != "" {
					parts = append(parts, strings.TrimSpace(s))
				}
			} else if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				parts = append(parts, strings.TrimSpace(s))
			}
			if len(parts) >= 4 {
				break
			}
		}
		return strings.Join(parts, " / ")
	default:
		return ""
	}
}

func extractBangumiArea(tags []string) string {
	for _, t := range tags {
		t = strings.TrimSpace(t)
		switch t {
		case "日本", "中国", "欧美", "美国", "韩国":
			return t
		}
	}
	return ""
}

var (
	bracketNoiseRe = regexp.MustCompile(`(?i)(\[[^\]]*(?:完结|连载|更新|中字|简中|繁中|双语|国语|粤语|日语|高清|超清|1080p|720p|4k|bd|tv|无修)[^\]]*\]|【[^】]*】|\([^)]*(?:完结|连载|更新|中字|bd|1080p|720p)[^)]*\))`)
	titleNormRe    = regexp.MustCompile(`[\s\p{P}\p{S}]+`)
)

func cleanBangumiLookupTitle(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = bracketNoiseRe.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

func normalizeBangumiTitle(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	return titleNormRe.ReplaceAllString(s, "")
}

func pickBestBangumiSubject(query string, list []bangumiV0Subject) (bangumiV0Subject, bool) {
	if len(list) == 0 {
		return bangumiV0Subject{}, false
	}
	qNorm := normalizeBangumiTitle(query)
	for _, item := range list {
		if qNorm != "" && (normalizeBangumiTitle(item.NameCN) == qNorm || normalizeBangumiTitle(item.Name) == qNorm) {
			return item, true
		}
	}
	for _, item := range list {
		cn := normalizeBangumiTitle(item.NameCN)
		nm := normalizeBangumiTitle(item.Name)
		if qNorm != "" && ((cn != "" && (strings.Contains(cn, qNorm) || strings.Contains(qNorm, cn))) ||
			(nm != "" && (strings.Contains(nm, qNorm) || strings.Contains(qNorm, nm)))) {
			return item, true
		}
	}
	return list[0], true
}

func cleanBangumiTags(tags []string) []string {
	var out []string
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" || t == "TV" || t == "WEB" || t == "日本" {
			continue
		}
		out = append(out, t)
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func nonEmpty(val, fallback string) string {
	if s := strings.TrimSpace(val); s != "" {
		return s
	}
	return fallback
}
