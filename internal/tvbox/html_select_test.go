package tvbox

import (
	"encoding/json"
	"testing"
)

func TestHTMLSelectJQueryPseudosAndOuterHTML(t *testing.T) {
	doc := `
<ul class="navbar-items">
  <li><a href="/home">首页</a></li>
  <li><a href="/hot">热门</a></li>
  <li><a href="/movie">电影</a></li>
  <li><a href="/tv">电视剧</a></li>
  <li><a href="/anime">动漫</a></li>
  <li><a href="/more">更多</a></li>
</ul>
<div class="cards">
  <ul>
    <li><a href="/v/1" title="片一"><span>HD</span>片一正文</a><img data-original="/img/1.jpg" src="/lazy.gif"></li>
    <li><a href="/v/2" title="片二"><span>4K</span>片二正文</a><img src="/img/2.jpg"></li>
  </ul>
</div>`

	raw := pdfa(doc, ".navbar-items li:gt(1):lt(3)")
	var items []string
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		t.Fatalf("unmarshal pdfa: %v (%s)", err, raw)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items for :gt(1):lt(3), got %d: %v", len(items), items)
	}
	if got := pdfh(items[0], "a&&Text"); got != "电影" {
		t.Fatalf("expected 电影, got %q (item=%s)", got, items[0])
	}
	if got := pdWithBase(items[0], "a&&href", "https://example.com"); got != "https://example.com/movie" {
		t.Fatalf("expected resolved href, got %q", got)
	}

	rawCards := pdfa(doc, ".cards&&ul&&li")
	var cards []string
	if err := json.Unmarshal([]byte(rawCards), &cards); err != nil || len(cards) != 2 {
		t.Fatalf("expected 2 cards from ul&&li, got %d (%s)", len(cards), rawCards)
	}
	if got := pdfh(cards[0], "a--span&&Text"); got != "片一正文" {
		t.Fatalf("expected exclusion a--span&&Text = 片一正文, got %q", got)
	}
	if got := pdfh(cards[0], "img&&data-original||src"); got != "/img/1.jpg" {
		t.Fatalf("expected data-original, got %q", got)
	}
	if got := pdfh(cards[1], "img&&data-original||src"); got != "/img/2.jpg" {
		t.Fatalf("expected fallback src, got %q", got)
	}
	if got := pdfh(doc, ".navbar-items li:eq(-1)&&a&&Text"); got != "更多" {
		t.Fatalf("expected negative :eq(-1) = 更多, got %q", got)
	}
}
