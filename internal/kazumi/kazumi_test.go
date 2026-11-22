package kazumi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/antchfx/htmlquery"
)

func TestNormalizeSourceURL(t *testing.T) {
	cases := []struct {
		in   string
		want []string
		repo bool
	}{
		{
			in:   "https://github.com/Predidit/KazumiRules",
			want: []string{"https://raw.githubusercontent.com/Predidit/KazumiRules/main/index.json", "https://raw.githubusercontent.com/Predidit/KazumiRules/master/index.json"},
			repo: true,
		},
		{
			in:   "https://github.com/Predidit/KazumiRules/tree/dev/rules",
			want: []string{"https://raw.githubusercontent.com/Predidit/KazumiRules/dev/rules/index.json"},
			repo: true,
		},
		{
			in:   "https://github.com/Predidit/KazumiRules/blob/main/AGE.json",
			want: []string{"https://raw.githubusercontent.com/Predidit/KazumiRules/main/AGE.json"},
			repo: true,
		},
		{
			in:   "https://mirror.example.com/kazumi/index.json",
			want: []string{"https://mirror.example.com/kazumi/index.json"},
			repo: true,
		},
		{
			in:   "https://mirror.example.com/kazumi/AGE.json",
			want: []string{"https://mirror.example.com/kazumi/AGE.json"},
			repo: false,
		},
	}
	for _, tc := range cases {
		if got := LooksLikeRepoURL(tc.in); got != tc.repo {
			t.Errorf("LooksLikeRepoURL(%q) = %v, want %v", tc.in, got, tc.repo)
		}
		got, err := NormalizeSourceURL(tc.in)
		if err != nil {
			t.Fatalf("NormalizeSourceURL(%q) error: %v", tc.in, err)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("NormalizeSourceURL(%q) = %v, want %v", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("NormalizeSourceURL(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

func TestNormalizeEpisodeURL(t *testing.T) {
	if got := NormalizeEpisodeURL("https://www.agedm.io/", "/play/123/1/1/"); got != "https://www.agedm.io/play/123/1/1" {
		t.Fatalf("relative url got %q", got)
	}
	if got := NormalizeEpisodeURL("https://www.agedm.io/", "http://www.agedm.io/play/123/"); got != "https://www.agedm.io/play/123" {
		t.Fatalf("same-host scheme unify got %q", got)
	}
	if got := NormalizeEpisodeURL("https://www.agedm.io/", "http://other.example.com/p/1"); got != "http://other.example.com/p/1" {
		t.Fatalf("cross-host scheme should remain http, got %q", got)
	}
	baidu := "https://gimg0.baidu.com/gimg/app=2001&n=0&g=0n&fmt=jpeg&src=https://cdn.aqdstatic.com:966/age/covers/20260200.jpg"
	want := "https://cdn.aqdstatic.com:966/age/covers/20260200.jpg"
	if got := cleanPosterURL(baidu); got != want {
		t.Fatalf("cleanPosterURL got %q, want %q", got, want)
	}
	baiduEncoded := "https://gimg4.baidu.com/gimg/app=2001&n=0&g=0n&fmt=jpeg&src=https%3A%2F%2Fcdn.aqdstatic.com%3A966%2Fage%2Fcovers%2F20260200.jpg"
	if got := cleanPosterURL(baiduEncoded); got != want {
		t.Fatalf("cleanPosterURL encoded got %q, want %q", got, want)
	}
}

func TestExtractSearchPicFallbacks(t *testing.T) {
	doc, err := htmlquery.Parse(strings.NewReader(`<div class="item"><div class="cover" style="background-image: linear-gradient(180deg, #000, #111), url(https://img.example.com/bg.jpg);"></div></div>`))
	if err != nil {
		t.Fatal(err)
	}
	node, err := htmlquery.Query(doc, "//div[@class='item']")
	if err != nil {
		t.Fatal(err)
	}
	if got := extractSearchPic(node, "https://example.com/"); got != "https://img.example.com/bg.jpg" {
		t.Fatalf("style background-image extract got %q", got)
	}
	doc2, err := htmlquery.Parse(strings.NewReader(`<li class="card"><a class="lazyload" data-original="/covers/a.webp"></a></li>`))
	if err != nil {
		t.Fatal(err)
	}
	node2, err := htmlquery.Query(doc2, "//li[@class='card']")
	if err != nil {
		t.Fatal(err)
	}
	if got := extractSearchPic(node2, "https://example.com/"); got != "https://example.com/covers/a.webp" {
		t.Fatalf("anchor data-original extract got %q", got)
	}
}

func TestNormalizeRelativeXPath(t *testing.T) {
	cases := map[string]string{
		"//ul/li/a":    ".//ul/li/a",
		"//div[2]/text()": ".//div[2]/text()",
		".":            ".",
		".//a[contains(@href, '/play/')]": ".//a[contains(@href, '/play/')]",
		"/self::*[@class='a']/following-sibling::a[@class='b'] | /self::*[@class='c']/following-sibling::a[@class='d']": "./self::*[@class='a']/following-sibling::a[@class='b'] | ./self::*[@class='c']/following-sibling::a[@class='d']",
	}
	for in, want := range cases {
		if got := NormalizeRelativeXPath(in); got != want {
			t.Errorf("NormalizeRelativeXPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEngineXPathSearchAndChapters(t *testing.T) {
	var postReceived string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			if r.Method == http.MethodPost {
				_ = r.ParseForm()
				postReceived = r.Form.Get("wd")
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><html><body>
				<div class="results">
					<div class="card"><img data-original="/cover/1.jpg"><h5><a href="/detail/101">孤独摇滚</a></h5></div>
					<div class="card"><h5><a href="/detail/102">葬送的芙莉莲</a></h5></div>
				</div>
			</body></html>`))
		case "/detail/101":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><html><head><title>孤独摇滚 - 动漫</title></head><body>
				<div class="detail_imform_kv"><span class="detail_imform_tag">地区：</span><span class="detail_imform_value">日本</span></div>
				<div class="detail_imform_kv"><span class="detail_imform_tag">首播时间：</span><span class="detail_imform_value">2022-10-08</span></div>
				<div class="detail_imform_desc_pre"><p>简介：后藤一里是一名热爱吉他的孤独少女，在加入结束乐队后逐渐成长。[收起]</p></div>
				<section class="anthology">
					<div class="line_button line_button1">线路A</div>
					<a class="circuit_switch1" href="/play/101-1">第 1 集</a>
					<a class="circuit_switch1" href="/play/101-2">第 2 集</a>
					<div class="line_button line_button2">线路B</div>
					<a class="circuit_switch2" href="/play/101-b1">HD 01</a>
				</section>
			</body></html>`))
		case "/captcha":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<html><body><form action="/captcha/verify"><img src="/cap.png"></form></body></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	engine := &Engine{Client: srv.Client()}
	rule := Rule{
		Name:          "TestXPath",
		BaseURL:       srv.URL,
		SearchURL:     srv.URL + "/search?wd=@keyword",
		UsePost:       true,
		SearchList:    "//div[@class='results']/div[@class='card']",
		SearchName:    "//h5/a",
		SearchResult:  "//h5/a",
		ChapterRoads:  "//section[@class='anthology'][1]/div[contains(@class,'line_button')]",
		ChapterResult: "/self::*[contains(@class,'line_button1')]/following-sibling::a[@class='circuit_switch1'] | /self::*[contains(@class,'line_button2')]/following-sibling::a[@class='circuit_switch2']",
	}

	items, err := engine.Search(context.Background(), rule, "孤独")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if postReceived != "孤独" {
		t.Fatalf("expected POST form wd=孤独, got %q", postReceived)
	}
	if len(items) != 2 || items[0].Name != "孤独摇滚" || items[0].Src != "/detail/101" || items[0].Pic != srv.URL+"/cover/1.jpg" {
		t.Fatalf("unexpected search items: %+v", items)
	}

	roads, pageMeta, err := engine.Chapters(context.Background(), rule, items[0].Src)
	if err != nil {
		t.Fatalf("Chapters failed: %v", err)
	}
	if pageMeta.Title != "孤独摇滚 - 动漫" {
		t.Fatalf("unexpected page title: %q", pageMeta.Title)
	}
	if pageMeta.Content != "后藤一里是一名热爱吉他的孤独少女，在加入结束乐队后逐渐成长。" || pageMeta.Area != "日本" || pageMeta.Year != "2022" {
		t.Fatalf("unexpected page meta: %+v", pageMeta)
	}
	if len(roads) != 2 || len(roads[0].Episodes) != 2 || roads[0].Episodes[0].Name != "第1集" || roads[0].Episodes[0].URL != srv.URL+"/play/101-1" {
		t.Fatalf("unexpected roads: %+v", roads)
	}
	if len(roads[1].Episodes) != 1 || roads[1].Episodes[0].URL != srv.URL+"/play/101-b1" {
		t.Fatalf("unexpected second road: %+v", roads[1])
	}

	captchaRule := rule
	captchaRule.UsePost = false
	captchaRule.SearchURL = srv.URL + "/captcha?q=@keyword"
	captchaRule.AntiCrawlerConfig = &AntiCrawlerConfig{
		Enabled:            true,
		CaptchaDetectType:  1,
		CaptchaDetectValue: "//form[contains(@action,'/captcha/verify')]",
	}
	if _, err := engine.Search(context.Background(), captchaRule, "test"); !errors.Is(err, ErrCaptchaRequired) {
		t.Fatalf("expected ErrCaptchaRequired, got %v", err)
	}
}

func TestEngineAPISearchAndChaptersDelimitedAndNested(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/search":
			if r.URL.Query().Get("vod_name") != "进击" {
				t.Errorf("unexpected query: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"info":{"rows":[{"vod_id":42,"vod_name":"进击的巨人","vod_pic":"/pic/42.jpg"}]}}`))
		case "/api/detail":
			_, _ = w.Write([]byte(`{"info":{"vod_name":"进击的巨人","vod_content":"<p>人类与巨人的漫长抗争史。</p>","vod_year":"2013","vod_area":"日本","vod_play_from":"线路甲$$$线路乙","vod_play_url":"第01集$ep-a1#第02集$ep-a2$$$第01集$ep-b1"}}`))
		case "/api/nested/42":
			_, _ = w.Write([]byte(`{"data":{"episodes":[{"episodeLabel":"EP 1","episodeOrder":1},{"episodeLabel":"EP 2","episodeOrder":2}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	engine := &Engine{Client: srv.Client()}
	delimRule := Rule{
		Name:        "giriMock",
		BaseURL:     srv.URL,
		SearchMode:  "api",
		ChapterMode: "api",
		SearchAPIConfig: &APISearchConfig{
			Request: APIRequestConfig{
				Method: "GET",
				URL:    srv.URL + "/api/search",
				Query:  map[string]any{"vod_name": "@keyword", "limit": 24},
			},
			ListPath:   "$.info.rows[*]",
			NamePath:   "$.vod_name",
			SourcePath: "$.vod_id",
		},
		ChapterAPIConfig: &APIChapterConfig{
			Request: APIRequestConfig{
				Method: "GET",
				URL:    srv.URL + "/api/detail",
				Query:  map[string]any{"vod_id": "@source"},
			},
			Format:           "delimited",
			RoadNamesPath:    "$.info.vod_play_from",
			RoadEpisodesPath: "$.info.vod_play_url",
			RoadSeparator:    "$$$",
			EpisodeSeparator: "#",
			FieldSeparator:   "$",
			EpisodePage: &APIEpisodePageConfig{
				URL:   srv.URL + "/player",
				Query: map[string]any{"key": "0", "url": "@episodeUrl"},
			},
		},
	}

	items, err := engine.Search(context.Background(), delimRule, "进击")
	if err != nil {
		t.Fatalf("API search failed: %v", err)
	}
	if len(items) != 1 || items[0].Name != "进击的巨人" || items[0].Src != "42" || items[0].Pic != srv.URL+"/pic/42.jpg" {
		t.Fatalf("unexpected api items: %+v", items)
	}
	roads, apiMeta, err := engine.Chapters(context.Background(), delimRule, items[0].Src)
	if err != nil {
		t.Fatalf("delimited chapters failed: %v", err)
	}
	if apiMeta.Content != "人类与巨人的漫长抗争史。" || apiMeta.Year != "2013" || apiMeta.Area != "日本" {
		t.Fatalf("unexpected api chapter meta: %+v", apiMeta)
	}
	if len(roads) != 2 || roads[0].Name != "线路甲" || len(roads[0].Episodes) != 2 {
		t.Fatalf("unexpected delimited roads: %+v", roads)
	}
	if !strings.Contains(roads[0].Episodes[0].URL, "/player?key=0&url=ep-a1") {
		t.Fatalf("unexpected episodePage url: %q", roads[0].Episodes[0].URL)
	}

	nestedRule := delimRule
	nestedRule.ChapterAPIConfig = &APIChapterConfig{
		Request: APIRequestConfig{
			Method: "GET",
			URL:    srv.URL + "/api/nested/@source",
		},
		Format:          "nested",
		RoadsPath:       "$.data",
		EpisodesPath:    "$.episodes[*]",
		EpisodeNamePath: "$.episodeLabel",
		EpisodeUrlPath:  "$.episodeOrder",
		EpisodePage: &APIEpisodePageConfig{
			URL: srv.URL + "/anime/@source/episode/@episodeUrl",
		},
	}
	nestedRoads, _, err := engine.Chapters(context.Background(), nestedRule, "42")
	if err != nil {
		t.Fatalf("nested chapters failed: %v", err)
	}
	if len(nestedRoads) != 1 || len(nestedRoads[0].Episodes) != 2 || nestedRoads[0].Episodes[1].URL != srv.URL+"/anime/42/episode/2" {
		t.Fatalf("unexpected nested roads: %+v", nestedRoads)
	}
}

func TestFetchBundleRepoAndSingleRule(t *testing.T) {
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/index.json":
			_, _ = w.Write([]byte(`[
				{"name":"AGE","version":"1.6","useNativePlayer":true},
				{"name":"Broken","version":"1.0","useNativePlayer":true}
			]`))
		case "/AGE.json":
			_ = json.NewEncoder(w).Encode(Rule{
				API:           "5",
				Type:          "anime",
				Name:          "AGE",
				Version:       "1.6",
				BaseURL:       srvURL,
				SearchURL:     srvURL + "/search?q=@keyword",
				SearchList:    "//div",
				SearchName:    "//a",
				SearchResult:  "//a",
				ChapterRoads:  "//ul",
				ChapterResult: "//li/a",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	bundle, ok, err := FetchBundle(context.Background(), srv.Client(), srv.URL+"/index.json")
	if err != nil || !ok || bundle == nil {
		t.Fatalf("FetchBundle index failed: bundle=%+v ok=%v err=%v", bundle, ok, err)
	}
	if len(bundle.Rules) != 1 || bundle.Rules[0].Name != "AGE" || len(bundle.Warnings) != 1 {
		t.Fatalf("unexpected bundle: rules=%d warnings=%v", len(bundle.Rules), bundle.Warnings)
	}

	single, ok, err := FetchBundle(context.Background(), srv.Client(), srv.URL+"/AGE.json")
	if err != nil || !ok || single == nil || len(single.Rules) != 1 {
		t.Fatalf("FetchBundle single failed: single=%+v ok=%v err=%v", single, ok, err)
	}
}

func TestBangumiCatalogTrendingCalendarAndBrowse(t *testing.T) {
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits[r.URL.Path]++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/p1/trending/subjects":
			_, _ = w.Write([]byte(`{"total":48,"data":[{"count":5600,"subject":{"id":1001,"name":"Frieren","nameCN":"葬送的芙莉莲","info":"28话 / 2026年1月","metaTags":["TV","日本","奇幻","冒险"],"rating":{"score":8.8,"rank":20},"platform":{"typeCN":"TV"},"images":{"common":"https://lain.bgm.tv/pic/1001.jpg"}}}]}`))
		case "/p1/calendar":
			_, _ = w.Write([]byte(`{"1":[{"watchers":10,"subject":{"id":1,"nameCN":"冷门番","platform":{"typeCN":"WEB"},"images":{"common":"https://lain.bgm.tv/pic/1.jpg"}}},{"watchers":900,"subject":{"id":2,"nameCN":"热门周一新番","rating":{"score":8.1},"platform":{"typeCN":"TV"},"images":{"common":"https://lain.bgm.tv/pic/2.jpg"}}}]}`))
		case "/v0/search/subjects":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["sort"] != "heat" {
				t.Errorf("expected sort=heat, got %v", body["sort"])
			}
			_, _ = w.Write([]byte(`{"total":2,"data":[{"id":2001,"name":"Test","name_cn":"分类测试番","date":"2026-04-01","platform":"TV","rating":{"score":8.3,"rank":100},"images":{"common":"https://lain.bgm.tv/pic/2001.jpg"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	bgm := &BangumiClient{
		HTTP:        srv.Client(),
		NextBaseURL: srv.URL,
		APIBaseURL:  srv.URL,
		Now:         func() time.Time { return time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC) }, // Monday
	}

	home := bgm.HomePayload()
	classes, _ := home["class"].([]map[string]any)
	if len(classes) != 6 || classes[0]["type_id"] != "trending" || classes[1]["type_id"] != "calendar" {
		t.Fatalf("unexpected classes: %+v", classes)
	}

	trend, err := bgm.CategoryPage(context.Background(), "trending", 1, nil, "msearch:bgm:")
	if err != nil {
		t.Fatalf("trending failed: %v", err)
	}
	list, _ := trend["list"].([]any)
	if len(list) != 1 {
		t.Fatalf("unexpected trending list: %+v", trend)
	}
	first := list[0].(map[string]any)
	if first["vod_id"] != "msearch:bgm:1001" || first["vod_name"] != "葬送的芙莉莲" || first["vod_remarks"] != "★ 8.8" {
		t.Fatalf("unexpected trending card: %+v", first)
	}
	// Second identical request should hit memory cache.
	if _, err := bgm.CategoryPage(context.Background(), "trending", 1, nil, "msearch:bgm:"); err != nil {
		t.Fatal(err)
	}
	if hits["/p1/trending/subjects"] != 1 {
		t.Fatalf("expected trending cache hit, got %d requests", hits["/p1/trending/subjects"])
	}

	cal, err := bgm.CategoryPage(context.Background(), "calendar", 1, map[string]string{"day": "1"}, "ssearch:bgm:")
	if err != nil {
		t.Fatalf("calendar failed: %v", err)
	}
	calList := cal["list"].([]any)
	if len(calList) != 2 || calList[0].(map[string]any)["vod_name"] != "热门周一新番" || calList[0].(map[string]any)["vod_id"] != "ssearch:bgm:2" {
		t.Fatalf("expected calendar sorted by watchers desc with ssearch prefix, got %+v", calList)
	}

	browse, err := bgm.CategoryPage(context.Background(), "browse", 1, map[string]string{"tag": "奇幻", "year": "2026", "sort": "heat"}, "msearch:bgm:")
	if err != nil {
		t.Fatalf("browse failed: %v", err)
	}
	bList := browse["list"].([]any)
	if len(bList) != 1 || bList[0].(map[string]any)["vod_name"] != "分类测试番" {
		t.Fatalf("unexpected browse result: %+v", browse)
	}
}

func TestServerDetailEnrichesDescriptionFromBangumi(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<html><body><div class="item"><a href="/detail/1001">葬送的芙莉莲 [完结]</a></div></body></html>`))
		case "/detail/1001":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<html><body><ul class="eps"><li><a href="/play/1001-1">第1集</a></li></ul></body></html>`))
		case "/v0/search/subjects":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"total":1,"data":[{"id":1001,"name":"Sousou no Frieren","name_cn":"葬送的芙莉莲","summary":"打倒魔王后的勇者一行人中，魔法使芙莉莲踏上了理解人类的旅途。","date":"2023-09-29","platform":"TV","meta_tags":["TV","日本","奇幻","冒险"],"rating":{"score":8.8,"rank":20},"images":{"common":"https://lain.bgm.tv/pic/1001.jpg"},"infobox":[{"key":"导演","value":"斋藤圭一郎"},{"key":"动画制作","value":"MADHOUSE"},{"key":"声优","value":[{"v":"种崎敦美"},{"v":"市之濑加那"}]}]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	bundle := &Bundle{
		SourceURL: srv.URL + "/index.json",
		Rules: []Rule{{
			API:           "5",
			Type:          "anime",
			Name:          "AGE",
			BaseURL:       srv.URL,
			SearchURL:     srv.URL + "/search?q=@keyword",
			SearchList:    "//div[@class='item']",
			SearchName:    "//a",
			SearchResult:  "//a",
			ChapterRoads:  "//ul[@class='eps']",
			ChapterResult: "//li/a",
		}},
	}
	bgm := &BangumiClient{
		HTTP:        srv.Client(),
		NextBaseURL: srv.URL,
		APIBaseURL:  srv.URL,
	}
	ks := NewServer(bundle, Options{HTTP: srv.Client(), Bangumi: bgm})
	if err := ks.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer ks.Stop()

	searchOut, code, err := ks.dispatch(context.Background(), 0, bundle.Rules[0], "search", map[string]any{"wd": "芙莉莲"})
	if err != nil || code != http.StatusOK {
		t.Fatalf("dispatch search failed: code=%d err=%v", code, err)
	}
	_ = searchOut

	detailOut, code, err := ks.dispatch(context.Background(), 0, bundle.Rules[0], "detail", map[string]any{"id": "/detail/1001"})
	if err != nil || code != http.StatusOK {
		t.Fatalf("dispatch detail failed: code=%d err=%v", code, err)
	}
	detailMap, _ := detailOut.(map[string]any)
	list, _ := detailMap["list"].([]any)
	if len(list) != 1 {
		t.Fatalf("unexpected detail list: %+v", detailOut)
	}
	vod, _ := list[0].(map[string]any)
	if vod["vod_content"] != "打倒魔王后的勇者一行人中，魔法使芙莉莲踏上了理解人类的旅途。" {
		t.Fatalf("expected Bangumi summary in vod_content, got %q", vod["vod_content"])
	}
	if vod["vod_year"] != "2023" || vod["vod_area"] != "日本" || vod["vod_director"] != "斋藤圭一郎 / MADHOUSE" || vod["vod_actor"] != "种崎敦美 / 市之濑加那" {
		t.Fatalf("unexpected enriched detail metadata: %+v", vod)
	}
	if vod["vod_pic"] != "https://lain.bgm.tv/pic/1001.jpg" || !strings.Contains(vod["vod_remarks"].(string), "★ 8.8") {
		t.Fatalf("unexpected enriched pic/remarks: %+v", vod)
	}
}

func TestBangumiClientOptionalBearerToken(t *testing.T) {
	var gotSearchAuth, gotSubjectAuth string
	var gotFilter map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v0/search/subjects":
			gotSearchAuth = r.Header.Get("Authorization")
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotFilter, _ = body["filter"].(map[string]any)
			_, _ = w.Write([]byte(`{"total":1,"data":[{"id":99,"name_cn":"测试受限番","summary":"带 Token 的简介。","infobox":[{"key":"导演","value":"测试监督"}]}]}`))
		case "/v0/subjects/99":
			gotSubjectAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"id":99,"name_cn":"测试受限番","summary":"带 Token 的简介。"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	token := ""
	bgm := &BangumiClient{
		HTTP:       srv.Client(),
		APIBaseURL: srv.URL,
		Token:      func() string { return token },
	}
	if _, err := bgm.LookupSubject(context.Background(), "公开测试番"); err != nil {
		t.Fatalf("anon LookupSubject: %v", err)
	}
	if gotSearchAuth != "" {
		t.Fatalf("expected no Authorization without token, got %q", gotSearchAuth)
	}
	if nsfw, ok := gotFilter["nsfw"]; !ok || nsfw != false {
		t.Fatalf("expected filter.nsfw=false without token, got %+v", gotFilter)
	}

	token = "sample-token-value"
	item, err := bgm.LookupSubject(context.Background(), "另一部受限番")
	if err != nil {
		t.Fatalf("auth LookupSubject: %v", err)
	}
	if gotSearchAuth != "Bearer sample-token-value" {
		t.Fatalf("expected Bearer auth header on search, got %q", gotSearchAuth)
	}
	if _, hasNSFW := gotFilter["nsfw"]; hasNSFW {
		t.Fatalf("expected filter.nsfw omitted with token, got %+v", gotFilter)
	}
	if item == nil || item.Summary != "带 Token 的简介。" {
		t.Fatalf("unexpected item: %+v", item)
	}
	if _, err := bgm.SubjectByID(context.Background(), 99); err != nil {
		t.Fatalf("SubjectByID: %v", err)
	}
	if gotSubjectAuth != "Bearer sample-token-value" {
		t.Fatalf("expected Bearer auth header on subject fetch, got %q", gotSubjectAuth)
	}
}
