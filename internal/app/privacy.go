package app

import (
	"context"

	"github.com/egoist/mygo"
)

const contentRulesID = "jstv-privacy-v1"

// defaultContentBlockerRulesJSON is a WebKit Content Blocker rule list compiled
// via mygo.CompileContentRules for hidden sniffing pages and third-party web
// windows. Every regex follows WebKit's ASCII subset (no '|', '{n}', '\d', etc.).
const defaultContentBlockerRulesJSON = `[
  {"trigger":{"url-filter":"^https?://[^/]*google-analytics\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*analytics\\.google\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*googletagmanager\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*googletagservices\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*doubleclick\\.net/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*googlesyndication\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*googleadservices\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*adservice\\.google\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*imasdk\\.googleapis\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*hm\\.baidu\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*pos\\.baidu\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*cpro\\.baidustatic\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*dup\\.baidustatic\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*cnzz\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*51\\.la/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*51yes\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*umeng\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*clarity\\.ms/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*cloudflareinsights\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*mc\\.yandex\\.ru/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*mc\\.yandex\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*histats\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*adsco\\.re/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*popads\\.net/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*popcash\\.net/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*propellerads\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*exoclick\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*juicyads\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*trafficjunky\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*taboola\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*outbrain\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*mgid\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*scorecardresearch\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*quantserve\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*hotjar\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*ads-twitter\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*connect\\.facebook\\.net/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*openfpcdn\\.io/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*yadro\\.ru/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*webvisor\\.org/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*adx1\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*profitableratecpm\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*protrafficinspector\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":"^https?://[^/]*usrpubtrk\\.com/"},"action":{"type":"block"}},
  {"trigger":{"url-filter":".*","load-type":["third-party"],"resource-type":["ping"]},"action":{"type":"block"}},
  {"trigger":{"url-filter":".*","load-type":["third-party"],"resource-type":["popup"]},"action":{"type":"block"}},
  {"trigger":{"url-filter":".*","unless-domain":["localhost","127.0.0.1"]},"action":{"type":"css-display-none","selector":".ad-banner, .ad-overlay, .popup-ad, #ad-overlay, ins.adsbygoogle, iframe[src*='doubleclick'], iframe[src*='googlesyndication']"}}
]`

type contentRulesContextKey struct{}

func withContentRules(ctx context.Context, rules []*mygo.ContentRules) context.Context {
	if len(rules) == 0 {
		return ctx
	}
	return context.WithValue(ctx, contentRulesContextKey{}, rules)
}

func contentRulesFromContext(ctx context.Context) []*mygo.ContentRules {
	if ctx == nil {
		return nil
	}
	rules, _ := ctx.Value(contentRulesContextKey{}).([]*mygo.ContentRules)
	return rules
}

func (p *Player) initPrivacyRules() {
	if p.webMode.Load() || p.desktop.CompileContentRules == nil {
		return
	}
	compiled, err := p.desktop.CompileContentRules(contentRulesID, defaultContentBlockerRulesJSON)
	if err != nil {
		p.log("隐私拦截规则编译失败: " + err.Error())
		return
	}
	p.contentRulesMu.Lock()
	p.contentRules = compiled
	p.contentRulesMu.Unlock()
}

func (p *Player) activeContentRules() []*mygo.ContentRules {
	if p.webMode.Load() || p.GetSettings().DisableAdBlock {
		return nil
	}
	p.contentRulesMu.Lock()
	rules := p.contentRules
	p.contentRulesMu.Unlock()
	if rules == nil {
		return nil
	}
	return []*mygo.ContentRules{rules}
}
