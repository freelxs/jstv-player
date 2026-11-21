package app

import (
	"errors"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/freelxs/jstv-player/internal/atomicfile"
)

const (
	libraryBackupVersion = 1
	maxLibraryItems      = 2000
	maxVodMetaCache      = 512
)

// HistoryItem is a watch-history entry returned to the frontend library view.
type HistoryItem struct {
	Subscription string  `json:"subscription"`
	API          string  `json:"api"`
	ID           string  `json:"id"`
	ShowName     string  `json:"showName"`
	Picture      string  `json:"picture,omitempty"`
	Flag         string  `json:"flag"`
	Index        int     `json:"index"`
	EpisodeName  string  `json:"episodeName"`
	Time         int64   `json:"time"`
	Position     float64 `json:"position,omitempty"`
	Duration     float64 `json:"duration,omitempty"`
	Watched      bool    `json:"watched,omitempty"`
}

// LibraryBackup is the portable JSON format for exporting and importing
// favorites and watch history for the active subscription.
type LibraryBackup struct {
	Version    int           `json:"version"`
	ExportedAt int64         `json:"exportedAt"`
	Favorites  []Favorite    `json:"favorites"`
	History    []HistoryItem `json:"history"`
}

// LibraryImportResult reports how many entries were added or updated.
type LibraryImportResult struct {
	FavoritesAdded   int `json:"favoritesAdded"`
	FavoritesUpdated int `json:"favoritesUpdated"`
	HistoryAdded     int `json:"historyAdded"`
	HistoryUpdated   int `json:"historyUpdated"`
}

func (p *Player) rememberVodMeta(api, vodID, name, picture string) {
	name = strings.TrimSpace(name)
	picture = strings.TrimSpace(picture)
	if vodID == "" || (name == "" && picture == "") {
		return
	}
	key := persistentAPI(api) + "\x00" + vodID
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.vodMeta == nil {
		p.vodMeta = map[string]vodMetaEntry{}
	}
	if len(p.vodMeta) >= maxVodMetaCache {
		for existing := range p.vodMeta {
			delete(p.vodMeta, existing)
			break
		}
	}
	p.vodMeta[key] = vodMetaEntry{Name: name, Picture: picture}
}

func (p *Player) lookupVodPicture(api, vodID string) string {
	if vodID == "" {
		return ""
	}
	key := persistentAPI(api) + "\x00" + vodID
	p.mu.Lock()
	meta := p.vodMeta[key]
	p.mu.Unlock()
	return meta.Picture
}

