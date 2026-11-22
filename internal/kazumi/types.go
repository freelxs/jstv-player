package kazumi

import (
	"encoding/json"
	"strconv"
	"strings"
)

// FlexString unmarshals a JSON string or number into a trimmed string.
type FlexString string

func (f *FlexString) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*f = ""
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*f = FlexString(s)
		return nil
	}
	var num json.Number
	if err := json.Unmarshal(data, &num); err == nil {
		*f = FlexString(num.String())
		return nil
	}
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		*f = FlexString(strconv.FormatBool(b))
		return nil
	}
	*f = ""
	return nil
}

// IndexItem is one entry inside a Kazumi rules repository index.json.
type IndexItem struct {
	Name               string     `json:"name"`
	Version            FlexString `json:"version"`
	UseNativePlayer    *bool      `json:"useNativePlayer,omitempty"`
	AntiCrawlerEnabled bool       `json:"antiCrawlerEnabled,omitempty"`
	Author             string     `json:"author,omitempty"`
	LastUpdate         int64      `json:"lastUpdate,omitempty"`
}

// Rule represents one Kazumi anime source rule JSON file.
type Rule struct {
	API               FlexString         `json:"api"`
	Type              string             `json:"type"`
	Name              string             `json:"name"`
	Version           FlexString         `json:"version"`
	MultiSources      bool               `json:"muliSources"`
	UseWebview        bool               `json:"useWebview"`
	UseNativePlayer   bool               `json:"useNativePlayer"`
	UsePost           bool               `json:"usePost"`
	UseLegacyParser   bool               `json:"useLegacyParser"`
	AdBlocker         bool               `json:"adBlocker"`
	UserAgent         string             `json:"userAgent"`
	BaseURL           string             `json:"baseURL"`
	SearchURL         string             `json:"searchURL"`
	SearchList        string             `json:"searchList"`
	SearchName        string             `json:"searchName"`
	SearchResult      string             `json:"searchResult"`
	ChapterRoads      string             `json:"chapterRoads"`
	ChapterResult     string             `json:"chapterResult"`
	Referer           string             `json:"referer"`
	SearchMode        string             `json:"searchMode,omitempty"`
	ChapterMode       string             `json:"chapterMode,omitempty"`
	SearchAPIConfig   *APISearchConfig   `json:"searchApiConfig,omitempty"`
	ChapterAPIConfig  *APIChapterConfig  `json:"chapterApiConfig,omitempty"`
	AntiCrawlerConfig *AntiCrawlerConfig `json:"antiCrawlerConfig,omitempty"`
}

// NormalizedSearchMode returns "api" or "xpath".
func (r Rule) NormalizedSearchMode() string {
	if strings.EqualFold(strings.TrimSpace(r.SearchMode), "api") {
		return "api"
	}
	return "xpath"
}

// NormalizedChapterMode returns "api" or "xpath".
func (r Rule) NormalizedChapterMode() string {
	if strings.EqualFold(strings.TrimSpace(r.ChapterMode), "api") {
		return "api"
	}
	return "xpath"
}

// IsValid checks whether the rule has the minimum required search and chapter config.
func (r Rule) IsValid() bool {
	if strings.TrimSpace(r.Name) == "" {
		return false
	}
	hasSearch := false
	if r.NormalizedSearchMode() == "api" {
		hasSearch = r.SearchAPIConfig != nil && strings.TrimSpace(r.SearchAPIConfig.Request.URL) != ""
	} else {
		hasSearch = strings.TrimSpace(r.SearchURL) != "" && strings.TrimSpace(r.SearchList) != ""
	}
	hasChapter := false
	if r.NormalizedChapterMode() == "api" {
		hasChapter = r.ChapterAPIConfig != nil && strings.TrimSpace(r.ChapterAPIConfig.Request.URL) != ""
	} else {
		hasChapter = strings.TrimSpace(r.ChapterRoads) != "" && strings.TrimSpace(r.ChapterResult) != ""
	}
	return hasSearch && hasChapter
}

