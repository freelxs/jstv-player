package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

type progressSession struct {
	subscription, api, vodID string
	entry                    HistoryEntry
	generation               uint64
	lastWrite                time.Time
	expires                  time.Time
}

// Progress tokens identify a server-owned history entry. A page cannot choose
// another subscription, show or episode when reporting its current position.
func (p *Player) newProgressSession(subscription, api, vodID string, entry HistoryEntry, generation uint64) (string, float64) {
	if vodID == "" || persistentAPI(api) == "live" {
		return "", 0
	}
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", 0
	}
	token := hex.EncodeToString(random[:])
	p.histMu.Lock()
	history := p.loadHistoryLocked(subscription)
	old := history[historyKey(subscription, api, vodID)]
	p.histMu.Unlock()
	position := float64(0)
	if old.Flag == entry.Flag && old.Index == entry.Index {
		position = old.Position
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if generation != p.sourceGeneration {
		return "", 0
	}
	if p.progress == nil {
		p.progress = map[string]progressSession{}
	}
	now := time.Now()
	for id, session := range p.progress {
		if now.After(session.expires) || (session.subscription == subscription && session.api == api && session.vodID == vodID) {
			delete(p.progress, id)
		}
	}
	if len(p.progress) >= 128 {
		for id := range p.progress {
			delete(p.progress, id)
			break
		}
	}
	p.progress[token] = progressSession{subscription: subscription, api: api, vodID: vodID, entry: entry, generation: p.sourceGeneration, expires: now.Add(12 * time.Hour)}
	return token, position
}

// ReportProgress coalesces normal updates to one disk write per ten seconds.
// Pausing, seeking, closing and ending may flush at most once per second.
func (p *Player) ReportProgress(token string, position, duration float64, final bool) error {
	if math.IsNaN(position) || math.IsNaN(duration) || math.IsInf(position, 0) || math.IsInf(duration, 0) || position < 0 || duration <= 0 || duration > 30*24*3600 {
		return errors.New("播放进度无效")
	}
	p.mu.Lock()
	session, ok := p.progress[token]
	now := time.Now()
	if !ok || session.generation != p.sourceGeneration || now.After(session.expires) {
		p.mu.Unlock()
		return errors.New("播放记录会话已失效")
	}
	interval := 10 * time.Second
	if final {
		interval = time.Second
	}
	if now.Sub(session.lastWrite) < interval {
		p.mu.Unlock()
		return nil
	}
	session.lastWrite = now
	p.progress[token] = session
	p.mu.Unlock()
	entry := session.entry
	entry.Duration = duration
	entry.Position = min(position, duration)
	if final && duration-entry.Position <= 3 {
		entry.Position = 0
	}
	p.recordHistory(session.subscription, session.api, session.vodID, entry)
	return nil
}

func (p *Player) serveProgress(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(204)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method", 405)
		return
	}
	var body struct {
		Position float64 `json:"position"`
		Duration float64 `json:"duration"`
		Final    bool    `json:"final"`
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 4097))
	if err != nil || len(data) > 4096 || json.Unmarshal(data, &body) != nil {
		http.Error(w, "进度请求无效", 400)
		return
	}
	if err := p.ReportProgress(strings.TrimPrefix(r.URL.Path, "/progress/"), body.Position, body.Duration, body.Final); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	w.WriteHeader(204)
}
