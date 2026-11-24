package tvbox

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/freelxs/jstv-player/internal/catvod"
)

func TestPythonHostFullFlowAndCompatShims(t *testing.T) {
	pyBin := FindPython("")
	if pyBin == "" {
		t.Skip("python3 not available in test environment")
	}
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/spider.py":
			fmt.Fprint(w, testPythonSpider)
		case "/page":
			fmt.Fprint(w, `<html><body><div class="list"><a class="item" href="/v/42">测试影片</a></div></body></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	raw := []byte(`{"sites":[{"key":"py1","name":"测试Python","type":3,"api":"` + upstream.URL + `/spider.py","ext":"` + upstream.URL + `/page"}]}`)
	cfg, err := Parse(raw, upstream.URL+"/sub.json")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sites[0].Kind != "python" || cfg.Sites[0].Unsupported {
		t.Fatalf("expected supported python site, got %+v", cfg.Sites[0])
	}

	srv := NewServer(cfg, Options{Dir: t.TempDir(), HTTP: upstream.Client(), Python: pyBin})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	client := &catvod.Client{Base: srv.Base(), HTTP: &http.Client{Timeout: 15 * time.Second}}
	siteAPI := srv.Sites()[0].API

	home, err := client.Home(siteAPI)
	if err != nil {
		t.Fatalf("Home error: %v", err)
	}
	cls, _ := home["class"].([]any)
	if len(cls) != 1 || asMap(cls[0])["type_name"] != "电影" {
		t.Fatalf("unexpected home: %+v", home)
	}

	cat, err := client.Category(siteAPI, "1", 1, nil)
	if err != nil {
		t.Fatalf("Category error: %v", err)
	}
	list, _ := cat["list"].([]any)
	if len(list) != 1 {
		t.Fatalf("unexpected category list: %+v", cat)
	}
	item := asMap(list[0])
	if item["vod_name"] != "测试影片" || item["vod_id"] != "/v/42" || item["vod_remarks"] != "aes-ok|xpath-ok|pq-ok|cache-ok" {
		t.Fatalf("unexpected item: %+v", item)
	}

	det, err := client.Detail(siteAPI, "/v/42")
	if err != nil {
		t.Fatalf("Detail error: %v", err)
	}
	dlist, _ := det["list"].([]any)
	if len(dlist) != 1 || asMap(dlist[0])["vod_name"] != "测试影片" {
		t.Fatalf("unexpected detail: %+v", det)
	}

	srch, err := client.Search(siteAPI, "流浪地球", 1)
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	slist, _ := srch["list"].([]any)
	if len(slist) != 1 || asMap(slist[0])["vod_name"] != "搜索:流浪地球" {
		t.Fatalf("unexpected search: %+v", srch)
	}

	play, err := client.Play(siteAPI, "默认", "42")
	if err != nil {
		t.Fatalf("Play error: %v", err)
	}
	playURL := scalar(play["url"])
	if !strings.Contains(playURL, "/tvbox/pyproxy/") {
		t.Fatalf("expected localProxy URL, got %q", playURL)
	}
	resp, err := http.Get(playURL)
	if err != nil {
		t.Fatalf("GET localProxy failed: %v", err)
	}
	defer resp.Body.Close()
	proxied, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(proxied) != "#EXTM3U\nproxied-42" {
		t.Fatalf("unexpected localProxy response: %d %q", resp.StatusCode, string(proxied))
	}
}

func TestPythonUnwrapGitHubProxy(t *testing.T) {
	in := "https://gh-proxy.org/https://raw.githubusercontent.com/user/repo/main/a.py"
	want := "https://raw.githubusercontent.com/user/repo/main/a.py"
	if got := unwrapGitHubProxy(in); got != want {
		t.Fatalf("unwrapGitHubProxy = %q, want %q", got, want)
	}
	if got := unwrapGitHubProxy("https://example.com/a.py"); got != "" {
		t.Fatalf("expected empty for non-proxy URL, got %q", got)
	}
}

func TestLivePythonSampleFromTest1(t *testing.T) {
	if os.Getenv("JSTV_LIVE_PY") != "1" {
		t.Skip("set JSTV_LIVE_PY=1 to run live python spider probe")
	}
	raw, final, err := Fetch(&http.Client{Timeout: 20 * time.Second}, "https://raw.githubusercontent.com/wliqi495-create/jaychou123/refs/heads/main/yingshi/test1.json", 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(raw, final)
	if err != nil {
		t.Fatal(err)
	}
	pyCount := 0
	openable := 0
	for _, s := range cfg.Sites {
		if !s.Unsupported {
			openable++
		}
		if s.Kind == "python" && !s.Unsupported {
			pyCount++
		}
	}
	t.Logf("test1.json parsed: total=%d openable=%d python_openable=%d", len(cfg.Sites), openable, pyCount)

	srv := NewServer(cfg, Options{
		Dir:    t.TempDir(),
		HTTP:   &http.Client{Timeout: 12 * time.Second},
		Python: FindPython(""),
		Log:    func(msg string) { t.Log("HOST:", msg) },
	})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	tested := 0
	worked := 0
	seenAPI := map[string]bool{}
	for _, s := range cfg.Sites {
		if s.Kind != "python" || s.Unsupported || seenAPI[s.API] {
			continue
		}
		seenAPI[s.API] = true
		tested++
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		home, _, herr := srv.dispatch(ctx, s, "home", map[string]any{"filter": true})
		hmap := asMap(home)
		cls, _ := hmap["class"].([]any)
		hlist, _ := hmap["list"].([]any)
		catInfo := "no-cls"
		hasData := len(hlist) > 0
		if len(cls) > 0 {
			c0 := asMap(cls[0])
			tid := scalar(c0["type_id"])
			cres, _, cerr := srv.dispatch(ctx, s, "category", map[string]any{"id": tid, "page": 1})
			if cerr != nil {
				catInfo = fmt.Sprintf("tid=%s ERR=%v", tid, cerr)
			} else {
				clist, _ := asMap(cres)["list"].([]any)
				first := ""
				if len(clist) > 0 {
					first = scalar(asMap(clist[0])["vod_name"])
					hasData = true
				}
				catInfo = fmt.Sprintf("tid=%s(%s) items=%d first=%q", tid, scalar(c0["type_name"]), len(clist), first)
			}
		}
		if len(cls) > 0 || hasData {
			worked++
		}
		t.Logf("[%02d] %-16s %-16s cls=%d hlist=%d | %s | herr=%v", tested, s.Key, s.Name, len(cls), len(hlist), catInfo, herr)
		cancel()
		if tested >= 15 {
			break
		}
	}
	t.Logf("SUMMARY: tested=%d worked=%d", tested, worked)
}

const testPythonSpider = `
import base64
import requests
from requests.adapters import HTTPAdapter
from urllib3.util.retry import Retry
from bs4 import BeautifulSoup
from lxml import etree
from pyquery import PyQuery as pq
from Crypto.Cipher import AES
from Crypto.Util.Padding import pad, unpad
from base.spider import Spider

class Spider(Spider):
    def init(self, extend=""):
        self.page_url = extend

    def getName(self):
        return "TestPy"

    def homeContent(self, filter):
        return {"class": [{"type_id": "1", "type_name": "电影"}]}

    def homeVideoContent(self):
        return {"list": []}

    def categoryContent(self, tid, pg, filter, extend):
        resp = self.fetch(self.page_url)
        soup = BeautifulSoup(resp.text, "html.parser")
        a = soup.select_one(".list a.item")
        title = a.get_text(strip=True)
        href = a.get("href")

        doc = etree.HTML(resp.text)
        xtitle = "".join(doc.xpath('//div[@class="list"]/a/@href'))
        xpath_tag = "xpath-ok" if xtitle == "/v/42" else "xpath-bad"

        q = pq(resp.text)
        pq_tag = "pq-ok" if q(".list a.item").text() == "测试影片" else "pq-bad"

        key = b"0123456789abcdef"
        iv = b"abcdef0123456789"
        enc = AES.new(key, AES.MODE_CBC, iv).encrypt(pad(b"hello-tvbox", 16))
        dec = unpad(AES.new(key, AES.MODE_CBC, iv).decrypt(enc), 16)
        aes_tag = "aes-ok" if dec == b"hello-tvbox" else "aes-bad"

        self.setCache("k1", "v1")
        cache_tag = "cache-ok" if self.getCache("k1") == "v1" else "cache-bad"

        return {
            "page": int(pg),
            "pagecount": 1,
            "list": [{
                "vod_id": href,
                "vod_name": title,
                "vod_pic": "",
                "vod_remarks": f"{aes_tag}|{xpath_tag}|{pq_tag}|{cache_tag}",
            }]
        }

    def detailContent(self, ids):
        return {"list": [{"vod_id": ids[0], "vod_name": "测试影片", "vod_play_from": "默认", "vod_play_url": "正片$42"}]}

    def searchContent(self, key, quick, pg="1"):
        return {"list": [{"vod_id": "s1", "vod_name": f"搜索:{key}"}]}

    def playerContent(self, flag, id, vipFlags):
        return {"parse": 0, "url": self.getProxyUrl() + f"&vid={id}"}

    def localProxy(self, param):
        vid = param.get("vid", "")
        return [200, "application/vnd.apple.mpegurl", f"#EXTM3U\nproxied-{vid}".encode("utf-8")]
`