// APIRequestConfig describes an HTTP request template for API-mode rules.
type APIRequestConfig struct {
	Method   string         `json:"method"`
	URL      string         `json:"url"`
	Headers  map[string]any `json:"headers,omitempty"`
	Query    map[string]any `json:"query,omitempty"`
	BodyType string         `json:"bodyType,omitempty"` // none | json | form
	Body     any            `json:"body,omitempty"`
}

// APISearchConfig configures JSON API search extraction.
type APISearchConfig struct {
	Request        APIRequestConfig `json:"request"`
	ListPath       string           `json:"listPath"`
	NamePath       string           `json:"namePath"`
	SourcePath     string           `json:"sourcePath"`
	SourceTemplate string           `json:"sourceTemplate,omitempty"`
}

// APIEpisodePageConfig constructs an episode playback page URL from extracted values.
type APIEpisodePageConfig struct {
	URL   string         `json:"url"`
	Query map[string]any `json:"query,omitempty"`
}

// APIChapterConfig configures JSON API episode/road extraction.
type APIChapterConfig struct {
	Request          APIRequestConfig      `json:"request"`
	Format           string                `json:"format,omitempty"` // nested | delimited
	RoadsPath        string                `json:"roadsPath,omitempty"`
	RoadNamePath     string                `json:"roadNamePath,omitempty"`
	EpisodesPath     string                `json:"episodesPath,omitempty"`
	EpisodeNamePath  string                `json:"episodeNamePath,omitempty"`
	EpisodeUrlPath   string                `json:"episodeUrlPath,omitempty"`
	RoadNamesPath    string                `json:"roadNamesPath,omitempty"`
	RoadEpisodesPath string                `json:"roadEpisodesPath,omitempty"`
	RoadSeparator    string                `json:"roadSeparator,omitempty"`
	EpisodeSeparator string                `json:"episodeSeparator,omitempty"`
	FieldSeparator   string                `json:"fieldSeparator,omitempty"`
	Variables        map[string]string     `json:"variables,omitempty"`
	EpisodePage      *APIEpisodePageConfig `json:"episodePage,omitempty"`
}

// AntiCrawlerConfig describes captcha/challenge detection settings.
type AntiCrawlerConfig struct {
	Enabled            bool   `json:"enabled"`
	CaptchaType        int    `json:"captchaType"`
	CaptchaImage       string `json:"captchaImage"`
	CaptchaInput       string `json:"captchaInput"`
	CaptchaButton      string `json:"captchaButton"`
	CaptchaDetectType  int    `json:"captchaDetectType"`
	CaptchaDetectValue string `json:"captchaDetectValue"`
	CaptchaScript      string `json:"captchaScript"`
}

// Bundle is a fetched and cached collection of Kazumi rules for one subscription.
type Bundle struct {
	SourceURL string   `json:"sourceUrl"`
	IndexURL  string   `json:"indexUrl,omitempty"`
	BaseURL   string   `json:"baseUrl,omitempty"`
	Rules     []Rule   `json:"rules"`
	Warnings  []string `json:"warnings,omitempty"`
}

// SearchItem is one search result produced by a Kazumi rule.
type SearchItem struct {
	Name     string
	Src      string
	Pic      string
	Content  string
	Year     string
	Area     string
	TypeName string
	Actor    string
	Director string
	Remarks  string
}

// PageMeta holds optional detail metadata extracted from a rule page or JSON response.
type PageMeta struct {
	Title    string
	Pic      string
	Content  string
	Year     string
	Area     string
	TypeName string
	Actor    string
	Director string
	Remarks  string
}

// Episode is one playable entry in a road.
type Episode struct {
	Name string
	URL  string
}

// Road is one playback line/source with ordered episodes.
type Road struct {
	Name     string
	Episodes []Episode
}