func parseScopedHistoryKey(key string) (subID, api, vodID string, ok bool) {
	parts := strings.Split(key, "\x00")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// ListHistory returns watch history for the active subscription, newest first.
func (p *Player) ListHistory() ([]HistoryItem, error) {
	subscription := p.activeSubID()
	if subscription == "" {
		return []HistoryItem{}, nil
	}
	p.mu.Lock()
	liveAPI := p.liveAPI
	p.mu.Unlock()

	p.libraryMu.Lock()
	favorites, _ := p.readFavorites()
	p.libraryMu.Unlock()

	p.histMu.Lock()
	history := p.loadHistoryLocked(subscription)
	p.histMu.Unlock()
	if history == nil {
		return nil, errors.New("无法读取播放记录")
	}
	out := make([]HistoryItem, 0, len(history))
	for key, entry := range history {
		subID, api, vodID, ok := parseScopedHistoryKey(key)
		if !ok || subID != subscription {
			continue
		}
		showName := entry.ShowName
		picture := entry.Picture
		if fav, exists := favorites[key]; exists {
			if showName == "" {
				showName = fav.Name
			}
			if picture == "" {
				picture = fav.Picture
			}
		}
		if showName == "" {
			showName = entry.Name
		}
		if showName == "" {
			showName = vodID
		}
		resolvedAPI := api
		if resolvedAPI == "live" && liveAPI != "" {
			resolvedAPI = liveAPI
		}
		out = append(out, HistoryItem{
			Subscription: subID,
			API:          resolvedAPI,
			ID:           vodID,
			ShowName:     showName,
			Picture:      picture,
			Flag:         entry.Flag,
			Index:        entry.Index,
			EpisodeName:  entry.Name,
			Time:         entry.Time,
			Position:     entry.Position,
			Duration:     entry.Duration,
			Watched:      entry.Watched,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Time != out[j].Time {
			return out[i].Time > out[j].Time
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// RemoveHistory deletes one show from the active subscription's watch history.
func (p *Player) RemoveHistory(api, vodID string) error {
	if api == "" || vodID == "" {
		return errors.New("播放记录标识无效")
	}
	subscription := p.activeSubID()
	if subscription == "" {
		return errNoSub
	}
	p.histMu.Lock()
	defer p.histMu.Unlock()
	history := p.loadHistoryLocked(subscription)
	if history == nil {
		return errors.New("无法读取播放记录")
	}
	key := historyKey(subscription, api, vodID)
	if _, exists := history[key]; !exists {
		return nil
	}
	delete(history, key)
	return atomicfile.JSON(filepath.Join(p.dir, "history.json"), history)
}

// ClearHistory removes all watch history entries for the active subscription.
func (p *Player) ClearHistory() error {
	subscription := p.activeSubID()
	if subscription == "" {
		return errNoSub
	}
	p.histMu.Lock()
	defer p.histMu.Unlock()
	history := p.loadHistoryLocked(subscription)
	if history == nil {
		return errors.New("无法读取播放记录")
	}
	changed := false
	for key := range history {
		subID, _, _, ok := parseScopedHistoryKey(key)
		if ok && subID == subscription {
			delete(history, key)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return atomicfile.JSON(filepath.Join(p.dir, "history.json"), history)
}

// SetWatched toggles the watched status of a show in the active subscription.
func (p *Player) SetWatched(api, vodID string, watched bool) error {
	if api == "" || vodID == "" || len(api) > 2048 || len(vodID) > 8192 {
		return errors.New("播放记录标识无效")
	}
	subscription := p.activeSubID()
	if subscription == "" {
		return errNoSub
	}
	p.histMu.Lock()
	defer p.histMu.Unlock()
	history := p.loadHistoryLocked(subscription)
	if history == nil {
		return errors.New("无法读取播放记录")
	}
	key := historyKey(subscription, api, vodID)
	entry, exists := history[key]
	if !exists {
		if !watched {
			return nil
		}
		metaKey := persistentAPI(api) + "\x00" + vodID
		p.mu.Lock()
		meta := p.vodMeta[metaKey]
		p.mu.Unlock()
		entry = HistoryEntry{
			ShowName: meta.Name,
			Picture:  meta.Picture,
			Time:     time.Now().Unix(),
		}
	}
	entry.Watched = watched
	if watched {
		entry.Position = 0
	}
	if entry.Time == 0 {
		entry.Time = time.Now().Unix()
	}
	history[key] = entry
	return atomicfile.JSON(filepath.Join(p.dir, "history.json"), history)
}

// ExportLibrary exports the active subscription's favorites and watch history.
func (p *Player) ExportLibrary() (LibraryBackup, error) {
	subscription := p.activeSubID()
	if subscription == "" {
		return LibraryBackup{}, errNoSub
	}
	favorites, err := p.GetFavorites()
	if err != nil {
		return LibraryBackup{}, err
	}
	history, err := p.ListHistory()
	if err != nil {
		return LibraryBackup{}, err
	}
	exportedFavs := make([]Favorite, 0, len(favorites))
	for _, fav := range favorites {
		fav.Subscription = ""
		fav.API = persistentAPI(fav.API)
		exportedFavs = append(exportedFavs, fav)
	}
	exportedHist := make([]HistoryItem, 0, len(history))
	for _, item := range history {
		item.Subscription = ""
		item.API = persistentAPI(item.API)
		exportedHist = append(exportedHist, item)
	}
	return LibraryBackup{
		Version:    libraryBackupVersion,
		ExportedAt: time.Now().Unix(),
		Favorites:  exportedFavs,
		History:    exportedHist,
	}, nil
}

func validTiming(position, duration float64) (float64, float64, bool) {
	if math.IsNaN(position) || math.IsInf(position, 0) || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return 0, 0, false
	}
	if position < 0 || duration < 0 || duration > 30*24*3600 {
		return 0, 0, false
	}
	if duration > 0 && position > duration {
		position = duration
	}
	return position, duration, true
}

// ImportLibrary merges a backup into the active subscription's favorites and
// watch history. Newer timestamps win and existing entries are preserved.
func (p *Player) ImportLibrary(backup LibraryBackup) (LibraryImportResult, error) {
	if backup.Version != 0 && backup.Version != libraryBackupVersion {
		return LibraryImportResult{}, errors.New("不支持的媒体库备份版本")
	}
	if len(backup.Favorites) > maxLibraryItems || len(backup.History) > maxLibraryItems {
		return LibraryImportResult{}, errors.New("备份条目数超过上限（2000）")
	}
	subscription := p.activeSubID()
	if subscription == "" {
		return LibraryImportResult{}, errNoSub
	}
	var result LibraryImportResult

	p.libraryMu.Lock()
	favorites, err := p.readFavorites()
	if err != nil {
		p.libraryMu.Unlock()
		return LibraryImportResult{}, err
	}
	favChanged := false
	for _, item := range backup.Favorites {
		api := persistentAPI(strings.TrimSpace(item.API))
		id := strings.TrimSpace(item.ID)
		name := strings.TrimSpace(item.Name)
		pic := strings.TrimSpace(item.Picture)
		if api == "" || id == "" || name == "" || len(api) > 2048 || len(id) > 8192 || len(name) > 512 || len(pic) > 8192 {
			continue
		}
		ts := item.Time
		if ts <= 0 {
			ts = time.Now().Unix()
		}
		key := historyKey(subscription, api, id)
		old, exists := favorites[key]
		if !exists {
			if len(favorites) >= maxLibraryItems {
				continue
			}
			favorites[key] = Favorite{Subscription: subscription, API: api, ID: id, Name: name, Picture: pic, Time: ts}
			result.FavoritesAdded++
			favChanged = true
		} else if ts > old.Time {
			favorites[key] = Favorite{Subscription: subscription, API: api, ID: id, Name: name, Picture: pic, Time: ts}
			result.FavoritesUpdated++
			favChanged = true
		}
	}
	if favChanged {
		if err := atomicfile.JSON(filepath.Join(p.dir, "favorites.json"), favorites); err != nil {
			p.libraryMu.Unlock()
			return LibraryImportResult{}, err
		}
	}
	p.libraryMu.Unlock()

	p.histMu.Lock()
	history := p.loadHistoryLocked(subscription)
	if history == nil {
		p.histMu.Unlock()
		return LibraryImportResult{}, errors.New("无法读取播放记录")
	}
	histChanged := false
	for _, item := range backup.History {
		api := persistentAPI(strings.TrimSpace(item.API))
		id := strings.TrimSpace(item.ID)
		if api == "" || id == "" || len(api) > 2048 || len(id) > 8192 || len(item.ShowName) > 512 || len(item.Picture) > 8192 || len(item.Flag) > 512 || len(item.EpisodeName) > 512 || item.Index < 0 {
			continue
		}
		pos, dur, ok := validTiming(item.Position, item.Duration)
		if !ok {
			continue
		}
		ts := item.Time
		if ts <= 0 {
			ts = time.Now().Unix()
		}
		key := historyKey(subscription, api, id)
		next := HistoryEntry{
			Flag:     item.Flag,
			Index:    item.Index,
			Name:     item.EpisodeName,
			Time:     ts,
			Position: pos,
			Duration: dur,
			ShowName: strings.TrimSpace(item.ShowName),
			Picture:  strings.TrimSpace(item.Picture),
			Watched:  item.Watched,
		}
		old, exists := history[key]
		if !exists {
			if len(history) >= maxLibraryItems {
				continue
			}
			history[key] = next
			result.HistoryAdded++
			histChanged = true
		} else if ts > old.Time {
			if next.ShowName == "" {
				next.ShowName = old.ShowName
			}
			if next.Picture == "" {
				next.Picture = old.Picture
			}
			history[key] = next
			result.HistoryUpdated++
			histChanged = true
		}
	}
	if histChanged {
		if err := atomicfile.JSON(filepath.Join(p.dir, "history.json"), history); err != nil {
			p.histMu.Unlock()
			return LibraryImportResult{}, err
		}
	}
	p.histMu.Unlock()

	return result, nil
}
