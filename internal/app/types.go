package app

import (
	"context"
	"io/fs"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/freelxs/jstv-player/internal/catvod"
	"github.com/freelxs/jstv-player/internal/live"
)

// Settings are kept in settings.json in the user data directory.
type Settings struct {
	// Subscriptions are the plugin addresses the user added; Active is the
	// ID of the one currently loaded.
	Subscriptions []Subscription `json:"subscriptions"`
	Active        string         `json:"active"`
	// SourceURL is the pre-0.2 single address, migrated on load.
	SourceURL string `json:"sourceUrl,omitempty"`
	NodePath  string `json:"nodePath"` // optional explicit node executable
	Player    string `json:"player"`   // builtin | potplayer | iina | senplayer | mpv | custom | system
	// CustomPlayer is the application or executable used when Player is
	// "custom". CustomArgs is an optional argv list; {url} is the media or
	// playlist address. Both are ignored for every other player.
	CustomPlayer string `json:"customPlayer,omitempty"`
	CustomArgs   string `json:"customArgs,omitempty"`
	StrictMD5    bool   `json:"strictMd5"` // refuse to run when index.js.md5 differs
	// BridgeTemplate is where the built-in play bridge (/play?vid=) finds
	// media: {vid} and {res} are substituted. Empty disables the bridge.
	BridgeTemplate string `json:"bridgeTemplate"`
	// DisableContinuous turns off 连播 (playing the following episodes).
	DisableContinuous bool `json:"disableContinuous"`
	// AllowLAN binds the plugin's HTTP server to all interfaces so its
	// configuration page is reachable from other devices. Off by default:
	// the server listens on loopback only.
	AllowLAN        bool `json:"allowLan"`
	AllowLANMedia   bool `json:"allowLanMedia"`
	InheritProxyEnv bool `json:"inheritProxyEnv"`
	InheritNodeEnv  bool `json:"inheritNodeEnv"`
	PersistLogs     bool `json:"persistLogs"`
}

// LoadResult is what LoadSource reports to the page.
type LoadResult struct {
	Sites             []catvod.Site `json:"sites"`
	MD5               string        `json:"md5"`
	ExpectedMD5       string        `json:"expectedMd5"`
	Mismatch          bool          `json:"mismatch"`
	Node              string        `json:"node"`
	Warnings          []string      `json:"warnings,omitempty"`
	Sub               Subscription  `json:"subscription"`
	TrustRequired     bool          `json:"trustRequired,omitempty"`
	TrustSHA256       string        `json:"trustSHA256,omitempty"`
	TrustURL          string        `json:"trustURL,omitempty"`
	TrustScriptSHA256 string        `json:"trustScriptSHA256,omitempty"`
	TrustConfigSHA256 string        `json:"trustConfigSHA256,omitempty"`
}

// DetailResult is a vod with its play lines parsed.
type DetailResult struct {
	Vod   catvod.M      `json:"vod"`
	Lines []catvod.Line `json:"lines"`
}

// SiteResults is one site's search answer.
type SiteResults struct {
	Site  catvod.Site `json:"site"`
	List  []any       `json:"list"`
	Error string      `json:"error,omitempty"`
}

// Player is the service the page calls (window.mygo.call("Player.X", …)).
type Player struct {
	assets fs.FS
	dir    string
	proxy  *catvod.HeaderProxy
	bridge *catvod.Bridge

	loadCancel       context.CancelFunc // guarded by mu
	sourceContext    context.Context
	catalogCache     map[string]catalogEntry // guarded by mu
	catalogEpoch     uint64
	searchHealth     map[string]siteHealth
	sourceGeneration uint64     // guarded by mu; invalidates all source-owned work
	loadMu           sync.Mutex // serializes LoadSource / switching
	initMu           sync.Mutex // serializes per-site init and permits retry after failure
	settingsMu       sync.Mutex // serializes writers of the whole settings file
	eps              episodeServer
	histMu           sync.Mutex
	libraryMu        sync.Mutex
	progress         map[string]progressSession // guarded by mu
	logMu            sync.Mutex
	logCleanupAt     time.Time // guarded by logMu; bounds cleanup cost while appending

	mu           sync.Mutex
	settings     Settings
	host         *catvod.Host
	liveCatalog  *live.Catalog
	liveAPI      string
	sites        []catvod.Site
	inited       map[string]bool
	logs         []string
	pendingTrust map[string]sourcePin

	// webMode is set only by the HTTP deployment. Desktop leaves it false,
	// so playback still opens native players and windows.
	webMode atomic.Bool

	// builtinWin is the in-window player, when one is open. The page asks
	// the loopback episode server to close it; window.close() does not.
	builtinMu  sync.Mutex
	builtinWin *mygo.Window
}
